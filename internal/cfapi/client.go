// Package cfapi is a small client for the Cloudflare API calls FELDCORE needs:
// creating a tunnel, writing its ingress rules, and upserting DNS records.
// The API token is sent as a header and is removed from any error text.
package cfapi

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/secrets"
)

const defaultBase = "https://api.cloudflare.com/client/v4"

// Client talks to the Cloudflare API.
type Client struct {
	Base  string
	Token string
	HTTP  *http.Client
}

// Account is a Cloudflare account the token can see.
type Account struct {
	ID   string `json:"id"`
	Name string `json:"name"`
}

// Tunnel is a remotely-managed cloudflared tunnel.
type Tunnel struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Status      string `json:"status"`
	Token       string `json:"token,omitempty"`
	Connections []struct {
		ID string `json:"id"`
	} `json:"connections"`
}

// Rule is one ingress rule. A catch-all has no hostname.
type Rule struct {
	Hostname string `json:"hostname,omitempty"`
	Service  string `json:"service"`
}

func (c *Client) http() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}
	return &http.Client{Timeout: 30 * time.Second}
}

func (c *Client) base() string {
	if c.Base != "" {
		return strings.TrimRight(c.Base, "/")
	}
	return defaultBase
}

// Accounts lists accounts this token can see.
func (c *Client) Accounts(ctx context.Context) ([]Account, error) {
	var out []Account
	if err := c.call(ctx, http.MethodGet, "/accounts?per_page=50", nil, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// CreateTunnel creates a remotely-managed tunnel and returns its connector token.
func (c *Client) CreateTunnel(ctx context.Context, accountID, name string) (Tunnel, error) {
	var out Tunnel
	body := map[string]string{"name": name, "config_src": "cloudflare"}
	err := c.call(ctx, http.MethodPost, "/accounts/"+url.PathEscape(accountID)+"/cfd_tunnel", body, &out)
	return out, err
}

// GetTunnel returns a tunnel, including how many connectors are attached.
func (c *Client) GetTunnel(ctx context.Context, accountID, id string) (Tunnel, error) {
	var out Tunnel
	err := c.call(ctx, http.MethodGet, "/accounts/"+url.PathEscape(accountID)+"/cfd_tunnel/"+url.PathEscape(id), nil, &out)
	return out, err
}

// PutIngress replaces the tunnel's ingress rules. The last rule must be the catch-all.
func (c *Client) PutIngress(ctx context.Context, accountID, id string, rules []Rule) error {
	body := map[string]any{"config": map[string]any{"ingress": rules}}
	return c.call(ctx, http.MethodPut, "/accounts/"+url.PathEscape(accountID)+"/cfd_tunnel/"+url.PathEscape(id)+"/configurations", body, nil)
}

// Record is a DNS record FELDCORE created.
type Record struct {
	ID      string `json:"id"`
	Type    string `json:"type"`
	Name    string `json:"name"`
	Content string `json:"content"`
}

// UpsertCNAME ensures hostname is a proxied CNAME to target. It returns the record id.
func (c *Client) UpsertCNAME(ctx context.Context, hostname, target string) (string, error) {
	zone, err := c.zoneFor(ctx, hostname)
	if err != nil {
		return "", err
	}
	existing, err := c.findCNAME(ctx, zone, hostname)
	if err != nil {
		return "", err
	}
	body := map[string]any{
		"type": "CNAME", "name": hostname, "content": target, "proxied": true, "ttl": 1,
	}
	if existing.ID != "" {
		if existing.Content == target {
			return existing.ID, nil
		}
		var out Record
		err := c.call(ctx, http.MethodPut, "/zones/"+url.PathEscape(zone)+"/dns_records/"+url.PathEscape(existing.ID), body, &out)
		return out.ID, err
	}
	var out Record
	err = c.call(ctx, http.MethodPost, "/zones/"+url.PathEscape(zone)+"/dns_records", body, &out)
	return out.ID, err
}

// DeleteRecord removes a DNS record by id. An unknown id is not an error.
func (c *Client) DeleteRecord(ctx context.Context, hostname, recordID string) error {
	if recordID == "" {
		return nil
	}
	zone, err := c.zoneFor(ctx, hostname)
	if err != nil {
		return err
	}
	err = c.call(ctx, http.MethodDelete, "/zones/"+url.PathEscape(zone)+"/dns_records/"+url.PathEscape(recordID), nil, nil)
	if err == nil {
		return nil
	}
	if strings.Contains(strings.ToLower(err.Error()), "not found") {
		return nil
	}
	return err
}

func (c *Client) zoneFor(ctx context.Context, hostname string) (string, error) {
	for _, name := range zoneCandidates(hostname) {
		var zones []struct {
			ID   string `json:"id"`
			Name string `json:"name"`
		}
		q := "/zones?name=" + url.QueryEscape(name)
		if err := c.call(ctx, http.MethodGet, q, nil, &zones); err != nil {
			return "", err
		}
		if len(zones) > 0 && zones[0].ID != "" {
			return zones[0].ID, nil
		}
	}
	return "", fmt.Errorf("Cloudflare has no DNS zone for %s. Add that domain to Cloudflare, or pick a hostname on a domain already there", hostname)
}

func (c *Client) findCNAME(ctx context.Context, zone, hostname string) (Record, error) {
	var recs []Record
	q := "/zones/" + url.PathEscape(zone) + "/dns_records?type=CNAME&name=" + url.QueryEscape(hostname)
	if err := c.call(ctx, http.MethodGet, q, nil, &recs); err != nil {
		return Record{}, err
	}
	if len(recs) == 0 {
		return Record{}, nil
	}
	return recs[0], nil
}

func zoneCandidates(host string) []string {
	host = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(host)), ".")
	parts := strings.Split(host, ".")
	var out []string
	for i := 0; i < len(parts)-1; i++ {
		out = append(out, strings.Join(parts[i:], "."))
	}
	return out
}

type envelope struct {
	Success bool                       `json:"success"`
	Errors  []struct{ Message string } `json:"errors"`
	Result  json.RawMessage            `json:"result"`
}

func (c *Client) call(ctx context.Context, method, path string, body, out any) error {
	var r io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return err
		}
		r = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.base()+path, r)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+c.Token)
	req.Header.Set("Content-Type", "application/json")
	resp, err := c.http().Do(req)
	if err != nil {
		return fmt.Errorf("could not reach Cloudflare: %s", secrets.Redact(err.Error(), c.Token))
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	var env envelope
	if err := json.Unmarshal(raw, &env); err != nil {
		return fmt.Errorf("Cloudflare returned an unexpected response (%d)", resp.StatusCode)
	}
	if !env.Success {
		msg := "Cloudflare refused the request"
		if len(env.Errors) > 0 && env.Errors[0].Message != "" {
			msg = env.Errors[0].Message
		}
		return fmt.Errorf("%s", secrets.Redact(msg, c.Token))
	}
	if out != nil && len(env.Result) > 0 && string(env.Result) != "null" {
		if err := json.Unmarshal(env.Result, out); err != nil {
			return fmt.Errorf("could not read Cloudflare's response")
		}
	}
	return nil
}

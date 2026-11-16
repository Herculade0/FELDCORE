// Package docker is a small client for the Docker Engine API over its unix
// socket, plus a manager that exposes just what the FELDCORE UI needs.
//
// It deliberately avoids the official Docker SDK: that module is very large and
// would dominate the binary. The Engine API is a plain versioned REST API, and
// FELDCORE uses a small, well-documented subset of it.
package docker

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultSocket is where the Docker daemon listens on most systems.
const DefaultSocket = "/var/run/docker.sock"

// maxAPIVersion caps the Engine API version FELDCORE will request. The daemon
// refuses versions newer than it supports, so the client negotiates down to
// the daemon's version; this cap keeps it from requesting something newer than
// the API this code was written against.
const maxAPIVersion = "1.45"

// APIError is a non-2xx response from the daemon, with the message it returned.
type APIError struct {
	Status  int
	Message string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("docker returned HTTP %d", e.Status)
}

// NotFound reports whether err is a 404 from the daemon.
func NotFound(err error) bool {
	var ae *APIError
	return asAPIError(err, &ae) && ae.Status == http.StatusNotFound
}

// Client talks to the Docker daemon over a unix socket.
type Client struct {
	socket string
	http   *http.Client

	mu         sync.Mutex
	apiVersion string // negotiated "/v1.xx" path prefix, empty until first use
}

// NewClient returns a client for the given socket path.
func NewClient(socket string) *Client {
	if socket == "" {
		socket = DefaultSocket
	}
	return &Client{
		socket: socket,
		http: &http.Client{
			Transport: &http.Transport{
				DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
					var d net.Dialer
					return d.DialContext(ctx, "unix", socket)
				},
				DisableCompression: true,
				MaxIdleConns:       4,
				IdleConnTimeout:    30 * time.Second,
			},
		},
	}
}

// prefix returns the negotiated version path prefix (e.g. "/v1.44"), resolving
// it once against the daemon. On any failure it falls back to no prefix, which
// the daemon serves at its own default version.
func (c *Client) prefix(ctx context.Context) string {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.apiVersion != "" {
		return c.apiVersion
	}
	c.apiVersion = "/v" + maxAPIVersion // sensible default if negotiation fails
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, "http://docker/version", nil)
	if err == nil {
		if resp, err := c.http.Do(req); err == nil {
			defer drain(resp)
			var v struct{ ApiVersion string }
			if json.NewDecoder(resp.Body).Decode(&v) == nil && v.ApiVersion != "" {
				c.apiVersion = "/v" + minVersion(v.ApiVersion, maxAPIVersion)
			}
		}
	}
	return c.apiVersion
}

// do issues a request to path (which must start with "/") and returns the raw
// response. The caller closes the body.
func (c *Client) do(ctx context.Context, method, path string, query url.Values, body io.Reader, contentType string) (*http.Response, error) {
	u := "http://docker" + c.prefix(ctx) + path
	if len(query) > 0 {
		u += "?" + query.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, method, u, body)
	if err != nil {
		return nil, err
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, dialError(err)
	}
	if resp.StatusCode >= 400 {
		defer drain(resp)
		return nil, &APIError{Status: resp.StatusCode, Message: errorMessage(resp.Body)}
	}
	return resp, nil
}

// getJSON performs a GET and decodes the JSON body into out.
func (c *Client) getJSON(ctx context.Context, path string, query url.Values, out any) error {
	resp, err := c.do(ctx, http.MethodGet, path, query, nil, "")
	if err != nil {
		return err
	}
	defer drain(resp)
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// postJSON performs a POST with an optional JSON body and optional JSON result.
func (c *Client) postJSON(ctx context.Context, path string, query url.Values, in, out any) error {
	var body io.Reader
	ct := ""
	if in != nil {
		raw, err := json.Marshal(in)
		if err != nil {
			return err
		}
		body = strings.NewReader(string(raw))
		ct = "application/json"
	}
	resp, err := c.do(ctx, http.MethodPost, path, query, body, ct)
	if err != nil {
		return err
	}
	defer drain(resp)
	if out == nil {
		return nil
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

// errorMessage extracts the daemon's {"message": "..."} error text.
func errorMessage(r io.Reader) string {
	var e struct {
		Message string `json:"message"`
	}
	raw, _ := io.ReadAll(io.LimitReader(r, 8<<10))
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		return e.Message
	}
	return strings.TrimSpace(string(raw))
}

// drain closes a response body, reading any remainder so the connection can be
// reused.
func drain(resp *http.Response) {
	if resp == nil || resp.Body == nil {
		return
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 4<<10))
	resp.Body.Close()
}

// dialError turns a raw socket failure into a friendlier message.
func dialError(err error) error {
	msg := err.Error()
	switch {
	case strings.Contains(msg, "no such file"):
		return fmt.Errorf("the Docker socket was not found")
	case strings.Contains(msg, "permission denied"):
		return fmt.Errorf("permission denied on the Docker socket")
	case strings.Contains(msg, "connection refused"):
		return fmt.Errorf("the Docker daemon is not running")
	}
	return err
}

// minVersion returns the lower of two "major.minor" API versions.
func minVersion(a, b string) string {
	if cmpVersion(a, b) <= 0 {
		return a
	}
	return b
}

func cmpVersion(a, b string) int {
	var a1, a2, b1, b2 int
	fmt.Sscanf(a, "%d.%d", &a1, &a2)
	fmt.Sscanf(b, "%d.%d", &b1, &b2)
	switch {
	case a1 != b1:
		return a1 - b1
	default:
		return a2 - b2
	}
}

// scanLines returns a bufio.Scanner with a large buffer, for line-delimited
// JSON streams (pull progress, events).
func scanLines(r io.Reader) *bufio.Scanner {
	s := bufio.NewScanner(r)
	s.Buffer(make([]byte, 0, 64<<10), 1<<20)
	return s
}

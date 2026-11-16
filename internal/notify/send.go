package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/events"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

func postMessage(ctx context.Context, client *http.Client, method, rawURL, contentType string, body []byte, headers map[string]string, hidden ...string) error {
	req, err := http.NewRequestWithContext(ctx, method, rawURL, bytes.NewReader(body))
	if err != nil {
		return op("The notification address is not valid.")
	}
	if contentType != "" {
		req.Header.Set("Content-Type", contentType)
	}
	req.Header.Set("User-Agent", "FELDCORE")
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return op(scrubDelivery(err.Error(), rawURL, hidden...))
	}
	defer resp.Body.Close()
	slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	if resp.StatusCode >= 300 {
		detail := scrubDelivery(string(slurp), rawURL, hidden...)
		if detail == "" {
			detail = "The notification service refused the message."
		}
		return op(detail)
	}
	return nil
}

func scrubDelivery(msg, rawURL string, hidden ...string) string {
	msg = strings.TrimSpace(msg)
	if rawURL != "" {
		msg = strings.ReplaceAll(msg, rawURL, "the notification server")
	}
	if u, err := url.Parse(rawURL); err == nil && u.Path != "" && u.Path != "/" {
		msg = strings.ReplaceAll(msg, u.Path, "/…")
	}
	msg = secrets.Redact(msg, hidden...)
	msg = strings.Join(strings.Fields(msg), " ")
	if len(msg) > 240 {
		msg = msg[:240]
	}
	return msg
}

func deliverChannel(ctx context.Context, client *http.Client, ch store.NotifyChannel, secret string, ev events.Event) error {
	switch ch.Kind {
	case "ntfy":
		return deliverNtfy(ctx, client, ch, secret, ev)
	case "gotify":
		return deliverGotify(ctx, client, ch, secret, ev)
	case "discord":
		return deliverDiscord(ctx, client, secret, ev)
	case "slack":
		return deliverSlack(ctx, client, secret, ev)
	case "webhook":
		return deliverWebhook(ctx, client, ch, secret, ev)
	default:
		return input("That channel is not a kind FELDCORE can send.")
	}
}

func deliverNtfy(ctx context.Context, client *http.Client, ch store.NotifyChannel, secret string, ev events.Event) error {
	endpoint := strings.TrimRight(strings.TrimSpace(ch.URL), "/")
	topic := strings.Trim(strings.TrimSpace(ch.Topic), "/")
	if topic != "" && !strings.HasSuffix(endpoint, "/"+topic) {
		endpoint += "/" + topic
	}
	body := ev.Body
	if body == "" {
		body = ev.Title
	}
	headers := map[string]string{
		"Title":    oneLine(ev.Title, 180),
		"Priority": ntfyPriority(ev.Severity),
	}
	if secret != "" {
		headers["Authorization"] = "Bearer " + secret
	}
	return postMessage(ctx, client, http.MethodPost, endpoint, "text/plain; charset=utf-8", []byte(body), headers, secret, endpoint)
}

func ntfyPriority(s events.Severity) string {
	switch s {
	case events.Critical:
		return "5"
	case events.Warning:
		return "4"
	default:
		return "3"
	}
}

func deliverGotify(ctx context.Context, client *http.Client, ch store.NotifyChannel, secret string, ev events.Event) error {
	endpoint := strings.TrimRight(strings.TrimSpace(ch.URL), "/")
	if !strings.HasSuffix(endpoint, "/message") {
		endpoint += "/message"
	}
	payload, _ := json.Marshal(map[string]any{
		"title":    oneLine(ev.Title, 180),
		"message":  clipText(ev.Body, 4000),
		"priority": gotifyPriority(ev.Severity),
	})
	return postMessage(ctx, client, http.MethodPost, endpoint, "application/json", payload, map[string]string{
		"X-Gotify-Key": secret,
	}, secret, endpoint)
}

func gotifyPriority(s events.Severity) int {
	switch s {
	case events.Critical:
		return 10
	case events.Warning:
		return 7
	default:
		return 4
	}
}

func deliverDiscord(ctx context.Context, client *http.Client, webhook string, ev events.Event) error {
	content := "**" + oneLine(ev.Title, 180) + "**\n" + clipText(ev.Body, 1700)
	content = strings.ReplaceAll(content, "@everyone", "@ everyone")
	content = strings.ReplaceAll(content, "@here", "@ here")
	payload, _ := json.Marshal(map[string]string{"content": content})
	return postMessage(ctx, client, http.MethodPost, webhook, "application/json", payload, nil, webhook)
}

func deliverSlack(ctx context.Context, client *http.Client, webhook string, ev events.Event) error {
	text := "*" + oneLine(ev.Title, 180) + "*\n" + clipText(ev.Body, 3000)
	payload, _ := json.Marshal(map[string]string{"text": text})
	return postMessage(ctx, client, http.MethodPost, webhook, "application/json", payload, nil, webhook)
}

func deliverWebhook(ctx context.Context, client *http.Client, ch store.NotifyChannel, secret string, ev events.Event) error {
	payload, err := json.Marshal(hookBody{
		Name: ev.Name, Severity: string(ev.Severity), Title: ev.Title, Body: ev.Body,
		Source: ev.Source, At: ev.At, Fields: ev.Fields,
	})
	if err != nil {
		return op("The notification could not be encoded.")
	}
	headers := map[string]string{}
	if secret != "" {
		headers["Authorization"] = "Bearer " + secret
	}
	return postMessage(ctx, client, http.MethodPost, ch.URL, "application/json", payload, headers, secret, ch.URL)
}

type hookBody struct {
	Name     string            `json:"name"`
	Severity string            `json:"severity"`
	Title    string            `json:"title"`
	Body     string            `json:"body"`
	Source   string            `json:"source,omitempty"`
	At       time.Time         `json:"at"`
	Fields   map[string]string `json:"fields,omitempty"`
}

func oneLine(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	if len(s) > n {
		return s[:n]
	}
	return s
}

func clipText(s string, n int) string {
	s = strings.TrimSpace(s)
	if len(s) > n {
		return s[:n]
	}
	return s
}

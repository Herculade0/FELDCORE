package notify

import (
	"crypto/rand"
	"encoding/hex"
	"net/url"
	"strings"

	"github.com/Herculade0/feldcore/internal/store"
)

// ChannelInput is one channel form. Secret is write-only. For Discord and
// Slack the secret is the webhook URL, because that URL is the credential.
type ChannelInput struct {
	ID      string `json:"id"`
	Kind    string `json:"kind"`
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"`
	URL     string `json:"url"`
	Topic   string `json:"topic"`
	Secret  string `json:"secret,omitempty"`
}

// PolicyInput is quiet hours, de-duplication, and the in-app bell.
type PolicyInput struct {
	InAppEnabled  bool              `json:"inAppEnabled"`
	Quiet         store.NotifyQuiet `json:"quiet"`
	DedupeSeconds int               `json:"dedupeSeconds"`
	RatePerHour   int               `json:"ratePerHour"`
}

// RoutesInput replaces the routing table.
type RoutesInput struct {
	Routes []store.NotifyRoute `json:"routes"`
}

func secretName(id string) string { return "notify-" + id }

// SaveChannel creates or updates one channel. An empty secret keeps the stored one.
func (s *Service) SaveChannel(in ChannelInput) error {
	kind := strings.TrimSpace(in.Kind)
	switch kind {
	case "ntfy", "gotify", "discord", "slack", "webhook":
	default:
		return input("Choose ntfy, Gotify, Discord, Slack, or a webhook.")
	}
	name := strings.TrimSpace(in.Name)
	if name == "" || len(name) > 40 {
		return input("Name the channel with up to 40 characters.")
	}
	if kind == "discord" || kind == "slack" {
		if strings.TrimSpace(in.Secret) == "" && strings.TrimSpace(in.URL) != "" {
			in.Secret = strings.TrimSpace(in.URL)
		}
		in.URL = ""
	}
	in.URL = strings.TrimSpace(in.URL)
	in.Topic = strings.TrimSpace(in.Topic)
	in.Secret = strings.TrimSpace(in.Secret)
	if err := validateChannel(kind, in.URL, in.Topic, in.Secret, false); err != nil {
		return err
	}

	n := s.Store.Snapshot().Notify
	idx := -1
	if in.ID != "" {
		if !idRe(in.ID) {
			return input("That channel was not found.")
		}
		for i, c := range n.Channels {
			if c.ID == in.ID {
				idx = i
				break
			}
		}
		if idx < 0 {
			return input("That channel was not found.")
		}
	}
	hasSecret := in.Secret != ""
	if idx >= 0 && !hasSecret {
		hasSecret = n.Channels[idx].HasSecret
	}
	if !hasSecret && secretRequired(kind) {
		return input("Enter the token or webhook address.")
	}
	if kind == "discord" || kind == "slack" {
		if in.Secret != "" {
			if err := validateWebhook(kind, in.Secret); err != nil {
				return err
			}
		}
	} else if err := validateChannel(kind, in.URL, in.Topic, in.Secret, true); err != nil {
		return err
	}

	id := in.ID
	if id == "" {
		var err error
		id, err = newNotifyID()
		if err != nil {
			return op("Could not save the channel.")
		}
		if len(n.Channels) >= 12 {
			return input("Twelve channels is the limit.")
		}
	}
	if in.Secret != "" {
		if s.Secrets == nil {
			return op("Could not store the channel secret.")
		}
		if err := s.Secrets.Put(secretName(id), in.Secret); err != nil {
			return op("Could not store the channel secret.")
		}
		hasSecret = true
	}
	ch := store.NotifyChannel{
		ID: id, Kind: kind, Name: name, Enabled: in.Enabled,
		URL: in.URL, Topic: in.Topic, HasSecret: hasSecret,
	}
	return s.Store.Update(func(st *store.State) error {
		list := st.Notify.Channels
		if in.ID == "" {
			st.Notify.Channels = append(list, ch)
			return nil
		}
		for i := range list {
			if list[i].ID == id {
				list[i] = ch
				st.Notify.Channels = list
				return nil
			}
		}
		return input("That channel was not found.")
	})
}

// DeleteChannel removes a channel, its secret, and any routes that named it.
func (s *Service) DeleteChannel(id string) error {
	if !idRe(id) {
		return input("That channel was not found.")
	}
	if s.Secrets != nil {
		_ = s.Secrets.Put(secretName(id), "")
	}
	return s.Store.Update(func(st *store.State) error {
		next := make([]store.NotifyChannel, 0, len(st.Notify.Channels))
		found := false
		for _, c := range st.Notify.Channels {
			if c.ID == id {
				found = true
				continue
			}
			next = append(next, c)
		}
		if !found {
			return input("That channel was not found.")
		}
		st.Notify.Channels = next
		var routes []store.NotifyRoute
		for _, r := range st.Notify.Routes {
			kept := make([]string, 0, len(r.Channels))
			for _, c := range r.Channels {
				if c != id {
					kept = append(kept, c)
				}
			}
			if len(kept) == 0 {
				continue
			}
			r.Channels = kept
			routes = append(routes, r)
		}
		st.Notify.Routes = routes
		return nil
	})
}

// SaveRoutes replaces the routing table.
func (s *Service) SaveRoutes(in RoutesInput) error {
	if len(in.Routes) > 24 {
		return input("Twenty-four routes is the limit.")
	}
	n := s.Store.Snapshot().Notify
	known := map[string]bool{"email": true, "inapp": true}
	for _, c := range n.Channels {
		known[c.ID] = true
	}
	out := make([]store.NotifyRoute, 0, len(in.Routes))
	for _, r := range in.Routes {
		if !validPattern(r.Event) {
			return input("An event name is lowercase, like backup.failed, backup.*, or *.")
		}
		sev := strings.TrimSpace(r.MinSeverity)
		if sev == "" {
			sev = "warning"
		}
		switch sev {
		case "info", "warning", "critical":
		default:
			return input("Severity is info, warning, or critical.")
		}
		if len(r.Channels) == 0 || len(r.Channels) > 8 {
			return input("Each route needs between one and eight channels.")
		}
		seen := map[string]bool{}
		var channels []string
		for _, id := range r.Channels {
			id = strings.TrimSpace(id)
			if !known[id] || seen[id] {
				return input("Choose a channel that exists.")
			}
			seen[id] = true
			channels = append(channels, id)
		}
		id := strings.TrimSpace(r.ID)
		if !idRe(id) {
			var err error
			id, err = newNotifyID()
			if err != nil {
				return op("Could not save the routes.")
			}
		}
		out = append(out, store.NotifyRoute{ID: id, Event: strings.TrimSpace(r.Event), MinSeverity: sev, Channels: channels})
	}
	return s.Store.Update(func(st *store.State) error {
		st.Notify.Routes = out
		return nil
	})
}

// SavePolicy stores quiet hours and the rate limits.
func (s *Service) SavePolicy(in PolicyInput) error {
	if in.Quiet.Enabled {
		if _, ok := clock(in.Quiet.Start); !ok {
			return input("Quiet hours start on a 24-hour clock, like 22:00.")
		}
		if _, ok := clock(in.Quiet.End); !ok {
			return input("Quiet hours end on a 24-hour clock, like 07:00.")
		}
	}
	if in.DedupeSeconds != 0 && (in.DedupeSeconds < 30 || in.DedupeSeconds > 86400) {
		return input("De-duplication is between 30 seconds and a day, or leave it at the default.")
	}
	if in.RatePerHour != 0 && (in.RatePerHour < 1 || in.RatePerHour > 1000) {
		return input("The rate limit is between 1 and 1000 messages an hour.")
	}
	return s.Store.Update(func(st *store.State) error {
		st.Notify.Quiet = in.Quiet
		st.Notify.DedupeSeconds = in.DedupeSeconds
		st.Notify.RatePerHour = in.RatePerHour
		st.Notify.InAppSet = true
		st.Notify.InAppEnabled = in.InAppEnabled
		return nil
	})
}

func (s *Service) channelSecret(id string) (string, bool) {
	if s.Secrets == nil {
		return "", false
	}
	v, ok, err := s.Secrets.Get(secretName(id))
	return v, err == nil && ok && v != ""
}

func decorate(v *View, n store.Notify) {
	v.Channels = append([]store.NotifyChannel(nil), n.Channels...)
	if v.Channels == nil {
		v.Channels = []store.NotifyChannel{}
	}
	v.Routes = make([]store.NotifyRoute, len(n.Routes))
	for i, r := range n.Routes {
		r.Channels = append([]string(nil), r.Channels...)
		v.Routes[i] = r
	}
	if v.Routes == nil {
		v.Routes = []store.NotifyRoute{}
	}
	v.Quiet = n.Quiet
	// An untouched quiet window is shown as 22:00–07:00 with critical still
	// delivered. Saving the form is what stores a different choice.
	if !v.Quiet.Enabled && v.Quiet.Start == "" && v.Quiet.End == "" {
		v.Quiet.Start = "22:00"
		v.Quiet.End = "07:00"
		v.Quiet.AllowCritical = true
	}
	v.DedupeSeconds = n.DedupeSeconds
	if v.DedupeSeconds == 0 {
		v.DedupeSeconds = defaultDedupe
	}
	v.RatePerHour = n.RatePerHour
	if v.RatePerHour == 0 {
		v.RatePerHour = defaultRate
	}
	v.InAppEnabled = inAppOn(n)
}

func validateChannel(kind, rawURL, topic, secret string, final bool) error {
	switch kind {
	case "ntfy":
		if err := publicURL(rawURL); err != nil {
			return err
		}
		if topic != "" && !topicRe(topic) {
			return input("An ntfy topic uses letters, numbers, and . _ - :")
		}
		if topic == "" && urlPath(rawURL) == "" {
			return input("Enter an ntfy topic.")
		}
	case "gotify":
		if err := publicURL(rawURL); err != nil {
			return err
		}
	case "webhook":
		if err := publicURL(rawURL); err != nil {
			return err
		}
		u, _ := url.Parse(rawURL)
		if u != nil && u.RawQuery != "" {
			return input("Put a token in the token field, not in the address.")
		}
	}
	if secret != "" && (strings.Contains(secret, "\n") || strings.Contains(secret, "\r")) {
		return input("The token cannot contain a line break.")
	}
	_ = final
	return nil
}

func secretRequired(kind string) bool {
	return kind == "gotify" || kind == "discord" || kind == "slack"
}

func validateWebhook(kind, raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") || u.User != nil {
		return input("That webhook address is not valid.")
	}
	host := strings.ToLower(u.Hostname())
	switch kind {
	case "discord":
		if host != "discord.com" && host != "discordapp.com" && host != "canary.discord.com" && host != "ptb.discord.com" {
			return input("A Discord webhook has to be on discord.com.")
		}
		if !strings.Contains(u.Path, "/api/webhooks/") {
			return input("That does not look like a Discord webhook.")
		}
	case "slack":
		if host != "hooks.slack.com" {
			return input("A Slack webhook has to be on hooks.slack.com.")
		}
	}
	return nil
}

func publicURL(raw string) error {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return input("Enter an http or https address.")
	}
	if u.User != nil {
		return input("Put the token in the token field, not in the address.")
	}
	return nil
}

func urlPath(raw string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return ""
	}
	return strings.Trim(u.Path, "/")
}

func topicRe(s string) bool {
	if s == "" || len(s) > 64 {
		return false
	}
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9', r == '_', r == '.', r == '-', r == ':':
		default:
			return false
		}
	}
	return true
}

func idRe(id string) bool {
	if len(id) != 16 {
		return false
	}
	for _, r := range id {
		switch {
		case r >= '0' && r <= '9', r >= 'a' && r <= 'f':
		default:
			return false
		}
	}
	return true
}

func newNotifyID() (string, error) {
	b := make([]byte, 8)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

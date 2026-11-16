package notify

import (
	"bytes"
	"context"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/events"
	"github.com/Herculade0/feldcore/internal/store"
)

type memBox struct{ m map[string]string }

func (b *memBox) Put(name, value string) error {
	if b.m == nil {
		b.m = map[string]string{}
	}
	if value == "" {
		delete(b.m, name)
		return nil
	}
	b.m[name] = value
	return nil
}
func (b *memBox) Get(name string) (string, bool, error) {
	v, ok := b.m[name]
	return v, ok, nil
}
func (b *memBox) Has(name string) bool { v, ok := b.m[name]; return ok && v != "" }
func (b *memBox) Values() []string {
	var out []string
	for _, v := range b.m {
		if len(v) >= 8 {
			out = append(out, v)
		}
	}
	return out
}

func TestQuietAndMatch(t *testing.T) {
	now := time.Date(2026, 6, 1, 23, 30, 0, 0, time.Local)
	q := store.NotifyQuiet{Enabled: true, Start: "22:00", End: "07:00", AllowCritical: true}
	if !inQuiet(q, now) {
		t.Fatal("23:30 should be quiet")
	}
	if inQuiet(q, time.Date(2026, 6, 1, 12, 0, 0, 0, time.Local)) {
		t.Fatal("noon is not quiet")
	}
	if inQuiet(q, time.Date(2026, 6, 1, 6, 59, 0, 0, time.Local)) == false {
		t.Fatal("06:59 should be quiet")
	}
	if !matchEvent("backup.*", "backup.failed") || matchEvent("backup.*", "health.disk") || !matchEvent("*", "health.cpu") {
		t.Fatal("match")
	}
	if matchEvent("backup.*", "backup") {
		t.Fatal("prefix needs a dot")
	}
}

func TestRouteDedupeQuietAndRedact(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	box := &memBox{}
	var mailed []string
	mail := &Service{Store: st, Secrets: box, SendMail: func(ctx context.Context, m Message) error {
		mailed = append(mailed, m.Subject)
		return nil
	}}
	if err := st.Update(func(s *store.State) error {
		s.Notify.Enabled = true
		s.Notify.OnContainerStop = true
		s.Notify.Mode = ModeSMTP
		s.Notify.Host = "smtp.example.com"
		s.Notify.Port = 587
		s.Notify.From = "a@b.c"
		s.Notify.To = "a@b.c"
		s.Notify.Username = "a@b.c"
		s.Notify.HasPassword = true
		s.Notify.TLS = "starttls"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	_ = box.Put("mail-password", "mail-secret-value")

	var gotBody string
	var gotAuth string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		raw, _ := io.ReadAll(r.Body)
		gotBody = string(raw)
		http.Error(w, "refused "+gotAuth, http.StatusBadGateway)
	}))
	defer srv.Close()

	if err := mail.SaveChannel(ChannelInput{
		Kind: "webhook", Name: "Hook", Enabled: true, URL: srv.URL, Secret: "super-secret-token",
	}); err != nil {
		t.Fatal(err)
	}
	rawState, err := os.ReadFile(dir + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(rawState, []byte("super-secret-token")) {
		t.Fatal("token landed in state.json")
	}
	view := mail.View(context.Background())
	if len(view.Channels) != 1 || !view.Channels[0].HasSecret || view.Channels[0].URL != srv.URL {
		t.Fatalf("view %+v", view.Channels)
	}
	if err := mail.SaveRoutes(RoutesInput{Routes: []store.NotifyRoute{{
		Event: "backup.*", MinSeverity: "warning", Channels: []string{view.Channels[0].ID, "inapp"},
	}}}); err != nil {
		t.Fatal(err)
	}

	var logs bytes.Buffer
	d := NewDispatcher(st, box, mail, OpenInbox(dir+"/inbox.json"), dir+"/dedupe.json", slog.New(slog.NewTextHandler(&logs, nil)))
	d.Client = srv.Client()
	now := time.Date(2026, 6, 1, 23, 30, 0, 0, time.Local)
	d.Now = func() time.Time { return now }
	if err := mail.SavePolicy(PolicyInput{
		InAppEnabled:  true,
		Quiet:         store.NotifyQuiet{Enabled: true, Start: "22:00", End: "07:00", AllowCritical: true},
		DedupeSeconds: 900,
	}); err != nil {
		t.Fatal(err)
	}

	d.Handle(events.Event{Name: "backup.failed", Severity: events.Warning, Title: "Backup failed", Body: "drive refused", Key: "backup.failed"})
	if gotBody != "" {
		t.Fatal("warning during quiet hours must not call the webhook")
	}
	unread, items := d.Inbox.List()
	if unread != 1 || items[0].Name != "backup.failed" {
		t.Fatalf("inbox %+v unread %d", items, unread)
	}

	d.Handle(events.Event{Name: "backup.failed", Severity: events.Critical, Title: "Backup failed", Body: "drive refused", Key: "backup.failed"})
	if !strings.Contains(gotBody, "drive refused") || strings.Contains(gotBody, "super-secret-token") {
		t.Fatalf("body %s", gotBody)
	}
	if !strings.Contains(gotAuth, "super-secret-token") {
		t.Fatal("expected the token on the request, not in the body")
	}
	if strings.Contains(logs.String(), "super-secret-token") || strings.Contains(logs.String(), srv.URL) {
		t.Fatalf("log leaked: %s", logs.String())
	}

	// Same critical event inside the window is dropped.
	before := gotBody
	d.Handle(events.Event{Name: "backup.failed", Severity: events.Critical, Title: "Backup failed", Body: "again", Key: "backup.failed"})
	if gotBody != before {
		t.Fatal("dedupe should hold the second critical")
	}

	d.Handle(events.Event{Name: KindContainer, Severity: events.Warning, Title: "plex stopped", Body: "plex stopped", Key: "container.stopped:plex"})
	if len(mailed) != 0 {
		t.Fatal("mail is quiet too")
	}
	// Leave quiet hours and the legacy mail route should send.
	d.Now = func() time.Time { return time.Date(2026, 6, 1, 12, 0, 0, 0, time.Local) }
	d.Handle(events.Event{Name: KindContainer, Severity: events.Warning, Title: "plex stopped", Body: "plex stopped", Key: "container.stopped:plex"})
	if len(mailed) != 1 {
		t.Fatalf("mailed %v", mailed)
	}
}

func TestDiscordURLIsSecret(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	box := &memBox{}
	mail := &Service{Store: st, Secrets: box}
	hook := "https://discord.com/api/webhooks/123/abcdefghijklmnopqrstuvwxyz"
	if err := mail.SaveChannel(ChannelInput{Kind: "discord", Name: "Chat", Enabled: true, URL: hook}); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(dir + "/state.json")
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(raw, []byte("abcdefghijklmnopqrstuvwxyz")) || bytes.Contains(raw, []byte("webhooks/123")) {
		t.Fatal("discord webhook was stored in state.json")
	}
	if !box.Has(secretName(mail.View(context.Background()).Channels[0].ID)) {
		t.Fatal("secret missing")
	}
}

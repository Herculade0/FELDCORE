package notify

import (
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestUnexpectedStops(t *testing.T) {
	prev := map[string]bool{"plex": true, "vpn": true, "feldcore-vpn-nl": true}
	now := time.Date(2026, 1, 2, 3, 4, 0, 0, time.UTC)
	events, running := UnexpectedStops(prev, []Container{
		{Name: "plex", Running: false},
		{Name: "vpn", Running: false},
		{Name: "feldcore-vpn-nl", Running: false},
		{Name: "web", Running: true},
	}, map[string]time.Time{"vpn": now.Add(time.Minute)}, now)
	if len(events) != 1 || events[0].Kind != KindContainer || !strings.Contains(events[0].Title, "plex") {
		t.Fatalf("events %+v", events)
	}
	if !running["web"] || running["plex"] {
		t.Fatalf("running %v", running)
	}
	ev, ok := UpdateAvailable("0.1.0", "0.2.0", "")
	if !ok || !strings.Contains(ev.Title, "0.2.0") {
		t.Fatalf("update %+v %v", ev, ok)
	}
	if _, ok := UpdateAvailable("0.1.0", "0.2.0", "0.2.0"); ok {
		t.Fatal("should not email the same version twice")
	}
	if _, ok := UpdateAvailable("0.1.0", "0.1.0", ""); ok {
		t.Fatal("current version is not an update")
	}
}

func TestResolveProtonModes(t *testing.T) {
	token, err := Resolve(store.Notify{Mode: ModeToken, Username: "token-user", To: "a@b.c", From: "a@b.c"}, "submission-token-value", "sub", "body")
	if err != nil {
		t.Fatal(err)
	}
	if token.Host != ProtonSMTPHost || token.Port != ProtonSMTPPort || token.TLS != "starttls" || token.Insecure {
		t.Fatalf("token %+v", token)
	}
	bridge, err := Resolve(store.Notify{Mode: ModeBridge, BridgeManaged: true, Username: "user@proton.me", To: "user@proton.me"}, "bridge-smtp-password", "sub", "body")
	if err != nil {
		t.Fatal(err)
	}
	if bridge.Host != BridgeHost || bridge.Port != BridgePort || !bridge.Insecure {
		t.Fatalf("bridge %+v", bridge)
	}
	if _, err := Resolve(store.Notify{Mode: ModeSMTP, Host: "smtp.example.com", Port: 587, To: "a@b.c", From: "a@b.c"}, "", "s", "b"); err == nil {
		t.Fatal("missing password should fail")
	}
}

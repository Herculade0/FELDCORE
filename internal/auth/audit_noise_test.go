package auth

import (
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestAnonymousSignInNoiseDoesNotEvictSecurityEvents(t *testing.T) {
	a := newTestService(t)
	prev := maxAudit
	maxAudit = 4
	t.Cleanup(func() { maxAudit = prev })

	for _, action := range []string{"sign-in", "access denied", "sign-in failed", "GET /system/info"} {
		if err := a.Record("", store.AuditEvent{TokenName: "ada", Action: action, Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 6; i++ {
		if err := a.Record("", store.AuditEvent{TokenName: "Citizen ID", Action: "sign-in noise", Result: "denied"}); err != nil {
			t.Fatal(err)
		}
	}
	got := a.st.Snapshot().Audit
	if len(got) != 4 {
		t.Fatalf("len %d %+v", len(got), got)
	}
	for _, ev := range got {
		if ev.Action == "sign-in noise" {
			t.Fatalf("noise evicted a security event: %+v", got)
		}
	}
	// A later security event still has to fit, and it drops the oldest security event only then.
	if err := a.Record("", store.AuditEvent{TokenName: "ada", Action: "sign-in", Result: "ok", Time: a.now().Add(time.Second)}); err != nil {
		t.Fatal(err)
	}
	got = a.st.Snapshot().Audit
	if len(got) != 4 || got[0].Action != "access denied" {
		t.Fatalf("security cap %+v", got)
	}
}

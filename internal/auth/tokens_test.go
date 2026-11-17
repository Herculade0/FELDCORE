package auth

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestTokenHashIsSHA256AndSecretIsNotStored(t *testing.T) {
	a := newTestService(t)
	secret, info, err := a.CreateToken("Claude Code", []string{"read", "read"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(secret, "fc_") || len(secret) < 40 {
		t.Fatalf("secret %q", secret)
	}
	if info.Prefix == secret || !strings.HasPrefix(secret, info.Prefix) {
		t.Fatalf("prefix %q secret %q", info.Prefix, secret)
	}
	snap := a.st.Snapshot()
	if len(snap.APITokens) != 1 {
		t.Fatalf("stored %d tokens", len(snap.APITokens))
	}
	sum := sha256.Sum256([]byte(secret))
	if snap.APITokens[0].Hash != hex.EncodeToString(sum[:]) {
		t.Fatalf("hash = %s, want sha256 of the secret", snap.APITokens[0].Hash)
	}
	if snap.APITokens[0].Hash != hashToken(secret) {
		t.Fatal("stored hash does not match hashToken")
	}
	raw, err := json.Marshal(snap)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), secret) {
		t.Fatal("plaintext token was persisted")
	}
	if _, ok := a.AuthenticateToken(snap.APITokens[0].Hash); ok {
		t.Fatal("the hash was accepted as a bearer token")
	}
	got, ok := a.AuthenticateToken(secret)
	if !ok || got.Name != "Claude Code" || len(got.Scopes) != 1 || got.Scopes[0] != ScopeRead {
		t.Fatalf("authenticate: %+v ok=%v", got, ok)
	}
}

func TestTokenExpiryRevokeAndScopes(t *testing.T) {
	a := newTestService(t)
	if !Allows([]string{ScopeAdmin}, ScopeVMs) || !Allows([]string{ScopeAdmin}, ScopeRead) || !Allows([]string{ScopeAdmin}, ScopeFilesWrite) || !Allows([]string{ScopeAdmin}, ScopeStacks) || !Allows([]string{ScopeAdmin}, ScopeStorage) || !Allows([]string{ScopeAdmin}, ScopeBackupsWrite) {
		t.Fatal("admin should include the other scopes")
	}
	if Allows([]string{ScopeRead}, ScopeStorage) {
		t.Fatal("read should not include storage")
	}
	if !Allows([]string{ScopeFilesWrite}, ScopeFilesRead) || Allows([]string{ScopeFilesRead}, ScopeFilesWrite) {
		t.Fatal("files:write should include files:read, and not the other way round")
	}
	if !Allows([]string{ScopeBackupsWrite}, ScopeBackupsRead) || Allows([]string{ScopeBackupsRead}, ScopeBackupsWrite) {
		t.Fatal("backups:write should include backups:read, and not the other way round")
	}
	if Allows([]string{ScopeRead}, ScopeContainers) || Allows([]string{ScopeAdmin}, "shell") || Allows([]string{ScopeRead}, ScopeFilesRead) {
		t.Fatal("scope check allowed too much")
	}
	if _, _, err := a.CreateToken("bad", []string{"root"}, 0); err == nil {
		t.Fatal("accepted an unknown scope")
	}
	if _, _, err := a.CreateToken("soon", []string{ScopeRead}, -1); err == nil {
		t.Fatal("accepted a negative expiry")
	}

	secret, _, err := a.CreateToken("soon", []string{ScopeContainers}, 1)
	if err != nil {
		t.Fatal(err)
	}
	a.now = func() time.Time { return time.Now().Add(48 * time.Hour) }
	if _, ok := a.AuthenticateToken(secret); ok {
		t.Fatal("expired token was accepted")
	}

	a.now = time.Now
	other, info, err := a.CreateToken("Grok Bot", []string{ScopeRead, ScopeAdmin}, 7)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.RevokeToken(info.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.AuthenticateToken(other); ok {
		t.Fatal("revoked token was accepted")
	}
	if err := a.RevokeToken("missing"); !errors.Is(err, ErrTokenNotFound) {
		t.Fatalf("missing token: %v", err)
	}
	n, err := a.RevokeAllTokens()
	if err != nil || n != 1 {
		t.Fatalf("revoke all: n=%d err=%v", n, err)
	}
	if _, ok := a.AuthenticateToken(secret); ok {
		t.Fatal("revoke all left a live token")
	}
}

func TestAuditRetention(t *testing.T) {
	a := newTestService(t)
	prev := maxAudit
	maxAudit = 2
	t.Cleanup(func() { maxAudit = prev })

	secret, info, err := a.CreateToken("claude", []string{ScopeRead}, 0)
	if err != nil {
		t.Fatal(err)
	}
	old := a.now().Add(-200 * 24 * time.Hour)
	if err := a.st.Update(func(s *store.State) error {
		s.Audit = []store.AuditEvent{{
			ID: "old", Time: old, TokenID: info.ID, TokenName: info.Name,
			Action: "GET /system/info", Result: "ok",
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for _, action := range []string{"one", "two", "three"} {
		if err := a.Record(info.ID, store.AuditEvent{Action: action, Result: "ok"}); err != nil {
			t.Fatal(err)
		}
	}
	got := a.st.Snapshot().Audit
	if len(got) != 2 || got[0].Action != "two" || got[1].Action != "three" {
		t.Fatalf("audit = %+v", got)
	}
	listed := a.Audit()
	if len(listed) != 2 || listed[0].Action != "three" {
		t.Fatalf("newest first: %+v", listed)
	}
	tok := a.st.Snapshot().APITokens[0]
	if tok.LastUsedAt == nil {
		t.Fatal("last used was not recorded")
	}
	if _, ok := a.AuthenticateToken(secret); !ok {
		t.Fatal("token stopped authenticating")
	}
}

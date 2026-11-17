package auth

import (
	"encoding/base32"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestHOTPVector(t *testing.T) {
	// RFC 6238 SHA1, T = 59, counter 1.
	if got := hotp([]byte("12345678901234567890"), 1); got != "287082" {
		t.Fatalf("hotp = %s", got)
	}
}

func TestEnrolmentAndLogin(t *testing.T) {
	a := newTestService(t)
	box := newMemBox()
	a.UseSecrets(box)
	if err := a.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	secret, uri, err := a.BeginEnrolment("admin")
	if err != nil || secret == "" || !strings.HasPrefix(uri, "otpauth://totp/") {
		t.Fatalf("begin %q %q %v", secret, uri, err)
	}
	raw, _ := json.Marshal(a.st.Snapshot())
	if strings.Contains(string(raw), secret) {
		t.Fatal("secret landed in the state file")
	}
	now := time.Unix(1_700_000_000, 0)
	a.now = func() time.Time { return now }
	code := codeAt(secret, now)
	if err := a.VerifyPending("000000"); err == nil {
		t.Fatal("wrong code accepted")
	}
	recovery, err := a.ConfirmEnrolment(code)
	if err != nil || len(recovery) != 8 {
		t.Fatalf("confirm %v %v", recovery, err)
	}
	if err := a.EnableTOTP(); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Login("ip", "admin", testPass); err != ErrTOTPRequired {
		t.Fatalf("login without code: %v", err)
	}
	if _, _, err := a.LoginWithCode("ip", "admin", "wrong password", code); err != ErrInvalidCredentials {
		t.Fatal("wrong password should not ask for a code")
	}
	tok, _, err := a.LoginWithCode("ip", "admin", testPass, code)
	if err != nil || tok == "" {
		t.Fatal(err)
	}
	// A recovery code works once.
	if _, _, err := a.LoginWithCode("ip", "admin", testPass, recovery[0]); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.LoginWithCode("ip", "admin", testPass, recovery[0]); err == nil {
		t.Fatal("recovery code worked twice")
	}
	if err := a.DisableTOTP(testPass, code); err != nil {
		t.Fatal(err)
	}
	if _, _, err := a.Login("ip", "admin", testPass); err != nil {
		t.Fatal(err)
	}
}

func TestStrongPassword(t *testing.T) {
	if err := ValidateStrong("admin", "Correct Horse Battery 1"); err != nil {
		t.Fatal(err)
	}
	if err := ValidateStrong("admin", "S3cure!Pass1"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"short", "alllowercase1!", "correct horse", "admin-admin-admin"} {
		if err := ValidateStrong("admin", bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
}

func codeAt(secret string, now time.Time) string {
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		return ""
	}
	return hotp(raw, uint64(now.Unix()/30))
}

// memBox is an in-memory SecretBox. The state file is the store, so a
// secret that appears in PasswordHash would be a test failure above.
type memBox struct{ v map[string]string }

func newMemBox() *memBox { return &memBox{v: map[string]string{}} }

func (m *memBox) Put(name, value string) error {
	if value == "" {
		delete(m.v, name)
		return nil
	}
	m.v[name] = value
	return nil
}

func (m *memBox) Get(name string) (string, bool, error) {
	v, ok := m.v[name]
	return v, ok && v != "", nil
}

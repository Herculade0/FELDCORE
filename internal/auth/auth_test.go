package auth

import (
	"errors"
	"testing"
	"time"

	"golang.org/x/crypto/bcrypt"

	"github.com/Herculade0/feldcore/internal/store"
)

func newTestService(t *testing.T) *Service {
	t.Helper()
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(st)
	a.cost = bcrypt.MinCost // keep tests fast
	return a
}

const testPass = "correct horse battery"

func TestSetupOnlyOnce(t *testing.T) {
	a := newTestService(t)
	if !a.SetupRequired() {
		t.Fatal("a fresh install should require setup")
	}
	if err := a.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	if a.SetupRequired() {
		t.Error("setup should no longer be required")
	}
	if err := a.Setup("intruder", testPass); !errors.Is(err, ErrSetupDone) {
		t.Errorf("second setup: got %v, want ErrSetupDone", err)
	}
}

func TestSetupValidation(t *testing.T) {
	a := newTestService(t)
	for name, c := range map[string][2]string{
		"short password": {"admin", "short"},
		"long password":  {"admin", string(make([]byte, 73))},
		"empty username": {"", testPass},
		"bad username":   {"ad min", testPass},
	} {
		var ie *InputError
		if err := a.Setup(c[0], c[1]); !errors.As(err, &ie) {
			t.Errorf("%s: got %v, want a validation error", name, err)
		}
	}
}

func TestLoginAndSessionLifecycle(t *testing.T) {
	a := newTestService(t)
	if err := a.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}

	if _, _, err := a.Login("ip", "admin", "wrong password"); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong password: got %v", err)
	}
	if _, _, err := a.Login("ip", "nobody", testPass); !errors.Is(err, ErrInvalidCredentials) {
		t.Errorf("wrong username: got %v", err)
	}

	token, _, err := a.Login("ip", "admin", testPass)
	if err != nil {
		t.Fatal(err)
	}
	if user, ok := a.Authenticate(token); !ok || user != "admin" {
		t.Errorf("valid token not accepted: %q %v", user, ok)
	}
	if _, ok := a.Authenticate("not-a-token"); ok {
		t.Error("a made-up token must not authenticate")
	}
	if _, ok := a.Authenticate(""); ok {
		t.Error("an empty token must not authenticate")
	}

	// The raw token must never be written to disk.
	for _, se := range a.st.Snapshot().Sessions {
		if se.TokenHash == token {
			t.Error("session token stored unhashed")
		}
	}

	if err := a.Logout(token); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Authenticate(token); ok {
		t.Error("token still valid after logout")
	}
}

func TestSessionExpires(t *testing.T) {
	a := newTestService(t)
	now := time.Now()
	a.now = func() time.Time { return now }
	if err := a.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	token, _, err := a.Login("ip", "admin", testPass)
	if err != nil {
		t.Fatal(err)
	}
	now = now.Add(SessionTTL + time.Minute)
	if _, ok := a.Authenticate(token); ok {
		t.Error("an expired session must not authenticate")
	}
}

func TestLoginRateLimit(t *testing.T) {
	a := newTestService(t)
	now := time.Now()
	a.now = func() time.Time { return now }
	if err := a.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 8; i++ {
		if _, _, err := a.Login("attacker", "admin", "guess"); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: got %v", i, err)
		}
	}
	// Locked out now, even with the right password.
	if _, _, err := a.Login("attacker", "admin", testPass); !errors.Is(err, ErrRateLimited) {
		t.Errorf("after 8 failures: got %v, want ErrRateLimited", err)
	}
	// Other clients are unaffected.
	if _, _, err := a.Login("someone-else", "admin", testPass); err != nil {
		t.Errorf("a different client should still be able to sign in: %v", err)
	}
	// The lockout lifts after the window.
	now = now.Add(6 * time.Minute)
	if _, _, err := a.Login("attacker", "admin", testPass); err != nil {
		t.Errorf("after the window: got %v", err)
	}
}

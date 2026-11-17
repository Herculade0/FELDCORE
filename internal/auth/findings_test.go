package auth

import (
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestChangePasswordKeepsPasskeys(t *testing.T) {
	a := newTestService(t)
	if err := a.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	if err := a.st.Update(func(s *store.State) error {
		s.Passkeys = []store.Passkey{{ID: "pk", UserID: "admin", Name: "laptop"}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	tok := mustLogin(t, a, testPass)
	if err := a.ChangePassword(testPass, "a new password", tok); err != nil {
		t.Fatal(err)
	}
	got := a.st.Snapshot().Passkeys
	if len(got) != 1 || got[0].ID != "pk" {
		t.Fatalf("passkeys %+v", got)
	}
	if _, ok := a.Authenticate(tok); !ok {
		t.Fatal("a self-service password change dropped the current session")
	}
}

func TestDisableTOTPRevokesAPISessions(t *testing.T) {
	a := newTestService(t)
	a.UseSecrets(newMemBox())
	if err := a.Setup("admin", testPass); err != nil {
		t.Fatal(err)
	}
	secret, _, err := a.BeginEnrolment("admin")
	if err != nil {
		t.Fatal(err)
	}
	code := codeAt(secret, time.Now())
	if _, err := a.ConfirmEnrolment(code); err != nil {
		t.Fatal(err)
	}
	if err := a.EnableTOTP(); err != nil {
		t.Fatal(err)
	}
	first, _, err := a.LoginWithCode("a", "admin", testPass, code)
	if err != nil {
		t.Fatal(err)
	}
	second, _, err := a.LoginWithCode("b", "admin", testPass, code)
	if err != nil {
		t.Fatal(err)
	}
	if err := a.DisableTOTP(testPass, code); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Authenticate(first); ok {
		t.Fatal("first session still works")
	}
	if _, ok := a.Authenticate(second); ok {
		t.Fatal("second session still works")
	}
	if a.st.Snapshot().Admin.SessionVersion == 0 {
		t.Fatal("session version was not bumped")
	}
	if _, _, err := a.Login("c", "admin", testPass); err != nil {
		t.Fatal(err)
	}
}

func mustLogin(t *testing.T, a *Service, password string) string {
	t.Helper()
	tok, _, err := a.Login("ip", "admin", password)
	if err != nil {
		t.Fatal(err)
	}
	return tok
}

package setup

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

func TestReplacingTOTPNeedsTheCurrentCode(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	box, err := secrets.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	accounts.UseSecrets(box)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	secret, _, err := accounts.BeginEnrolment("admin")
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := accounts.ConfirmEnrolment(code); err != nil {
		t.Fatal(err)
	}
	if err := accounts.EnableTOTP(); err != nil {
		t.Fatal(err)
	}
	s := New(Deps{Store: st, DataDir: dir, Root: func() bool { return false }})
	s.UseAccounts(accounts)
	s.UseSecrets(box)
	if _, _, err := s.Apply(context.Background(), Request{
		Action: "totp", CurrentPassword: "correct horse", Client: "192.0.2.9",
	}); err == nil {
		t.Fatal("totp replacement accepted a password alone")
	}
	if _, _, err := s.enrolExisting(Request{
		CurrentPassword: "correct horse", TOTPCode: "000000", Client: "192.0.2.9",
	}); err == nil {
		t.Fatal("enrolExisting accepted no current authenticator code")
	}
	fresh, _, err := s.Apply(context.Background(), Request{
		Action: "totp", CurrentPassword: "correct horse", CurrentTOTP: code, Client: "192.0.2.10",
	})
	if err != nil || fresh.TOTPSecret == "" {
		t.Fatalf("replacement with the current code: %+v %v", fresh.TOTPSecret, err)
	}
	pending, err := totp.GenerateCode(fresh.TOTPSecret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := s.enrolExisting(Request{
		CurrentPassword: "correct horse", CurrentTOTP: code, TOTPCode: pending, Client: "192.0.2.10",
	}); err != nil {
		t.Fatal(err)
	}
	var ie *auth.InputError
	if _, _, err := s.beginTOTP(Request{
		CurrentPassword: "correct horse", CurrentTOTP: "000000", Client: "192.0.2.11",
	}); !errors.As(err, &ie) {
		t.Fatalf("wrong current code: %v", err)
	}
}

package citizenid

import (
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/pquerna/otp/totp"

	"github.com/Herculade0/feldcore/internal/auth"
)

func TestTOTPLogin(t *testing.T) {
	g, _ := newGateway(t)
	user, err := g.CreateUser("ada", "Ada", "correct horse", nil)
	if err != nil {
		t.Fatal(err)
	}
	id, ok := g.lookup(user.Username)
	if !ok {
		t.Fatal("user missing")
	}
	if _, err := g.Login(httptest.NewRecorder(), loginReq("192.0.2.60:1"), "ada", "correct horse", ""); err != nil {
		t.Fatal(err)
	}

	setup, err := g.BeginTOTP(id)
	if err != nil {
		t.Fatal(err)
	}
	if setup.Secret == "" || setup.QR == "" || setup.URL == "" {
		t.Fatalf("setup %+v", setup)
	}
	code, err := totp.GenerateCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := g.ConfirmTOTP(id, code); err != nil {
		t.Fatal(err)
	}
	if !g.Profile(id).TOTP {
		t.Fatal("authenticator was not enrolled")
	}

	_, err = g.Login(httptest.NewRecorder(), loginReq("192.0.2.61:1"), "ada", "correct horse", "")
	if !errors.Is(err, ErrSecondFactor) {
		t.Fatalf("missing code: %v", err)
	}
	bad := "000000"
	if bad == code {
		bad = "111111"
	}
	_, err = g.Login(httptest.NewRecorder(), loginReq("192.0.2.62:1"), "ada", "correct horse", bad)
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("wrong code: %v", err)
	}
	_, err = g.Login(httptest.NewRecorder(), loginReq("192.0.2.63:1"), "ada", "correct horse", code)
	if !errors.Is(err, ErrInvalidCode) {
		t.Fatalf("replayed code: %v", err)
	}
	fresh, err := totp.GenerateCode(setup.Secret, time.Now().Add(30*time.Second))
	if err != nil || fresh == code {
		fresh, err = totp.GenerateCode(setup.Secret, time.Now().Add(60*time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	rec := httptest.NewRecorder()
	signed, err := g.Login(rec, loginReq("192.0.2.63:1"), "ada", "correct horse", fresh)
	if err != nil || signed.Username != "ada" {
		t.Fatalf("good code: %v %+v", err, signed)
	}
	if citizenCookie(rec) == nil {
		t.Fatal("totp sign-in did not set a session")
	}

	if err := g.ResetTOTP(user.ID); err != nil {
		t.Fatal(err)
	}
	if g.Profile(id).TOTP {
		t.Fatal("reset left the authenticator on")
	}
	if _, err := g.Login(httptest.NewRecorder(), loginReq("192.0.2.64:1"), "ada", "correct horse", ""); err != nil {
		t.Fatalf("password after reset: %v", err)
	}
}

func TestAdminLegacyPasswordStillWorksWithTOTP(t *testing.T) {
	g, st := newGateway(t)
	accounts := auth.New(st)
	id, ok := g.lookup("admin")
	if !ok {
		t.Fatal("admin missing")
	}
	setup, err := g.BeginTOTP(id)
	if err != nil {
		t.Fatal(err)
	}
	code, err := totp.GenerateCode(setup.Secret, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if err := g.ConfirmTOTP(id, code); err != nil {
		t.Fatal(err)
	}
	accounts.SetSecondFactor(g.AdminFactor)
	if _, _, err := accounts.Login("192.0.2.70:1", "admin", "correct horse"); !errors.Is(err, ErrSecondFactor) {
		t.Fatalf("legacy login without a code: %v", err)
	}
	next, err := totp.GenerateCode(setup.Secret, time.Now().Add(30*time.Second))
	if err != nil || next == code {
		next, err = totp.GenerateCode(setup.Secret, time.Now().Add(60*time.Second))
		if err != nil {
			t.Fatal(err)
		}
	}
	if _, _, err := accounts.LoginCode("192.0.2.70:1", "admin", "correct horse", next); err != nil {
		t.Fatalf("legacy login with the authenticator: %v", err)
	}
	_, err = g.Login(httptest.NewRecorder(), loginReq("192.0.2.71:1"), "admin", "correct horse", "")
	if !errors.Is(err, ErrSecondFactor) {
		t.Fatalf("citizen login without code: %v", err)
	}
}

func loginReq(remote string) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = remote
	req.Host = "localhost"
	return req
}

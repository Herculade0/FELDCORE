package api

import (
	"context"
	"crypto/hmac"
	"crypto/sha1"
	"encoding/base32"
	"encoding/binary"
	"encoding/json"
	"log/slog"
	"net/http"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/setup"
	"github.com/Herculade0/feldcore/internal/store"
	"github.com/Herculade0/feldcore/internal/system"
)

func TestWizardOpenUntilAdminThenRequiresSession(t *testing.T) {
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
	wiz := setup.New(setup.Deps{Store: st, DataDir: dir, Cmd: okCmd{}, Root: func() bool { return true }})
	wiz.UseAccounts(accounts)
	wiz.UseSecrets(box)
	code, err := wiz.EnsureCode()
	if err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{Auth: accounts, System: system.NewCollector(), Setup: wiz, Log: slog.Default()})

	rec := call(srv, http.MethodGet, "/api/v1/setup/wizard", "", "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
	rec = call(srv, http.MethodPost, "/api/v1/setup/wizard", `{"step":"welcome","confirm":true}`, "", nil)
	if rec.Code != http.StatusBadRequest {
		t.Fatalf("missing code %d %s", rec.Code, rec.Body.String())
	}
	body := `{"step":"welcome","confirm":true,"code":"` + code + `"}`
	rec = call(srv, http.MethodPost, "/api/v1/setup/wizard", body, "", nil)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"step":"host"`) {
		t.Fatalf("welcome %d %s", rec.Code, rec.Body.String())
	}

	// Create the account through the wizard and expect a session cookie.
	pass := "Wizard Passphrase 1!"
	admin := `{"step":"host","skip":true,"code":"` + code + `"}`
	rec = call(srv, http.MethodPost, "/api/v1/setup/wizard", admin, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("skip host %d %s", rec.Code, rec.Body.String())
	}
	rec = call(srv, http.MethodPost, "/api/v1/setup/wizard", `{"step":"network","skip":true,"code":"`+code+`"}`, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("skip network %d %s", rec.Code, rec.Body.String())
	}
	rec = call(srv, http.MethodPost, "/api/v1/setup/wizard", `{"step":"admin","confirm":true,"code":"`+code+`","username":"admin","password":"`+pass+`","password2":"`+pass+`","skipTotp":true}`, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("admin %d %s", rec.Code, rec.Body.String())
	}
	var cookie *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if c.Name == sessionCookie {
			cookie = c
		}
	}
	if cookie == nil {
		t.Fatal("admin step did not set a session cookie")
	}
	rec = call(srv, http.MethodGet, "/api/v1/setup/wizard", "", "", nil)
	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("open after admin %d %s", rec.Code, rec.Body.String())
	}
	rec = call(srv, http.MethodGet, "/api/v1/setup/wizard", "", "", cookie)
	if rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), `"step":"storage"`) {
		t.Fatalf("resume %d %s", rec.Code, rec.Body.String())
	}
}

func TestLoginAsksForAuthenticator(t *testing.T) {
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
	const pass = "correct horse"
	if err := accounts.Setup("admin", pass); err != nil {
		t.Fatal(err)
	}
	secret, _, err := accounts.BeginEnrolment("admin")
	if err != nil {
		t.Fatal(err)
	}
	code := totpCode(t, secret, time.Now())
	if _, err := accounts.ConfirmEnrolment(code); err != nil {
		t.Fatal(err)
	}
	if err := accounts.EnableTOTP(); err != nil {
		t.Fatal(err)
	}
	srv := New(Deps{Auth: accounts, System: system.NewCollector(), Log: slog.Default(), Setup: setup.New(setup.Deps{Store: st, DataDir: filepath.Join(dir, "setup")})})

	rec := call(srv, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"correct horse"}`, "", nil)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"totpRequired":true`) {
		t.Fatalf("missing code %d %s", rec.Code, rec.Body.String())
	}
	rec = call(srv, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"correct horse","code":"000000"}`, "", nil)
	if rec.Code != http.StatusUnauthorized || !strings.Contains(rec.Body.String(), `"totpRequired":true`) {
		t.Fatalf("bad code %d %s", rec.Code, rec.Body.String())
	}
	good := totpCode(t, secret, time.Now())
	rec = call(srv, http.MethodPost, "/api/v1/auth/login", `{"username":"admin","password":"correct horse","code":"`+good+`"}`, "", nil)
	if rec.Code != http.StatusOK {
		t.Fatalf("good code %d %s", rec.Code, rec.Body.String())
	}
	var status map[string]any
	rec = call(srv, http.MethodGet, "/api/v1/auth/status", "", "", rec.Result().Cookies()[0])
	if err := json.Unmarshal(rec.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status["totpEnabled"] != true || status["authenticated"] != true {
		t.Fatalf("status %+v", status)
	}
}

type okCmd struct{}

func (okCmd) Run(context.Context, string, ...string) (string, error) { return "", nil }

func totpCode(t *testing.T, secret string, now time.Time) string {
	t.Helper()
	raw, err := base32.StdEncoding.WithPadding(base32.NoPadding).DecodeString(secret)
	if err != nil {
		t.Fatal(err)
	}
	var buf [8]byte
	binary.BigEndian.PutUint64(buf[:], uint64(now.Unix()/30))
	mac := hmac.New(sha1.New, raw)
	mac.Write(buf[:])
	sum := mac.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	bin := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	s := strconv.Itoa(int(bin % 1000000))
	for len(s) < 6 {
		s = "0" + s
	}
	return s
}

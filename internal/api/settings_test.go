package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/apps"
	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/setup"
	"github.com/Herculade0/feldcore/internal/shares"
	"github.com/Herculade0/feldcore/internal/store"
)

func TestSettingsAPI(t *testing.T) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	wiz := setup.New(setup.Deps{Store: st, DataDir: dir, Root: func() bool { return false }})
	catalogue := apps.New(apps.Deps{DataDir: dir, SharesRoot: "/srv/feldcore"})
	shareMgr := shares.New(st, shares.Paths{SharesRoot: dir}, nil)
	srv := New(Deps{Auth: accounts, Setup: wiz, Apps: catalogue, Shares: shareMgr})

	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	login.Host = "127.0.0.1"
	login.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, login)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]

	call := func(method, path, body string) *httptest.ResponseRecorder {
		t.Helper()
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			r.Header.Set("Content-Type", "application/json")
		}
		r.Host = "127.0.0.1"
		r.AddCookie(cookie)
		w := httptest.NewRecorder()
		srv.Router().ServeHTTP(w, r)
		return w
	}

	got := call(http.MethodGet, "/api/v1/settings", "")
	if got.Code != http.StatusOK || !strings.Contains(got.Body.String(), `"listenPort":8787`) {
		t.Fatalf("settings: %d %s", got.Code, got.Body.String())
	}
	if !strings.Contains(got.Body.String(), "22/tcp") {
		t.Fatalf("firewall rules forgot ssh: %s", got.Body.String())
	}

	if code := call(http.MethodPut, "/api/v1/settings/system", `{"hostname":"-bad","timezone":"UTC","ntpServers":""}`).Code; code != http.StatusBadRequest {
		t.Fatalf("bad hostname: %d", code)
	}
	if code := call(http.MethodPut, "/api/v1/settings/password", `{"current":"nope","next":"a new password"}`).Code; code != http.StatusUnauthorized {
		t.Fatalf("wrong password: %d", code)
	}
	if code := call(http.MethodPut, "/api/v1/settings/password", `{"current":"correct horse","next":"a new password"}`).Code; code != http.StatusOK {
		t.Fatalf("password change: %d", code)
	}
	if code := call(http.MethodPut, "/api/v1/settings/session", `{"hours":0}`).Code; code != http.StatusBadRequest {
		t.Fatalf("zero timeout: %d", code)
	}
	sess := call(http.MethodPut, "/api/v1/settings/session", `{"hours":24}`)
	if sess.Code != http.StatusOK || !strings.Contains(sess.Body.String(), `"sessionHours":24`) {
		t.Fatalf("session: %d %s", sess.Code, sess.Body.String())
	}
	if code := call(http.MethodPut, "/api/v1/settings/feed", `{"feedURL":"ftp://nope"}`).Code; code != http.StatusBadRequest {
		t.Fatalf("bad feed: %d", code)
	}
	feed := call(http.MethodPut, "/api/v1/settings/feed", `{"feedURL":"https://example.com/feed.json"}`)
	if feed.Code != http.StatusOK {
		t.Fatalf("feed: %d %s", feed.Code, feed.Body.String())
	}
	if catalogue.FeedURL() != "https://example.com/feed.json" || st.Snapshot().Prefs.FeedURL != "https://example.com/feed.json" {
		t.Fatalf("feed not stored: %s", catalogue.FeedURL())
	}
	def := call(http.MethodPut, "/api/v1/settings/defaults", `{"removeImage":true}`)
	if def.Code != http.StatusOK {
		t.Fatalf("defaults: %d", def.Code)
	}
	again := call(http.MethodGet, "/api/v1/settings", "")
	var view struct {
		Defaults struct {
			RemoveImage bool `json:"removeImage"`
		} `json:"defaults"`
		Security struct {
			SessionHours int `json:"sessionHours"`
		} `json:"security"`
	}
	if err := json.Unmarshal(again.Body.Bytes(), &view); err != nil {
		t.Fatal(err)
	}
	if !view.Defaults.RemoveImage || view.Security.SessionHours != 24 {
		t.Fatalf("saved settings %+v", view)
	}
	if code := call(http.MethodPost, "/api/v1/settings/shares", "").Code; code != http.StatusOK {
		t.Fatalf("recreate shares: %d", code)
	}
}

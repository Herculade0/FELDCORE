package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/shares"
	"github.com/Herculade0/feldcore/internal/store"
)

func TestFileBrowserAPI(t *testing.T) {
	dir := t.TempDir()
	root := filepath.Join(dir, "shares")
	share := filepath.Join(root, "media")
	outside := filepath.Join(dir, "outside")
	if err := os.MkdirAll(share, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(outside, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(outside, "secret.txt"), []byte("TOPSECRET"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(share, "note.txt"), []byte("abcdefghij"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(filepath.Join(outside, "secret.txt"), filepath.Join(share, "leak")); err != nil {
		t.Fatal(err)
	}

	st, err := store.Open(filepath.Join(dir, "data"))
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Shares = append(s.Shares, store.Share{Name: "media"})
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	accounts := auth.New(st)
	if err := accounts.Setup("admin", "correct horse"); err != nil {
		t.Fatal(err)
	}
	shareMgr := shares.New(st, shares.Paths{SharesRoot: root}, nil)
	srv := New(Deps{Auth: accounts, Shares: shareMgr})

	login := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login", strings.NewReader(`{"username":"admin","password":"correct horse"}`))
	login.Header.Set("Content-Type", "application/json")
	rec := httptest.NewRecorder()
	srv.Router().ServeHTTP(rec, login)
	if rec.Code != http.StatusOK {
		t.Fatalf("login %d %s", rec.Code, rec.Body.String())
	}
	cookie := rec.Result().Cookies()[0]

	readTok, _, err := accounts.CreateToken("reader", []string{auth.ScopeRead}, 0)
	if err != nil {
		t.Fatal(err)
	}
	fileRead, _, err := accounts.CreateToken("files", []string{auth.ScopeFilesRead}, 0)
	if err != nil {
		t.Fatal(err)
	}
	fileWrite, _, err := accounts.CreateToken("writer", []string{auth.ScopeFilesWrite}, 0)
	if err != nil {
		t.Fatal(err)
	}

	call := func(method, path, body, token string, cookieOK bool, headers map[string]string) *httptest.ResponseRecorder {
		t.Helper()
		var r *http.Request
		if body == "" {
			r = httptest.NewRequest(method, path, nil)
		} else {
			r = httptest.NewRequest(method, path, strings.NewReader(body))
			if headers["Content-Type"] == "" {
				r.Header.Set("Content-Type", "application/json")
			}
		}
		for k, v := range headers {
			r.Header.Set(k, v)
		}
		if token != "" {
			r.Header.Set("Authorization", "Bearer "+token)
		}
		if cookieOK {
			r.AddCookie(cookie)
		}
		w := httptest.NewRecorder()
		srv.Router().ServeHTTP(w, r)
		return w
	}

	if got := call(http.MethodGet, "/api/v1/shares/media/files", "", "", false, nil); got.Code != http.StatusUnauthorized {
		t.Fatalf("anonymous list: %d %s", got.Code, got.Body.String())
	}
	if got := call(http.MethodGet, "/api/v1/shares/media/files", "", readTok, false, nil); got.Code != http.StatusForbidden {
		t.Fatalf("read token list: %d %s", got.Code, got.Body.String())
	}
	listed := call(http.MethodGet, "/api/v1/shares/media/files", "", fileRead, false, nil)
	if listed.Code != http.StatusOK || !strings.Contains(listed.Body.String(), "note.txt") {
		t.Fatalf("files:read list: %d %s", listed.Code, listed.Body.String())
	}
	if got := call(http.MethodPost, "/api/v1/shares/media/files/mkdir", `{"path":"albums"}`, fileRead, false, nil); got.Code != http.StatusForbidden {
		t.Fatalf("files:read mkdir: %d", got.Code)
	}
	made := call(http.MethodPost, "/api/v1/shares/media/files/mkdir", `{"path":"albums"}`, fileWrite, false, nil)
	if made.Code != http.StatusOK {
		t.Fatalf("files:write mkdir: %d %s", made.Code, made.Body.String())
	}
	if _, err := os.Stat(filepath.Join(share, "albums")); err != nil {
		t.Fatal(err)
	}

	audit := call(http.MethodGet, "/api/v1/audit", "", "", true, nil)
	if audit.Code != http.StatusOK || !strings.Contains(audit.Body.String(), `"action":"file mkdir"`) {
		t.Fatalf("audit: %d %s", audit.Code, audit.Body.String())
	}

	ranged := call(http.MethodGet, "/api/v1/shares/media/files/content?path=note.txt", "", "", true, map[string]string{"Range": "bytes=0-3"})
	if ranged.Code != http.StatusPartialContent || ranged.Body.String() != "abcd" {
		t.Fatalf("range: %d %q", ranged.Code, ranged.Body.String())
	}

	leaked := call(http.MethodGet, "/api/v1/shares/media/files/content?path=leak", "", "", true, nil)
	if leaked.Code == http.StatusOK || strings.Contains(leaked.Body.String(), "TOPSECRET") {
		t.Fatalf("symlink escaped: %d %s", leaked.Code, leaked.Body.String())
	}

	escaped := call(http.MethodPost, "/api/v1/shares/media/files/mkdir", `{"path":"../outside/planted"}`, "", true, nil)
	if escaped.Code != http.StatusBadRequest {
		t.Fatalf("traversal mkdir: %d %s", escaped.Code, escaped.Body.String())
	}
	if _, err := os.Stat(filepath.Join(outside, "planted")); !os.IsNotExist(err) {
		t.Fatalf("traversal created a directory outside the share: %v", err)
	}

	zipped := call(http.MethodPost, "/api/v1/shares/media/files/zip", `{"paths":["note.txt"]}`, fileRead, false, nil)
	if zipped.Code != http.StatusOK || !strings.Contains(zipped.Header().Get("Content-Type"), "application/zip") || !strings.Contains(zipped.Body.String(), "note.txt") {
		t.Fatalf("zip: %d %s %q", zipped.Code, zipped.Header().Get("Content-Type"), zipped.Body.String())
	}

	for _, raw := range []string{"http://127.0.0.1/secret", "http://169.254.169.254/latest", "file:///etc/passwd", "http://metadata.google.internal/"} {
		got := call(http.MethodPost, "/api/v1/shares/media/fetch", `{"url":"`+raw+`","filename":"stolen.bin"}`, fileWrite, false, nil)
		if got.Code != http.StatusBadRequest {
			t.Fatalf("fetch %s: %d %s", raw, got.Code, got.Body.String())
		}
	}
	if _, err := os.Stat(filepath.Join(share, "stolen.bin")); !os.IsNotExist(err) {
		t.Fatalf("blocked fetch wrote a file: %v", err)
	}
	again := call(http.MethodGet, "/api/v1/audit", "", "", true, nil)
	if !strings.Contains(again.Body.String(), `"action":"file fetch"`) || !strings.Contains(again.Body.String(), `"result":"error"`) {
		t.Fatalf("fetch audit: %s", again.Body.String())
	}

	if got := call(http.MethodPut, "/api/v1/settings/files", `{"allowLANURLs":true}`, fileWrite, false, nil); got.Code != http.StatusForbidden {
		t.Fatalf("writer settings: %d %s", got.Code, got.Body.String())
	}
	set := call(http.MethodPut, "/api/v1/settings/files", `{"allowLANURLs":true}`, "", true, nil)
	if set.Code != http.StatusOK || !strings.Contains(set.Body.String(), `"allowLANURLs":true`) {
		t.Fatalf("lan setting: %d %s", set.Code, set.Body.String())
	}
	if !accounts.AllowLANURLs() {
		t.Fatal("allowLANURLs was not stored")
	}

	var listing struct {
		Entries []struct {
			Name string `json:"name"`
		} `json:"entries"`
	}
	if err := json.Unmarshal(listed.Body.Bytes(), &listing); err != nil {
		t.Fatal(err)
	}
}

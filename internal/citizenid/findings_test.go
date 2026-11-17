package citizenid

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/store"
)

func TestGroupListsIntersect(t *testing.T) {
	g, st := newGateway(t)
	family, err := g.CreateGroup("Family", "")
	if err != nil {
		t.Fatal(err)
	}
	kids, err := g.CreateGroup("Kids", "")
	if err != nil {
		t.Fatal(err)
	}
	ada, err := g.CreateUser("ada", "Ada", "correct horse", []string{family.ID})
	if err != nil {
		t.Fatal(err)
	}
	if err := g.SetAppAccess("files", LevelGroups, []string{family.ID, kids.ID}); err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Web.Routes = []store.WebRoute{{
			Container: "files", Hostname: "files.home.arpa", Port: 80, Enabled: true,
		}}
		s.Publish.Apps = []store.PublishedApp{{
			Container: "files", Hostname: "files.home.arpa", Access: "groups",
			Groups: []string{"Kids"}, Enabled: true,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	level, groups := g.Effective("files", "files.home.arpa")
	if level != LevelGroups || len(groups) != 1 || groups[0] != kids.ID {
		t.Fatalf("effective %s %v", level, groups)
	}
	app, ok := g.AppForHost("files.home.arpa")
	if !ok || app.Level != LevelGroups || len(app.GroupIDs) != 1 || app.GroupIDs[0] != kids.ID {
		t.Fatalf("host %+v %v", app, ok)
	}
	adaID := Identity{ID: ada.ID, Username: "ada", GroupIDs: []string{family.ID}}
	if allowed(adaID, app) {
		t.Fatal("family-only member passed an intersection with kids")
	}
	dir := g.Directory()
	if len(dir.Apps) != 1 || dir.Apps[0].Level != LevelGroups || len(dir.Apps[0].GroupIDs) != 1 {
		t.Fatalf("directory %+v", dir.Apps)
	}
	if len(g.Launcher(adaID)) != 0 {
		t.Fatal("launcher showed the app")
	}

	if err := st.Update(func(s *store.State) error {
		for i := range s.Accounts {
			if s.Accounts[i].ID == ada.ID {
				s.Accounts[i].GroupIDs = []string{family.ID, kids.ID}
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	both := Identity{ID: ada.ID, Username: "ada", GroupIDs: []string{family.ID, kids.ID}}
	app, _ = g.AppForHost("files.home.arpa")
	if !allowed(both, app) {
		t.Fatal("member of both lists was refused")
	}

	if err := g.SetAppAccess("files", LevelUsers, nil); err != nil {
		t.Fatal(err)
	}
	if level, _ := g.Effective("files", "files.home.arpa"); level != LevelGroups {
		t.Fatalf("publish groups should outrank signed-in users, got %s", level)
	}
	if err := st.Update(func(s *store.State) error {
		s.Publish.Apps[0].Access = "admins"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if level, _ := g.Effective("files", "files.home.arpa"); level != LevelAdmin {
		t.Fatalf("admin publish should win, got %s", level)
	}

	if err := g.DeleteGroup(kids.ID); err != nil {
		t.Fatal(err)
	}
	left := st.Snapshot().Publish.Apps[0].Groups
	for _, name := range left {
		if name == kids.ID || name == "Kids" || name == "kids" {
			t.Fatalf("deleted group still listed: %v", left)
		}
	}
}

func TestTunnelProtoIsTrusted(t *testing.T) {
	g, st := newGateway(t)
	if err := st.Update(func(s *store.State) error {
		s.Web.Routes = []store.WebRoute{{
			Container: "files", Hostname: "files.home.arpa", Port: 80, Enabled: true,
		}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := g.SetAppAccess("files", LevelUsers, nil); err != nil {
		t.Fatal(err)
	}
	tunnel := net.ParseIP("172.18.0.5")
	g.trust = func(_ context.Context, ip net.IP) bool {
		return ip != nil && ip.Equal(tunnel)
	}
	app, ok := g.AppForHost("files.home.arpa")
	if !ok {
		t.Fatal("no app")
	}
	req := httptest.NewRequest(http.MethodGet, "http://files.home.arpa/", nil)
	req.Host = "host.docker.internal"
	req.RemoteAddr = "172.18.0.5:9"
	req.Header.Set("X-Forwarded-Host", "files.home.arpa")
	req.Header.Set("X-Forwarded-Proto", "https")
	req.Header.Set("CF-Connecting-IP", "203.0.113.9")
	d, err := g.Authorize(req, app)
	if err != nil || d.Allow || d.Reason != "unauthenticated" {
		t.Fatalf("tunnel %+v %v", d, err)
	}
	spoof := req.Clone(req.Context())
	spoof.RemoteAddr = "203.0.113.8:9"
	d, err = g.Authorize(spoof, app)
	if err != nil || d.Allow || d.Reason != "forbidden" {
		t.Fatalf("spoofed proto %+v %v", d, err)
	}
}

func TestHostCookiePreferred(t *testing.T) {
	g, _ := newGateway(t)
	if _, err := g.CreateUser("ada", "Ada", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	cookie := signIn(t, g, "ada", "correct horse", "", "192.0.2.10:9")
	good := authed(nil, "files.home.arpa")
	good.AddCookie(&http.Cookie{Name: "__Host-" + cookieName, Value: cookie.Value})
	good.AddCookie(&http.Cookie{Name: cookieName, Value: "planted"})
	id, ok := g.Identity(good)
	if !ok || id.Username != "ada" {
		t.Fatalf("host cookie ignored: %+v %v", id, ok)
	}
	planted := authed(nil, "files.home.arpa")
	planted.AddCookie(&http.Cookie{Name: "__Host-" + cookieName, Value: "not-a-session"})
	planted.AddCookie(cookie)
	if _, ok := g.Identity(planted); ok {
		t.Fatal("plain cookie overrode a present __Host- cookie")
	}
}

func TestSetPasswordDropsPasskeys(t *testing.T) {
	g, st := newGateway(t)
	user, err := g.CreateUser("ada", "Ada", "correct horse", nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := st.Update(func(s *store.State) error {
		s.Passkeys = []store.Passkey{
			{ID: "ada", UserID: user.ID},
			{ID: "other", UserID: "someone-else"},
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	before := 0
	for _, a := range st.Snapshot().Accounts {
		if a.ID == user.ID {
			before = a.SessionVersion
		}
	}
	if err := g.SetPassword(user.ID, "a different password"); err != nil {
		t.Fatal(err)
	}
	snap := st.Snapshot()
	if len(snap.Passkeys) != 1 || snap.Passkeys[0].UserID != "someone-else" {
		t.Fatalf("passkeys %+v", snap.Passkeys)
	}
	for _, a := range snap.Accounts {
		if a.ID == user.ID && a.SessionVersion <= before {
			t.Fatal("session version was not bumped")
		}
	}
}

func TestResetTOTPRevokesSessions(t *testing.T) {
	g, st := newGateway(t)
	accounts := auth.New(st)
	tok, _, err := accounts.Login("ip", "admin", "correct horse")
	if err != nil || tok == "" {
		t.Fatal(err)
	}
	other, _, err := accounts.Login("other", "admin", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	version := st.Snapshot().Admin.SessionVersion
	if err := g.ResetTOTP("admin"); err != nil {
		t.Fatal(err)
	}
	if _, ok := accounts.Authenticate(tok); ok {
		t.Fatal("reset left the api session working")
	}
	if _, ok := accounts.Authenticate(other); ok {
		t.Fatal("reset left the other api session working")
	}
	if st.Snapshot().Admin.SessionVersion <= version {
		t.Fatal("session version was not bumped")
	}

	user, err := g.CreateUser("ada", "Ada", "correct horse", nil)
	if err != nil {
		t.Fatal(err)
	}
	fresh, _, err := accounts.Login("again", "admin", "correct horse")
	if err != nil {
		t.Fatal(err)
	}
	if err := g.ResetTOTP(user.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := accounts.Authenticate(fresh); !ok {
		t.Fatal("resetting another person signed the administrator out")
	}
	for _, a := range st.Snapshot().Accounts {
		if a.ID == user.ID && a.SessionVersion == 0 {
			t.Fatal("account session version was not bumped")
		}
	}
}

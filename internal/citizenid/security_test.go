package citizenid

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

func TestSafeNextRejectsTricks(t *testing.T) {
	g, st := newGateway(t)
	if err := st.Update(func(s *store.State) error {
		s.Web.Routes = []store.WebRoute{{Container: "files", Hostname: "files.home.arpa", Port: 80, Enabled: true}}
		s.Publish.Apps = []store.PublishedApp{{Container: "plex", Hostname: "plex.example.com", Port: 32400, Enabled: true}}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "feldcore.home.arpa"
	for _, next := range []string{
		"/%09/evil.example",
		"/%0a/evil.example",
		"/\\evil.example",
		"//evil.example",
		"https://evil.example/phish",
		"https://not-published.home.arpa/library",
		"https://files.home.arpa.evil.example/",
		"http://files.home.arpa%09.evil.example/",
	} {
		if got := g.safeNext(req, next); got != "/" {
			t.Fatalf("next %q kept %q", next, got)
		}
	}
	if got := g.safeNext(req, "/apps"); got != "/apps" {
		t.Fatalf("relative path %q", got)
	}
	if got := g.safeNext(req, "https://files.home.arpa/library"); got != "https://files.home.arpa/library" {
		t.Fatalf("published host %q", got)
	}
	if got := g.safeNext(req, "https://plex.example.com/web"); got != "https://plex.example.com/web" {
		t.Fatalf("publish app host %q", got)
	}
}

func TestGatedFailsClosed(t *testing.T) {
	if Gated("") || Gated(LevelPublic) {
		t.Fatal("public was gated")
	}
	if !Gated(LevelUsers) || !Gated(LevelGroups) || !Gated(LevelAdmin) || !Gated("nope") {
		t.Fatal("a non-public level was left open")
	}
}

func TestCleartextFromTheInternetIsRefused(t *testing.T) {
	g, _ := newGateway(t)
	app := AppAccess{ID: "wiki", Host: "wiki.home.arpa", Level: LevelUsers}
	public := httptest.NewRequest(http.MethodGet, "http://wiki.home.arpa/", nil)
	public.Host = "wiki.home.arpa"
	public.RemoteAddr = "203.0.113.9:9"
	d, err := g.Authorize(public, app)
	if err != nil || d.Allow || d.Reason != "forbidden" || d.Location != "" {
		t.Fatalf("public cleartext %+v %v", d, err)
	}
	local := httptest.NewRequest(http.MethodGet, "http://wiki.home.arpa/", nil)
	local.Host = "wiki.home.arpa"
	local.RemoteAddr = "127.0.0.1:9"
	d, err = g.Authorize(local, app)
	if err != nil || d.Allow || d.Reason != "unauthenticated" || !strings.HasPrefix(d.Location, "/citizen/signin?next=") {
		t.Fatalf("local cleartext %+v %v", d, err)
	}
}

func TestSessionBindsToUserID(t *testing.T) {
	g, _ := newGateway(t)
	user, err := g.CreateUser("ada", "Ada", "correct horse", nil)
	if err != nil {
		t.Fatal(err)
	}
	cookie := signIn(t, g, "ada", "correct horse", "", "192.0.2.20:9")
	if _, ok := g.Identity(authed(cookie, "feldcore.home.arpa")); !ok {
		t.Fatal("fresh session was not accepted")
	}
	if err := g.DeleteUser(user.ID); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Identity(authed(cookie, "feldcore.home.arpa")); ok {
		t.Fatal("deleted account stayed signed in")
	}
	if _, err := g.CreateUser("ada", "Ada", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	if _, ok := g.Identity(authed(cookie, "feldcore.home.arpa")); ok {
		t.Fatal("recreated username revived the old session")
	}
}

func TestSuccessDoesNotResetTheIPBucket(t *testing.T) {
	g, _ := newGateway(t)
	if _, err := g.CreateUser("ada", "Ada", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CreateUser("bob", "Bob", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "/login", nil)
	req.RemoteAddr = "203.0.113.8:9"
	for i := 0; i < 7; i++ {
		if _, err := g.Login(httptest.NewRecorder(), req, "ada", "wrong-password", ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	if _, err := g.Login(httptest.NewRecorder(), req, "ada", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	if _, err := g.Login(httptest.NewRecorder(), req, "bob", "wrong-password", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatalf("ip bucket was cleared: %v", err)
	}
	if _, err := g.Login(httptest.NewRecorder(), req, "bob", "correct horse", ""); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("ip lockout: %v", err)
	}
}

func TestLockoutUsesRealClientIP(t *testing.T) {
	g, _ := newGateway(t)
	if _, err := g.CreateUser("ada", "Ada", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := g.CreateUser("bob", "Bob", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	proxy := net.ParseIP("10.1.1.8")
	g.trust = func(_ context.Context, ip net.IP) bool {
		return ip != nil && ip.Equal(proxy)
	}
	spoof := httptest.NewRequest(http.MethodGet, "/", nil)
	spoof.RemoteAddr = "198.51.100.20:9"
	spoof.Header.Set("X-Forwarded-For", "203.0.113.50")
	spoof.Header.Set("CF-Connecting-IP", "203.0.113.50")
	if got := g.clientIP(spoof); got != "198.51.100.20" {
		t.Fatalf("untrusted peer client %s", got)
	}
	loop := httptest.NewRequest(http.MethodGet, "/", nil)
	loop.RemoteAddr = "127.0.0.1:9"
	loop.Header.Set("X-Forwarded-For", "203.0.113.77")
	if got := g.clientIP(loop); got != "203.0.113.77" {
		t.Fatalf("loopback client %s", got)
	}
	bad := httptest.NewRequest(http.MethodPost, "/login", nil)
	bad.RemoteAddr = "10.1.1.8:9"
	bad.Header.Set("X-Forwarded-For", "203.0.113.50")
	for i := 0; i < 8; i++ {
		if _, err := g.Login(httptest.NewRecorder(), bad, "ada", "wrong-password", ""); !errors.Is(err, ErrInvalidCredentials) {
			t.Fatalf("attempt %d: %v", i, err)
		}
	}
	direct := httptest.NewRequest(http.MethodPost, "/login", nil)
	direct.RemoteAddr = "203.0.113.50:9"
	if _, err := g.Login(httptest.NewRecorder(), direct, "bob", "correct horse", ""); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("real client was not locked: %v", err)
	}
	other := httptest.NewRequest(http.MethodPost, "/login", nil)
	other.RemoteAddr = "198.51.100.20:9"
	other.Header.Set("X-Forwarded-For", "203.0.113.50")
	if _, err := g.Login(httptest.NewRecorder(), other, "bob", "correct horse", ""); err != nil {
		t.Fatalf("spoofed address locked a different peer: %v", err)
	}
}

func TestLockoutCheckAndCountAreAtomic(t *testing.T) {
	lim := newLimiter(8, time.Minute)
	var passed atomic.Int32
	var wg sync.WaitGroup
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			done, ok := lim.begin(time.Now(), "ip:203.0.113.1")
			if !ok {
				return
			}
			passed.Add(1)
			done(true)
		}()
	}
	wg.Wait()
	if got := passed.Load(); got > 8 {
		t.Fatalf("lockout let %d attempts through", got)
	}
}

func TestCeremonyCapDoesNotWipe(t *testing.T) {
	g, _ := newGateway(t)
	keys := make([]string, 0, 257)
	for i := 0; i < 257; i++ {
		w := httptest.NewRecorder()
		r := httptest.NewRequest(http.MethodPost, "/", nil)
		r.Host = "localhost"
		err := g.stash(w, r, ceremony{Kind: "login", Expires: time.Now().Add(time.Duration(i+1) * time.Minute)})
		if err != nil {
			t.Fatal(err)
		}
		c := w.Result().Cookies()
		if len(c) == 0 {
			t.Fatal("no ceremony cookie")
		}
		keys = append(keys, hashKey(c[0].Value))
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(g.ceremonies) != 256 {
		t.Fatalf("ceremonies %d", len(g.ceremonies))
	}
	if _, ok := g.ceremonies[keys[0]]; ok {
		t.Fatal("oldest ceremony was kept past the cap")
	}
	if _, ok := g.ceremonies[keys[1]]; !ok {
		t.Fatal("the cap wiped ceremonies that were still valid")
	}
	if _, ok := g.ceremonies[keys[256]]; !ok {
		t.Fatal("the ceremony just stored was dropped")
	}
}

func TestSessionEvictsOldestExtraForThatUser(t *testing.T) {
	rows := []store.CitizenSession{
		{TokenHash: "a0", UserID: "ada", CreatedAt: time.Unix(1, 0)},
		{TokenHash: "a1", UserID: "ada", CreatedAt: time.Unix(2, 0)},
		{TokenHash: "a2", UserID: "ada", CreatedAt: time.Unix(3, 0)},
		{TokenHash: "current", UserID: "ada", CreatedAt: time.Unix(9, 0)},
		{TokenHash: "bob", UserID: "bob", CreatedAt: time.Unix(1, 0)},
	}
	out := evictSessions(rows, "current", 3)
	if len(out) != 3 {
		t.Fatalf("len %d", len(out))
	}
	have := map[string]bool{}
	for _, se := range out {
		have[se.TokenHash] = true
	}
	if !have["current"] {
		t.Fatal("the session just written was evicted")
	}
	if have["a0"] {
		t.Fatal("oldest extra session was kept")
	}
	if !have["bob"] {
		t.Fatal("a person with one session was evicted before extras")
	}
}

func TestPasskeyLoginRequiresUserVerification(t *testing.T) {
	g, _ := newGateway(t)
	w := httptest.NewRecorder()
	r := httptest.NewRequest(http.MethodPost, "/citizen/webauthn/login/begin", nil)
	r.Host = "localhost"
	opts, err := g.BeginPasskeyLogin(w, r)
	if err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"userVerification":"required"`) {
		t.Fatalf("passkey login did not require verification: %s", raw)
	}
}

func TestPasswordChecksTakeBothShapes(t *testing.T) {
	g, _ := newGateway(t)
	if _, err := g.Login(httptest.NewRecorder(), loginReq("192.0.2.30:1"), "admin", "correct horse", ""); err != nil {
		t.Fatal(err)
	}
	if g.dummy == "" {
		t.Fatal("administrator sign-in skipped the argon2 check")
	}
	g2, _ := newGateway(t)
	if _, err := g2.Login(httptest.NewRecorder(), loginReq("192.0.2.31:1"), "nobody", "whatever-password", ""); !errors.Is(err, ErrInvalidCredentials) {
		t.Fatal(err)
	}
	if g2.dummy == "" || len(g2.bcryptDummy) == 0 {
		t.Fatal("unknown user skipped one of the password checks")
	}
}

func TestArgonSemaphoreCapsConcurrency(t *testing.T) {
	for i := 0; i < cap(argonSlots); i++ {
		argonSlots <- struct{}{}
	}
	done := make(chan struct{})
	go func() {
		_, _ = hashPassword(fastArgon(), "correct horse")
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("argon2 ran with no free slot")
	case <-time.After(40 * time.Millisecond):
	}
	<-argonSlots
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("argon2 did not resume when a slot freed")
	}
	for len(argonSlots) > 0 {
		<-argonSlots
	}
}

func TestHostCookieOnHTTPS(t *testing.T) {
	g, _ := newGateway(t)
	if _, err := g.CreateUser("ada", "Ada", "correct horse", nil); err != nil {
		t.Fatal(err)
	}
	req := httptest.NewRequest(http.MethodPost, "https://feldcore.home.arpa/login", nil)
	req.Host = "feldcore.home.arpa"
	req.RemoteAddr = "127.0.0.1:9"
	req.Header.Set("X-Forwarded-Proto", "https")
	rec := httptest.NewRecorder()
	id, err := g.Login(rec, req, "ada", "correct horse", "")
	if err != nil {
		t.Fatal(err)
	}
	var citizen *http.Cookie
	for _, c := range rec.Result().Cookies() {
		if strings.HasPrefix(c.Name, "__Host-") && strings.Contains(c.Name, "citizen") {
			citizen = c
		}
	}
	// A parent domain is not configured, so the HTTPS cookie can use __Host-.
	if citizen == nil || !citizen.Secure || citizen.Domain != "" {
		t.Fatalf("cookie %+v", citizen)
	}
	w := httptest.NewRecorder()
	if err := g.StartDesk(w, req, id); err != nil {
		t.Fatal(err)
	}
	var desk *http.Cookie
	for _, c := range w.Result().Cookies() {
		if c.Name == "__Host-"+deskCookie {
			desk = c
		}
	}
	if desk == nil || !desk.Secure || desk.Domain != "" || desk.Path != "/" {
		t.Fatalf("desk %+v", desk)
	}
}

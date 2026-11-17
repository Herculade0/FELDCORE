// Package citizenid is FELDCORE's identity framework: users, groups, and the
// sign-in gateway in front of published apps.
//
// The built-in administrator account and its original sign-in are left
// alone. Citizen ID adds everyone else, plus a parent-domain session cookie
// so one sign-in covers every app the proxy publishes. TOTP secrets live in
// the secrets box. Sessions and the directory live in the state file.
//
// The proxy calls Authorize, or GET /citizen/check (forward_auth). Entra ID
// and Cloudflare Access are not built; they would be another IdentityProvider.
package citizenid

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/alexedwards/scs/v2"

	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
)

const (
	cookieName    = "feldcore_citizen"
	deskCookie    = "feldcore_desk"
	csrfCookie    = "feldcore_csrf"
	pendingCookie = "feldcore_pending"
	passkeyCookie = "feldcore_passkey"

	// LevelPublic needs no sign-in. LevelUsers is any signed-in person.
	// LevelGroups is the listed groups. LevelAdmin is the administrator only.
	LevelPublic = "public"
	LevelUsers  = "users"
	LevelGroups = "groups"
	LevelAdmin  = "admin"
)

var (
	ErrInvalidCredentials = errors.New("incorrect username or password")
	ErrRateLimited        = errors.New("too many failed attempts; wait a few minutes and try again")
	ErrSecondFactor       = errors.New("enter the code from your authenticator")
	ErrInvalidCode        = errors.New("that verification code is not valid")
	ErrDisabled           = errors.New("this account is disabled")
	ErrNotFound           = errors.New("not found")
)

// InputError is a validation failure safe to show.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

// Identity is a signed-in person.
type Identity struct {
	ID          string   `json:"id"`
	Username    string   `json:"username"`
	DisplayName string   `json:"displayName,omitempty"`
	Admin       bool     `json:"admin"`
	GroupIDs    []string `json:"groupIds,omitempty"`
	Version     int      `json:"-"`
}

// IdentityProvider authenticates a username and password.
// code is the authenticator code, or empty when the account has no second
// factor. ErrSecondFactor means the password was accepted and a code is
// still required.
//
// Local is the only provider built. An Entra ID (OIDC) or Cloudflare Access
// provider can implement this later; Authorize does not need to change.
type IdentityProvider interface {
	Name() string
	Authenticate(username, password, code string) (Identity, error)
}

// AppAccess is the rule for one published app.
type AppAccess struct {
	// ID is the container name the proxy already uses.
	ID   string
	Name string
	Host string
	// Level is public, users, groups or admin. Empty means public.
	Level    string
	GroupIDs []string
}

// Decision is what Authorize decided.
type Decision struct {
	Allow bool
	// Reason is public, ok, unauthenticated or forbidden.
	Reason   string
	Identity *Identity
	// Location is the Citizen ID sign-in URL when a browser should be sent there.
	Location string
}

// Auditor is the existing audit log. auth.Service implements it.
type Auditor interface {
	Record(tokenID string, ev store.AuditEvent) error
}

// Deps are the stores Citizen ID extends. There is no separate database.
type Deps struct {
	Store   *store.Store
	Secrets *secrets.Box
	Audit   Auditor
	Log     *slog.Logger
	// Trust reports whether a TCP peer is Caddy or cloudflared. Forwarded
	// client headers are honoured only from those peers and from loopback.
	Trust trustPeer
}

// Gateway is the sign-in service the proxy and the UI call.
type Gateway struct {
	st       *store.Store
	box      *secrets.Box
	audit    Auditor
	log      *slog.Logger
	sessions *scs.SessionManager
	provider IdentityProvider
	lim      *limiter
	argon    argonParams
	now      func() time.Time

	cookieMu    sync.Mutex
	dummyOnce   sync.Once
	dummy       string
	bcryptOnce  sync.Once
	bcryptDummy []byte
	trust       trustPeer
	stepMu      sync.Mutex

	mu         sync.Mutex
	pending    map[string]pendingLogin
	ceremonies map[string]ceremony
	noise      map[string]time.Time
}

type pendingLogin struct {
	Username string
	IP       string
	Expires  time.Time
}

type identityCtxKey struct{}

// WithIdentity stores id on ctx for handlers behind Middleware.
func WithIdentity(ctx context.Context, id Identity) context.Context {
	return context.WithValue(ctx, identityCtxKey{}, id)
}

// FromContext returns the identity Middleware attached.
func FromContext(ctx context.Context) (Identity, bool) {
	id, ok := ctx.Value(identityCtxKey{}).(Identity)
	return id, ok
}

// Open returns a gateway. The local provider is installed.
func Open(d Deps) *Gateway {
	g := &Gateway{
		st: d.Store, box: d.Secrets, audit: d.Audit, log: d.Log, trust: d.Trust,
		lim: newLimiter(8, 5*time.Minute), argon: prodArgon, now: time.Now,
		pending: map[string]pendingLogin{}, ceremonies: map[string]ceremony{},
	}
	sm := scs.New()
	sm.Store = &sessionStore{st: d.Store}
	sm.Lifetime = 7 * 24 * time.Hour
	sm.HashTokenInStore = true
	sm.Cookie = scs.SessionCookie{
		Name:     cookieName,
		HttpOnly: true,
		Path:     "/",
		SameSite: http.SameSiteLaxMode,
		Persist:  true,
	}
	g.sessions = sm
	g.provider = &localProvider{g: g}
	return g
}

// SetProvider replaces the local directory. Authorize is unchanged.
// Passing nil keeps the current provider.
func (g *Gateway) SetProvider(p IdentityProvider) {
	if p != nil {
		g.provider = p
	}
}

// ProviderName reports which IdentityProvider is installed.
func (g *Gateway) ProviderName() string {
	if g.provider == nil {
		return ""
	}
	return g.provider.Name()
}

// Gated reports whether a level needs the proxy to call Citizen ID.
// Public and empty stay open. Anything else, including a level this
// version does not recognise, is closed.
func Gated(level string) bool {
	switch level {
	case "", LevelPublic:
		return false
	default:
		return true
	}
}

func knownLevel(level string) bool {
	switch level {
	case LevelPublic, LevelUsers, LevelGroups, LevelAdmin:
		return true
	default:
		return false
	}
}

// Authorize decides whether r may open app.
// A public app is allowed with no session. Otherwise the caller needs the
// parent-domain session cookie (or the host-only one, when no parent domain
// is configured). Administrators may open every level.
func (g *Gateway) Authorize(r *http.Request, app AppAccess) (Decision, error) {
	level := app.Level
	if level == "" {
		level = LevelPublic
	}
	app.Level = level
	if level == LevelPublic {
		return Decision{Allow: true, Reason: "public"}, nil
	}
	// A gated app on plain HTTP is only answered for a local client.
	// A public address must use TLS before a session cookie is accepted.
	if !g.requestSecure(r) && !localClient(g.clientIP(r)) {
		g.record("", "access denied", appLabel(app), "denied")
		return Decision{Allow: false, Reason: "forbidden"}, nil
	}
	id, ok := g.Identity(r)
	if !knownLevel(level) {
		if ok {
			g.record(id.Username, "access denied", appLabel(app), "denied")
			cp := id
			return Decision{Allow: false, Reason: "forbidden", Identity: &cp}, nil
		}
		return Decision{
			Allow: false, Reason: "unauthenticated",
			Location: "/citizen/signin?next=" + url.QueryEscape(g.safeNext(r, g.returnURL(r))),
		}, nil
	}
	if !ok {
		return Decision{
			Allow: false, Reason: "unauthenticated",
			Location: "/citizen/signin?next=" + url.QueryEscape(g.safeNext(r, g.returnURL(r))),
		}, nil
	}
	if id.Admin || allowed(id, app) {
		cp := id
		return Decision{Allow: true, Reason: "ok", Identity: &cp}, nil
	}
	g.record(id.Username, "access denied", appLabel(app), "denied")
	cp := id
	return Decision{Allow: false, Reason: "forbidden", Identity: &cp}, nil
}

func allowed(id Identity, app AppAccess) bool {
	switch app.Level {
	case LevelPublic, LevelUsers:
		return true
	case LevelAdmin:
		return id.Admin
	case LevelGroups:
		if id.Admin {
			return true
		}
		for _, want := range app.GroupIDs {
			for _, have := range id.GroupIDs {
				if want == have {
					return true
				}
			}
		}
		return false
	default:
		return false
	}
}

func appLabel(app AppAccess) string {
	switch {
	case app.Host != "":
		return app.Host
	case app.Name != "":
		return app.Name
	default:
		return app.ID
	}
}

// Middleware authorises one app and redirects a browser to Citizen ID.
// The proxy's forward_auth endpoint is ServeCheck; this wrapper is for an
// in-process caller that already has the *http.Request.
func (g *Gateway) Middleware(app AppAccess) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			d, err := g.Authorize(r, app)
			if err != nil {
				http.Error(w, "Something went wrong.", http.StatusInternalServerError)
				return
			}
			if !d.Allow {
				if d.Location != "" && d.Reason == "unauthenticated" {
					http.Redirect(w, r, d.Location, http.StatusFound)
					return
				}
				http.Error(w, "You do not have access to this app.", http.StatusForbidden)
				return
			}
			if d.Identity != nil {
				r = r.WithContext(WithIdentity(r.Context(), *d.Identity))
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ServeCheck is the forward_auth target. It reads X-Forwarded-Host, which
// the proxy sets to the app the browser asked for.
func (g *Gateway) ServeCheck(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Cache-Control", "no-store")
	host := g.forwardedHost(r)
	app, ok := g.AppForHost(host)
	if !ok {
		http.Error(w, "That hostname is not published.", http.StatusForbidden)
		return
	}
	d, err := g.Authorize(r, app)
	if err != nil {
		http.Error(w, "Something went wrong.", http.StatusInternalServerError)
		return
	}
	if d.Allow {
		w.WriteHeader(http.StatusOK)
		return
	}
	if d.Location != "" && d.Reason == "unauthenticated" {
		http.Redirect(w, r, d.Location, http.StatusFound)
		return
	}
	http.Error(w, "You do not have access to this app.", http.StatusForbidden)
}

// AppForHost finds the published route and its access rule.
func (g *Gateway) AppForHost(host string) (AppAccess, bool) {
	host = strings.ToLower(stripPort(host))
	if host == "" {
		return AppAccess{}, false
	}
	snap := g.st.Snapshot()
	var route *store.WebRoute
	for i := range snap.Web.Routes {
		if snap.Web.Routes[i].Enabled && snap.Web.Routes[i].Hostname == host {
			route = &snap.Web.Routes[i]
			break
		}
	}
	if route == nil {
		return AppAccess{}, false
	}
	level, groups := g.Effective(route.Container, route.Hostname)
	app := AppAccess{ID: route.Container, Name: route.Hostname, Host: route.Hostname, Level: level, GroupIDs: groups}
	return app, true
}

// Identity resolves the Citizen ID cookie. A disabled account is signed out.
func (g *Gateway) Identity(r *http.Request) (Identity, bool) {
	ctx, err := g.load(r)
	if err != nil {
		return Identity{}, false
	}
	return g.identity(ctx)
}

// IdentityForUsername is the person behind a legacy administrator session,
// which does not carry the Citizen ID cookie.
func (g *Gateway) IdentityForUsername(username string) (Identity, bool) {
	return g.lookup(username)
}

func (g *Gateway) identity(ctx context.Context) (Identity, bool) {
	username := g.sessions.GetString(ctx, "username")
	if username == "" {
		return Identity{}, false
	}
	id, ok := g.lookup(username)
	if !ok {
		return Identity{}, false
	}
	if g.sessions.GetString(ctx, "ver") != strconv.Itoa(id.Version) {
		return Identity{}, false
	}
	// The session is bound to the account id. Recreating a username does
	// not revive a deleted person's session.
	if g.sessions.GetString(ctx, "uid") != id.ID {
		return Identity{}, false
	}
	return id, true
}

func (g *Gateway) lookup(username string) (Identity, bool) {
	snap := g.st.Snapshot()
	if snap.Admin != nil && snap.Admin.Username == username {
		return Identity{
			ID: "admin", Username: snap.Admin.Username, DisplayName: snap.Admin.Username,
			Admin: true, Version: snap.Admin.SessionVersion,
		}, true
	}
	for _, a := range snap.Accounts {
		if a.Username != username || a.Disabled {
			continue
		}
		return Identity{
			ID: a.ID, Username: a.Username, DisplayName: displayName(a),
			GroupIDs: append([]string(nil), a.GroupIDs...), Version: a.SessionVersion,
		}, true
	}
	return Identity{}, false
}

// Login checks the provider and, on success, sets the session cookie.
func (g *Gateway) Login(w http.ResponseWriter, r *http.Request, username, password, code string) (Identity, error) {
	username = strings.TrimSpace(username)
	if id, err := g.authenticate(r, username, password, code); err != nil {
		return Identity{}, err
	} else {
		factor := "password"
		if strings.TrimSpace(code) != "" {
			factor = "password+totp"
		}
		if err := g.startSession(w, r, id, factor); err != nil {
			return Identity{}, err
		}
		g.record(id.Username, "sign-in", factor, "ok")
		return id, nil
	}
}

func (g *Gateway) authenticate(r *http.Request, username, password, code string) (Identity, error) {
	ip := g.clientIP(r)
	userKey := "user:" + strings.ToLower(username)
	keys := []string{"ip:" + ip}
	if username != "" {
		keys = append(keys, userKey)
	}
	done, allowed := g.lim.begin(g.now(), keys...)
	if !allowed {
		if g.knownAccount(username) {
			g.record(username, "sign-in failed", "locked", "denied")
		} else {
			g.recordNoise(ip)
		}
		return Identity{}, ErrRateLimited
	}
	id, err := g.provider.Authenticate(username, password, code)
	if err == nil {
		done(false)
		if username != "" {
			// A success clears the username bucket only. The address keeps
			// its failures, so one good password cannot reset a shared IP.
			g.lim.reset(userKey)
		}
		return id, nil
	}
	if errors.Is(err, ErrSecondFactor) {
		done(false)
		return Identity{}, err
	}
	done(true)
	if g.knownAccount(username) {
		g.record(username, "sign-in failed", "", "denied")
	} else {
		g.recordNoise(ip)
	}
	return Identity{}, err
}

func (g *Gateway) knownAccount(username string) bool {
	username = strings.TrimSpace(username)
	if username == "" {
		return false
	}
	snap := g.st.Snapshot()
	if snap.Admin != nil && snap.Admin.Username == username {
		return true
	}
	for _, a := range snap.Accounts {
		if a.Username == username {
			return true
		}
	}
	return false
}

// recordNoise keeps at most one anonymous failure per address per minute,
// and marks it so the audit trim can drop it before a real security event.
func (g *Gateway) recordNoise(ip string) {
	g.mu.Lock()
	if g.noise == nil {
		g.noise = map[string]time.Time{}
	}
	now := g.now()
	if t, ok := g.noise[ip]; ok && now.Sub(t) < time.Minute {
		g.mu.Unlock()
		return
	}
	if len(g.noise) > 1024 {
		g.noise = map[string]time.Time{}
	}
	g.noise[ip] = now
	g.mu.Unlock()
	g.record("", "sign-in noise", ip, "denied")
}

// Logout ends the Citizen ID session and clears the cookie.
func (g *Gateway) Logout(w http.ResponseWriter, r *http.Request) error {
	g.cookieMu.Lock()
	defer g.cookieMu.Unlock()
	g.dropDeskLocked(w, r)
	g.prepareCookie(r)
	ctx, err := g.loadLocked(r)
	if err != nil {
		g.writeClear(w, r)
		return err
	}
	if err := g.sessions.Destroy(ctx); err != nil {
		g.writeClear(w, r)
		return err
	}
	return g.commitLocked(w, ctx)
}

func (g *Gateway) startSession(w http.ResponseWriter, r *http.Request, id Identity, factor string) error {
	g.cookieMu.Lock()
	defer g.cookieMu.Unlock()
	g.prepareCookie(r)
	ctx, err := g.loadLocked(r)
	if err != nil {
		return err
	}
	if err := g.sessions.RenewToken(ctx); err != nil {
		return err
	}
	fresh, ok := g.lookup(id.Username)
	if !ok {
		return ErrInvalidCredentials
	}
	g.sessions.Put(ctx, "username", fresh.Username)
	g.sessions.Put(ctx, "uid", fresh.ID)
	g.sessions.Put(ctx, "admin", fresh.Admin)
	g.sessions.Put(ctx, "ver", strconv.Itoa(fresh.Version))
	g.sessions.Put(ctx, "factor", factor)
	return g.commitLocked(w, ctx)
}

// StartDesk sets the host-only cookie the management UI uses. The parent-domain
// citizen cookie is never accepted by /api/v1.
func (g *Gateway) StartDesk(w http.ResponseWriter, r *http.Request, id Identity) error {
	g.cookieMu.Lock()
	defer g.cookieMu.Unlock()
	name := deskCookie
	secure := g.requestSecure(r)
	if secure {
		name = "__Host-" + deskCookie
	}
	g.sessions.Cookie.Name = name
	g.sessions.Cookie.Domain = ""
	g.sessions.Cookie.Secure = secure
	g.sessions.Cookie.Path = "/"
	g.sessions.Cookie.HttpOnly = true
	g.sessions.Cookie.SameSite = http.SameSiteStrictMode
	g.sessions.Cookie.Persist = true
	g.sessions.Lifetime = g.lifetime()
	ctx, err := g.sessions.Load(r.Context(), "")
	if err != nil {
		g.prepareCookie(r)
		return err
	}
	if err := g.sessions.RenewToken(ctx); err != nil {
		g.prepareCookie(r)
		return err
	}
	fresh, ok := g.lookup(id.Username)
	if !ok {
		g.prepareCookie(r)
		return ErrInvalidCredentials
	}
	g.sessions.Put(ctx, "username", fresh.Username)
	g.sessions.Put(ctx, "uid", fresh.ID)
	g.sessions.Put(ctx, "admin", fresh.Admin)
	g.sessions.Put(ctx, "ver", strconv.Itoa(fresh.Version))
	g.sessions.Put(ctx, "factor", "desk")
	err = g.commitLocked(w, ctx)
	g.prepareCookie(r)
	return err
}

// DeskIdentity resolves the host-only management cookie. The parent-domain
// citizen cookie is ignored here.
func (g *Gateway) DeskIdentity(r *http.Request) (Identity, bool) {
	g.cookieMu.Lock()
	defer g.cookieMu.Unlock()
	token := ""
	for _, name := range []string{"__Host-" + deskCookie, deskCookie} {
		if c, err := r.Cookie(name); err == nil && c.Value != "" {
			token = c.Value
			break
		}
	}
	if token == "" {
		return Identity{}, false
	}
	ctx, err := g.sessions.Load(r.Context(), token)
	if err != nil {
		return Identity{}, false
	}
	return g.identity(ctx)
}

func (g *Gateway) dropDeskLocked(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{"__Host-" + deskCookie, deskCookie} {
		c, err := r.Cookie(name)
		if err != nil || c.Value == "" {
			continue
		}
		secure := g.requestSecure(r) || strings.HasPrefix(name, "__Host-")
		g.sessions.Cookie.Name = name
		g.sessions.Cookie.Domain = ""
		g.sessions.Cookie.Secure = secure
		g.sessions.Cookie.Path = "/"
		g.sessions.Cookie.HttpOnly = true
		g.sessions.Cookie.SameSite = http.SameSiteStrictMode
		ctx, err := g.sessions.Load(r.Context(), c.Value)
		if err == nil {
			_ = g.sessions.Destroy(ctx)
			_ = g.commitLocked(w, ctx)
		}
	}
}

// ResolveGroups turns stored group names or ids into ids.
func (g *Gateway) ResolveGroups(names []string) []string {
	snap := g.st.Snapshot()
	var ids []string
	seen := map[string]bool{}
	for _, name := range names {
		n := strings.TrimSpace(name)
		if n == "" {
			continue
		}
		for _, gr := range snap.Groups {
			if gr.ID == n || strings.EqualFold(gr.Name, n) {
				if !seen[gr.ID] {
					ids = append(ids, gr.ID)
					seen[gr.ID] = true
				}
			}
		}
	}
	return ids
}

func (g *Gateway) prepareCookie(r *http.Request) {
	g.sessions.Lifetime = g.lifetime()
	domain := g.cookieDomain(r)
	secure := g.requestSecure(r)
	name := cookieName
	// __Host- forbids a Domain attribute. The parent-domain cookie therefore
	// keeps the plain name. On a host-only HTTPS sign-in the prefix is used.
	if secure && domain == "" {
		name = "__Host-" + cookieName
	}
	g.sessions.Cookie.Name = name
	g.sessions.Cookie.Domain = domain
	g.sessions.Cookie.Secure = secure
	g.sessions.Cookie.Path = "/"
	g.sessions.Cookie.HttpOnly = true
	g.sessions.Cookie.SameSite = http.SameSiteLaxMode
	g.sessions.Cookie.Persist = true
}

func (g *Gateway) load(r *http.Request) (context.Context, error) {
	g.cookieMu.Lock()
	defer g.cookieMu.Unlock()
	return g.loadLocked(r)
}

func (g *Gateway) loadLocked(r *http.Request) (context.Context, error) {
	// Prefer the host-only cookie. A planted plain cookie must not override it,
	// and a present __Host- value is not replaced by the plain name when it
	// fails to load.
	if c, err := r.Cookie("__Host-" + cookieName); err == nil && c.Value != "" {
		return g.sessions.Load(r.Context(), c.Value)
	}
	token := ""
	if c, err := r.Cookie(cookieName); err == nil {
		token = c.Value
	}
	return g.sessions.Load(r.Context(), token)
}

func (g *Gateway) commitLocked(w http.ResponseWriter, ctx context.Context) error {
	switch g.sessions.Status(ctx) {
	case scs.Unmodified:
		return nil
	case scs.Destroyed:
		g.sessions.WriteSessionCookie(ctx, w, "", time.Time{})
		return nil
	default:
		token, expiry, err := g.sessions.Commit(ctx)
		if err != nil {
			return err
		}
		g.sessions.WriteSessionCookie(ctx, w, token, expiry)
		return nil
	}
}

func (g *Gateway) writeClear(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{cookieName, "__Host-" + cookieName} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", Domain: g.sessions.Cookie.Domain,
			MaxAge: -1, HttpOnly: true, SameSite: http.SameSiteLaxMode, Secure: g.requestSecure(r) || strings.HasPrefix(name, "__Host-"),
		})
	}
	for _, name := range []string{deskCookie, "__Host-" + deskCookie} {
		http.SetCookie(w, &http.Cookie{
			Name: name, Value: "", Path: "/", MaxAge: -1, HttpOnly: true,
			SameSite: http.SameSiteStrictMode, Secure: g.requestSecure(r) || strings.HasPrefix(name, "__Host-"),
		})
	}
}

func (g *Gateway) lifetime() time.Duration {
	h := g.st.Snapshot().Prefs.SessionHours
	if h < 1 || h > 24*30 {
		return 7 * 24 * time.Hour
	}
	return time.Duration(h) * time.Hour
}

func (g *Gateway) cookieDomain(r *http.Request) string {
	host := strings.ToLower(stripPort(g.forwardedHost(r)))
	parent := g.parentDomain()
	if parent == "" || host == "" || host == "localhost" || net.ParseIP(host) != nil {
		return ""
	}
	if host == parent || strings.HasSuffix(host, "."+parent) {
		return parent
	}
	return ""
}

func (g *Gateway) parentDomain() string {
	d := strings.ToLower(strings.TrimSpace(g.st.Snapshot().Web.Domain))
	d = strings.TrimPrefix(d, ".")
	if d == "" || strings.ContainsAny(d, " /:") || net.ParseIP(d) != nil {
		return ""
	}
	return d
}

func (g *Gateway) record(username, action, target, result string) {
	if g.audit == nil {
		return
	}
	name := strings.TrimSpace(username)
	if name == "" {
		name = "Citizen ID"
	}
	if len(name) > 64 {
		name = name[:64]
	}
	if err := g.audit.Record("", store.AuditEvent{
		TokenName: name, Action: action, Target: clip(target, 200), Result: result,
	}); err != nil && g.log != nil {
		g.log.Warn("citizen id audit log write failed", "err", err)
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

func (g *Gateway) returnURL(r *http.Request) string {
	proto := "http"
	if g.requestSecure(r) {
		proto = "https"
	}
	host := g.forwardedHost(r)
	if host == "" {
		host = "localhost"
	}
	uri := r.Header.Get("X-Forwarded-Uri")
	if uri == "" {
		uri = r.URL.RequestURI()
	}
	if !strings.HasPrefix(uri, "/") || strings.HasPrefix(uri, "//") {
		uri = "/"
	}
	return proto + "://" + host + uri
}

// safeNext returns next when it is a single-slash path or an absolute URL
// whose host is a published app (or the management UI). Control characters,
// backslashes, scheme-relative URLs and any other host become "/".
func (g *Gateway) safeNext(r *http.Request, next string) string {
	next = strings.TrimSpace(next)
	if next == "" {
		return "/"
	}
	decoded := next
	for i := 0; i < 4; i++ {
		d, err := url.PathUnescape(decoded)
		if err != nil || d == decoded {
			break
		}
		decoded = d
	}
	if decoded == "" || hasControl(decoded) || strings.Contains(decoded, "\\") {
		return "/"
	}
	if strings.HasPrefix(decoded, "/") {
		if strings.HasPrefix(decoded, "//") || strings.Contains(decoded, "://") {
			return "/"
		}
		return decoded
	}
	u, err := url.Parse(decoded)
	if err != nil || u.User != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return "/"
	}
	if hasControl(u.Path) || strings.Contains(u.Path, "\\") || strings.HasPrefix(u.EscapedPath(), "//") {
		return "/"
	}
	if !g.allowedNextHost(strings.ToLower(stripPort(u.Host))) {
		return "/"
	}
	return u.Scheme + "://" + u.Host + u.EscapedPath() + q(u.RawQuery)
}

func hasControl(s string) bool {
	for _, r := range s {
		if r < 0x20 || r == 0x7f {
			return true
		}
	}
	return false
}

func (g *Gateway) allowedNextHost(host string) bool {
	snap := g.st.Snapshot()
	for _, rt := range snap.Web.Routes {
		if rt.Enabled && strings.EqualFold(rt.Hostname, host) {
			return true
		}
	}
	for _, app := range snap.Publish.Apps {
		if app.Enabled && strings.EqualFold(app.Hostname, host) {
			return true
		}
	}
	if snap.Web.UIEnabled {
		h := snap.Web.UIHostname
		if h == "" {
			d := snap.Web.Domain
			if d == "" {
				d = "home.arpa"
			}
			h = "feldcore." + d
		}
		if strings.EqualFold(stripPort(h), host) {
			return true
		}
	}
	return false
}

func q(raw string) string {
	if raw == "" {
		return ""
	}
	return "?" + raw
}

// headersTrusted reports whether this TCP peer may set X-Forwarded-Proto and
// X-Forwarded-Host. Loopback and the trusted-peer callback (Caddy and the
// containerized cloudflared) qualify. Anyone else is ignored, so a client
// cannot spoof https or the public hostname.
func (g *Gateway) headersTrusted(r *http.Request) bool {
	if r == nil {
		return false
	}
	return g.Trusts(r.Context(), remoteIP(r))
}

// Trusts reports whether ip is loopback or a proxy this gateway was told to trust.
func (g *Gateway) Trusts(ctx context.Context, ip net.IP) bool {
	if ip != nil && ip.IsLoopback() {
		return true
	}
	return g != nil && ip != nil && g.trust != nil && g.trust(ctx, ip)
}

// clientHost is the host the browser used, including a port. The forwarded
// host is read only from a trusted peer.
func (g *Gateway) clientHost(r *http.Request) string {
	if r == nil {
		return ""
	}
	if g.headersTrusted(r) {
		if h := firstForwarded(r.Header.Get("X-Forwarded-Host")); h != "" {
			return h
		}
	}
	return strings.TrimSpace(r.Host)
}

func (g *Gateway) forwardedHost(r *http.Request) string {
	return stripPort(g.clientHost(r))
}

func (g *Gateway) requestSecure(r *http.Request) bool {
	if r != nil && r.TLS != nil {
		return true
	}
	if !g.headersTrusted(r) {
		return false
	}
	return strings.EqualFold(firstForwarded(r.Header.Get("X-Forwarded-Proto")), "https")
}

func firstForwarded(v string) string {
	return strings.TrimSpace(strings.Split(v, ",")[0])
}

func stripPort(host string) string {
	host = strings.TrimSpace(host)
	if host == "" {
		return ""
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		return h
	}
	return host
}

func randomID() (string, error) {
	raw := make([]byte, 16)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	return hex.EncodeToString(raw), nil
}

func hashKey(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

func displayName(a store.Account) string {
	if strings.TrimSpace(a.DisplayName) != "" {
		return a.DisplayName
	}
	return a.Username
}

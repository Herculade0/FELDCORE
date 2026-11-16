package api

import (
	"bufio"
	"context"
	"errors"
	"net"
	"net/http"
	"strings"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/store"
)

// principal is who the request authenticated as. A browser session is the
// administrator and is not scoped. An agent token is.
type principal struct {
	ViaToken  bool
	Username  string
	TokenID   string
	TokenName string
	Scopes    []string
}

type principalKey struct{}

func withPrincipal(ctx context.Context, p principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

func principalFrom(ctx context.Context) (principal, bool) {
	p, ok := ctx.Value(principalKey{}).(principal)
	return p, ok && (p.ViaToken || p.Username != "")
}

func bearerToken(h http.Header) (string, bool) {
	v := strings.TrimSpace(h.Get("Authorization"))
	const scheme = "bearer "
	if len(v) < len(scheme) || !strings.EqualFold(v[:len(scheme)], scheme) {
		return "", false
	}
	tok := strings.TrimSpace(v[len(scheme):])
	if tok == "" || strings.Contains(tok, " ") {
		return "", false
	}
	return tok, true
}

// requireAuth accepts a browser session or a bearer agent token.
// A valid session wins when both are present, so the signed-in UI is never
// narrowed by a token header.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if user, ok := s.currentUser(r); ok {
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), principal{Username: user})))
			return
		}
		if raw, ok := bearerToken(r.Header); ok {
			if s.auth == nil {
				writeError(w, http.StatusUnauthorized, "Sign in to continue.")
				return
			}
			tok, ok := s.auth.AuthenticateToken(raw)
			if !ok {
				w.Header().Set("WWW-Authenticate", `Bearer realm="feldcore"`)
				writeError(w, http.StatusUnauthorized, "That agent token is not valid.")
				return
			}
			p := principal{ViaToken: true, TokenID: tok.ID, TokenName: tok.Name, Scopes: tok.Scopes}
			next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
			return
		}
		writeError(w, http.StatusUnauthorized, "Sign in to continue.")
	})
}

// requireBearer is the MCP gate. A session cookie is not enough: every MCP
// request has to carry Authorization: Bearer, and the token needs scope.
func (s *Server) requireBearer(scope string, next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, ok := bearerToken(r.Header)
		if !ok || s.auth == nil {
			w.Header().Set("WWW-Authenticate", `Bearer realm="feldcore"`)
			writeError(w, http.StatusUnauthorized, "Send Authorization: Bearer with an agent token.")
			return
		}
		tok, ok := s.auth.AuthenticateToken(raw)
		if !ok {
			w.Header().Set("WWW-Authenticate", `Bearer realm="feldcore"`)
			writeError(w, http.StatusUnauthorized, "That agent token is not valid.")
			return
		}
		p := principal{ViaToken: true, TokenID: tok.ID, TokenName: tok.Name, Scopes: tok.Scopes}
		if !auth.Allows(p.Scopes, scope) {
			s.record(p, "mcp", "", "denied")
			writeError(w, http.StatusForbidden, "This token does not include the "+scope+" scope.")
			return
		}
		next.ServeHTTP(w, r.WithContext(withPrincipal(r.Context(), p)))
	})
}

// enforceScope checks a token against the route it called, then records the
// call. Browser sessions skip the check. Anything not listed as read or as a
// container write requires admin, and a shell is refused to every token.
func (s *Server) enforceScope(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		p, ok := principalFrom(r.Context())
		if !ok || !p.ViaToken {
			next.ServeHTTP(w, r)
			return
		}
		need := scopeFor(r.Method, r.URL.Path)
		action := r.Method + " " + strings.TrimPrefix(r.URL.Path, "/api/v1")
		target := targetFrom(r.URL.Path)
		if !auth.Allows(p.Scopes, need) {
			s.record(p, action, target, "denied")
			msg := "This token does not include the " + need + " scope."
			if need == "shell" {
				msg = "Agent tokens cannot open a shell."
			}
			writeError(w, http.StatusForbidden, msg)
			return
		}
		cw := &capture{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(cw, r)
		s.record(p, action, target, resultOf(cw.status))
	})
}

func actorOf(r *http.Request) (string, string) {
	p, ok := principalFrom(r.Context())
	if !ok {
		return "admin", ""
	}
	if p.ViaToken {
		name := p.TokenName
		if name == "" {
			name = "agent"
		}
		return name, p.TokenID
	}
	if p.Username != "" {
		return p.Username, ""
	}
	return "admin", ""
}

// auditFile records a change made by a signed-in administrator or by an agent
// token: share files, health marks, and notification settings. Reads are not
// recorded here; enforceScope already logs token calls.
func (s *Server) auditFile(r *http.Request, action, target, result string) {
	if s.auth == nil {
		return
	}
	name, id := actorOf(r)
	if err := s.auth.Record(id, store.AuditEvent{
		TokenName: name,
		Action:    action,
		Target:    clip(target, 200),
		Result:    result,
	}); err != nil && s.log != nil {
		s.log.Warn("audit log write failed", "err", err)
	}
}

func (s *Server) record(p principal, action, target, result string) {
	if s.auth == nil || !p.ViaToken {
		return
	}
	if err := s.auth.Record(p.TokenID, store.AuditEvent{
		TokenName: p.TokenName,
		Action:    action,
		Target:    clip(target, 200),
		Result:    result,
	}); err != nil && s.log != nil {
		s.log.Warn("audit log write failed", "err", err)
	}
}

func resultOf(status int) string {
	switch {
	case status == http.StatusUnauthorized || status == http.StatusForbidden:
		return "denied"
	case status >= 400:
		return "error"
	default:
		return "ok"
	}
}

func clip(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// scopeFor maps a method and URL path to the scope a token needs.
// The default is admin, so a new route is closed to lesser tokens until it
// is classified. "shell" is not a grantable scope.
func scopeFor(method, path string) string {
	p := path
	if i := strings.Index(p, "/api/v1"); i >= 0 {
		p = p[i+len("/api/v1"):]
	}
	if p == "" {
		p = "/"
	}
	// The host terminal, a container console, and a VM's VNC console are a
	// shell. Agent tokens cannot open them; the handlers also require the
	// administrator's session cookie.
	if p == "/terminal" || strings.HasSuffix(p, "/terminal") || strings.HasSuffix(p, "/console") {
		return "shell"
	}
	if scope, ok := fileScope(method, p); ok {
		return scope
	}
	if method == http.MethodGet || method == http.MethodHead {
		if isReadPath(p) {
			return auth.ScopeRead
		}
		return auth.ScopeAdmin
	}
	if isContainerWrite(method, p) {
		return auth.ScopeContainers
	}
	if strings.HasPrefix(p, "/vms/") || p == "/vms" {
		return auth.ScopeVMs
	}
	return auth.ScopeAdmin
}

// fileScope classifies the share file browser. A zip download is a POST so
// the browser can send the selection, but it does not change the share.
// files:write includes files:read; admin includes both.
func fileScope(method, p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, "/shares/")
	if !ok {
		return "", false
	}
	_, sub, ok := strings.Cut(rest, "/")
	if !ok {
		return "", false
	}
	switch {
	case sub == "files" || strings.HasPrefix(sub, "files/"):
	case sub == "uploads" || strings.HasPrefix(sub, "uploads/"):
	case sub == "fetch" || strings.HasPrefix(sub, "fetch/"):
	default:
		return "", false
	}
	if method == http.MethodGet || method == http.MethodHead || (method == http.MethodPost && sub == "files/zip") {
		return auth.ScopeFilesRead, true
	}
	return auth.ScopeFilesWrite, true
}

func isReadPath(p string) bool {
	switch p {
	case "/system/info", "/system/stats", "/system/history", "/system/stream",
		"/shares", "/fs/suggest", "/apps",
		"/docker/status", "/docker/options", "/docker/containers", "/docker/images",
		"/vms",
		"/remote", "/proxy", "/proxy/ca", "/snapshots",
		"/health/status", "/health/history", "/health/thresholds",
		"/notifications/inbox":
		return true
	}
	if strings.HasPrefix(p, "/vms/") {
		return true
	}
	if strings.HasPrefix(p, "/apps/") && !strings.Contains(strings.TrimPrefix(p, "/apps/"), "/") {
		return true
	}
	if rest, ok := strings.CutPrefix(p, "/docker/containers/"); ok && strings.Contains(rest, "/") {
		return strings.HasSuffix(rest, "/spec") || strings.HasSuffix(rest, "/logs") ||
			strings.HasSuffix(rest, "/address") || strings.HasSuffix(rest, "/snapshots")
	}
	return false
}

func isContainerWrite(method, p string) bool {
	if p == "/docker/containers" && method == http.MethodPost {
		return true
	}
	if p == "/docker/review" && method == http.MethodPost {
		return true
	}
	rest, ok := strings.CutPrefix(p, "/docker/containers/")
	if !ok || rest == "" {
		return false
	}
	if !strings.Contains(rest, "/") {
		return method == http.MethodPut || method == http.MethodDelete
	}
	switch {
	case strings.HasSuffix(rest, "/start"), strings.HasSuffix(rest, "/stop"), strings.HasSuffix(rest, "/restart"):
		return method == http.MethodPost
	case strings.HasSuffix(rest, "/snapshots"):
		return method == http.MethodPost
	case strings.Contains(rest, "/snapshots/"):
		// Restore and delete a snapshot of this container. Publishing a
		// hostname (PUT .../address) stays an admin action.
		return method == http.MethodPost || method == http.MethodDelete
	default:
		return false
	}
}

func targetFrom(path string) string {
	p := path
	if i := strings.Index(p, "/api/v1"); i >= 0 {
		p = p[i+len("/api/v1"):]
	}
	switch {
	case strings.Contains(p, "/docker/containers/"):
		rest := strings.TrimPrefix(p, "/docker/containers/")
		id, _, _ := strings.Cut(rest, "/")
		return id
	case strings.Contains(p, "/shares/users/"):
		rest := strings.TrimPrefix(p, "/shares/users/")
		name, _, _ := strings.Cut(rest, "/")
		return name
	case strings.HasPrefix(p, "/shares/services/"):
		rest := strings.TrimPrefix(p, "/shares/services/")
		name, _, _ := strings.Cut(rest, "/")
		return name
	case strings.HasPrefix(p, "/shares/") && p != "/shares":
		name, _, _ := strings.Cut(strings.TrimPrefix(p, "/shares/"), "/")
		if name == "users" {
			return ""
		}
		return name
	case strings.HasPrefix(p, "/apps/"):
		return strings.TrimPrefix(p, "/apps/")
	case strings.HasPrefix(p, "/agent/tokens/"):
		id := strings.TrimPrefix(p, "/agent/tokens/")
		if id == "revoke-all" {
			return ""
		}
		return id
	case strings.HasPrefix(p, "/vms/"):
		id, _, _ := strings.Cut(strings.TrimPrefix(p, "/vms/"), "/")
		return id
	default:
		return ""
	}
}

// capture records the status code without hiding Hijack or Flush from the
// websocket handlers a token is allowed to open (the stats stream).
type capture struct {
	http.ResponseWriter
	status int
	wrote  bool
}

func (c *capture) WriteHeader(code int) {
	if !c.wrote {
		c.status = code
		c.wrote = true
	}
	c.ResponseWriter.WriteHeader(code)
}

func (c *capture) Write(b []byte) (int, error) {
	if !c.wrote {
		c.status = http.StatusOK
		c.wrote = true
	}
	return c.ResponseWriter.Write(b)
}

func (c *capture) Unwrap() http.ResponseWriter { return c.ResponseWriter }

func (c *capture) Flush() {
	if f, ok := c.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (c *capture) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	w := http.ResponseWriter(c.ResponseWriter)
	for i := 0; i < 8; i++ {
		if h, ok := w.(http.Hijacker); ok {
			return h.Hijack()
		}
		u, ok := w.(interface{ Unwrap() http.ResponseWriter })
		if !ok {
			break
		}
		w = u.Unwrap()
	}
	return nil, nil, errors.New("hijack is not supported")
}

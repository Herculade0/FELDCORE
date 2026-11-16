package api

import (
	"net"
	"net/http"
	"time"
)

const sessionCookie = "feldcore_session"

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

// currentUser resolves the request's session cookie to a username.
func (s *Server) currentUser(r *http.Request) (string, bool) {
	c, err := r.Cookie(sessionCookie)
	if err != nil {
		return "", false
	}
	return s.auth.Authenticate(c.Value)
}

// requireAuth rejects requests without a valid session.
func (s *Server) requireAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if _, ok := s.currentUser(r); !ok {
			writeError(w, http.StatusUnauthorized, "Sign in to continue.")
			return
		}
		next.ServeHTTP(w, r)
	})
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,                    // not readable by page scripts
		SameSite: http.SameSiteStrictMode, // never sent on cross-site requests
		Secure:   r.TLS != nil,
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookie,
		Value:    "",
		Path:     "/",
		MaxAge:   -1,
		HttpOnly: true,
		SameSite: http.SameSiteStrictMode,
		Secure:   r.TLS != nil,
	})
}

// clientKey identifies a caller for login rate limiting. It deliberately
// ignores X-Forwarded-For, which any client can forge.
func clientKey(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

func (s *Server) handleAuthStatus(w http.ResponseWriter, r *http.Request) {
	resp := map[string]any{
		"setupRequired": s.auth.SetupRequired(),
		"authenticated": false,
	}
	if user, ok := s.currentUser(r); ok {
		resp["authenticated"] = true
		resp["username"] = user
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(w, r, &in) {
		return
	}
	token, expires, err := s.auth.SetupAndLogin(in.Username, in.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("administrator account created", "user", in.Username)
	setSessionCookie(w, r, token, expires)
	writeJSON(w, http.StatusOK, map[string]string{"username": in.Username})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(w, r, &in) {
		return
	}
	token, expires, err := s.auth.Login(clientKey(r), in.Username, in.Password)
	if err != nil {
		s.log.Warn("failed sign-in", "from", clientKey(r))
		s.fail(w, r, err)
		return
	}
	setSessionCookie(w, r, token, expires)
	writeJSON(w, http.StatusOK, map[string]string{"username": in.Username})
}

func (s *Server) handleAuthLogout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(sessionCookie); err == nil {
		if err := s.auth.Logout(c.Value); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

package api

import (
	"net"
	"net/http"
	"strings"
	"time"
)

const sessionCookie = "feldcore_session"

type credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
	Code     string `json:"code"`
}

// currentUser resolves the request's session cookie to a username.
func (s *Server) currentUser(r *http.Request) (string, bool) {
	if s.auth == nil {
		return "", false
	}
	for _, name := range []string{"__Host-" + sessionCookie, sessionCookie} {
		c, err := r.Cookie(name)
		if err != nil || c.Value == "" {
			continue
		}
		if user, ok := s.auth.Authenticate(c.Value); ok {
			return user, true
		}
	}
	return "", false
}

func cookieSecure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

func sessionCookieName(r *http.Request) string {
	if cookieSecure(r) {
		return "__Host-" + sessionCookie
	}
	return sessionCookie
}

func setSessionCookie(w http.ResponseWriter, r *http.Request, token string, expires time.Time) {
	http.SetCookie(w, &http.Cookie{
		Name:     sessionCookieName(r),
		Value:    token,
		Path:     "/",
		Expires:  expires,
		HttpOnly: true,                    // not readable by page scripts
		SameSite: http.SameSiteStrictMode, // never sent on cross-site requests
		Secure:   cookieSecure(r),
	})
}

func clearSessionCookie(w http.ResponseWriter, r *http.Request) {
	for _, name := range []string{sessionCookie, "__Host-" + sessionCookie} {
		http.SetCookie(w, &http.Cookie{
			Name:     name,
			Value:    "",
			Path:     "/",
			MaxAge:   -1,
			HttpOnly: true,
			SameSite: http.SameSiteStrictMode,
			Secure:   cookieSecure(r) || strings.HasPrefix(name, "__Host-"),
		})
	}
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
	admin := false
	if user, ok := s.currentUser(r); ok {
		resp["authenticated"] = true
		resp["username"] = user
		admin = true
	} else if s.citizen != nil {
		if id, ok := s.citizen.DeskIdentity(r); ok {
			resp["authenticated"] = true
			resp["username"] = id.Username
			admin = false
		}
	}
	resp["admin"] = admin
	if s.wiz != nil && admin {
		resp["wizardPending"] = s.wiz.Pending()
	} else if s.wiz != nil && resp["authenticated"] != true {
		resp["wizardPending"] = s.wiz.Pending()
	}
	writeJSON(w, http.StatusOK, resp)
}

func (s *Server) handleAuthSetup(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(w, r, &in) {
		return
	}
	if s.wiz != nil {
		if err := s.wiz.CheckCode(clientKey(r), in.Code); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	token, expires, err := s.auth.SetupAndLogin(in.Username, in.Password)
	if err != nil {
		s.fail(w, r, err)
		return
	}
	s.log.Info("administrator account created", "user", in.Username)
	if s.wiz != nil {
		s.wiz.ConsumeCode()
		if err := s.wiz.Begin(s.skipWizard); err != nil {
			s.fail(w, r, err)
			return
		}
	}
	setSessionCookie(w, r, token, expires)
	writeJSON(w, http.StatusOK, map[string]string{"username": in.Username})
}

func (s *Server) handleAuthLogin(w http.ResponseWriter, r *http.Request) {
	var in credentials
	if !readJSON(w, r, &in) {
		return
	}
	token, expires, err := s.auth.LoginCode(clientKey(r), in.Username, in.Password, in.Code)
	if err != nil {
		if s.log != nil {
			s.log.Warn("failed sign-in", "from", clientKey(r))
		}
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
	if s.citizen != nil {
		_ = s.citizen.Logout(w, r)
	}
	clearSessionCookie(w, r)
	writeJSON(w, http.StatusOK, map[string]string{"status": "signed out"})
}

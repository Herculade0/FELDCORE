// Package api wires FELDCORE's HTTP surface: a versioned JSON API under
// /api/v1 plus the embedded single-page front-end served for everything else.
package api

import (
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/shares"
	"github.com/Herculade0/feldcore/internal/system"
)

// Deps are the services the HTTP layer exposes.
type Deps struct {
	System  *system.Collector
	Sampler *system.Sampler
	Auth    *auth.Service
	Shares  *shares.Manager
	WebRoot fs.FS
	Log     *slog.Logger
}

// Server holds the dependencies shared by HTTP handlers.
type Server struct {
	sys     *system.Collector
	sampler *system.Sampler
	auth    *auth.Service
	shares  *shares.Manager
	webRoot fs.FS
	log     *slog.Logger
}

// New returns a Server.
func New(d Deps) *Server {
	return &Server{
		sys: d.System, sampler: d.Sampler, auth: d.Auth, shares: d.Shares,
		webRoot: d.WebRoot, log: d.Log,
	}
}

// Router builds the full chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(securityHeaders)

	short := middleware.Timeout(30 * time.Second)

	r.Route("/api/v1", func(r chi.Router) {
		r.Use(sameOrigin)

		// Open endpoints: health and the login flow itself.
		r.Group(func(r chi.Router) {
			r.Use(short)
			r.Get("/health", s.handleHealth)
			r.Get("/auth/status", s.handleAuthStatus)
			r.Post("/auth/setup", s.handleAuthSetup)
			r.Post("/auth/login", s.handleAuthLogin)
			r.Post("/auth/logout", s.handleAuthLogout)
		})

		// Everything else needs a signed-in session.
		r.Group(func(r chi.Router) {
			r.Use(s.requireAuth)

			// Long-lived or slow endpoints sit outside the short timeout.
			r.Get("/system/stream", s.handleSystemStream)
			r.Post("/shares/services/{service}/install", s.handleServiceInstall)

			r.Group(func(r chi.Router) {
				r.Use(short)
				r.Get("/system/info", s.handleSystemInfo)
				r.Get("/system/stats", s.handleSystemStats)
				r.Get("/system/history", s.handleSystemHistory)

				r.Get("/shares", s.handleSharesOverview)
				r.Post("/shares", s.handleShareCreate)
				r.Put("/shares/{name}", s.handleShareUpdate)
				r.Delete("/shares/{name}", s.handleShareDelete)

				r.Post("/shares/users", s.handleShareUserCreate)
				r.Put("/shares/users/{name}/password", s.handleShareUserPassword)
				r.Delete("/shares/users/{name}", s.handleShareUserDelete)
			})
		})

		r.NotFound(func(w http.ResponseWriter, r *http.Request) {
			writeError(w, http.StatusNotFound, "No such API endpoint.")
		})
	})

	// Everything else is the SPA (static assets, or index.html fallback).
	r.NotFound(s.handleSPA)
	return r
}

// securityHeaders sets conservative browser protections on every response.
func securityHeaders(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		h := w.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "DENY")
		h.Set("Referrer-Policy", "same-origin")
		next.ServeHTTP(w, r)
	})
}

// sameOrigin rejects state-changing requests that a browser sent from another
// site. Together with the SameSite=Strict session cookie this blocks CSRF.
// Non-browser clients send no Origin header and are unaffected.
func sameOrigin(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.Method {
		case http.MethodGet, http.MethodHead, http.MethodOptions:
		default:
			if origin := r.Header.Get("Origin"); origin != "" {
				u, err := url.Parse(origin)
				if err != nil || !strings.EqualFold(u.Host, r.Host) {
					writeError(w, http.StatusForbidden, "Cross-site request refused.")
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeError(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]string{"error": msg})
}

// readJSON decodes a small JSON request body into v.
func readJSON(w http.ResponseWriter, r *http.Request, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, 64<<10)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		writeError(w, http.StatusBadRequest, "The request was not valid JSON.")
		return false
	}
	return true
}

// fail maps a service error onto an HTTP response.
func (s *Server) fail(w http.ResponseWriter, r *http.Request, err error) {
	var (
		authInput  *auth.InputError
		shareInput *shares.InputError
		shareOp    *shares.OpError
	)
	switch {
	case errors.As(err, &authInput):
		writeError(w, http.StatusBadRequest, authInput.Msg)
	case errors.As(err, &shareInput):
		writeError(w, http.StatusBadRequest, shareInput.Msg)
	case errors.As(err, &shareOp):
		s.log.Warn("operation failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, shareOp.Msg)
	case errors.Is(err, shares.ErrNotFound):
		writeError(w, http.StatusNotFound, "That item no longer exists.")
	case errors.Is(err, auth.ErrSetupDone):
		writeError(w, http.StatusConflict, "FELDCORE has already been set up. Sign in instead.")
	case errors.Is(err, auth.ErrInvalidCredentials):
		writeError(w, http.StatusUnauthorized, "Incorrect username or password.")
	case errors.Is(err, auth.ErrRateLimited):
		writeError(w, http.StatusTooManyRequests, "Too many failed attempts. Wait a few minutes and try again.")
	default:
		s.log.Error("request failed", "path", r.URL.Path, "err", err)
		writeError(w, http.StatusInternalServerError, "Something went wrong on the server. Check the FELDCORE log.")
	}
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.sys.Info(r.Context())
	if err != nil {
		s.fail(w, r, err)
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	st := s.sampler.Latest()
	if st == nil {
		writeError(w, http.StatusServiceUnavailable, "No sample yet.")
		return
	}
	writeJSON(w, http.StatusOK, st)
}

func (s *Server) handleSystemHistory(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, s.sampler.History())
}

// handleSPA serves embedded front-end files, falling back to index.html so the
// client-side router owns unknown paths.
func (s *Server) handleSPA(w http.ResponseWriter, r *http.Request) {
	p := strings.TrimPrefix(r.URL.Path, "/")
	if p == "" {
		p = "index.html"
	}
	if f, err := s.webRoot.Open(p); err == nil {
		f.Close()
		http.FileServer(http.FS(s.webRoot)).ServeHTTP(w, r)
		return
	}
	// SPA fallback.
	r2 := r.Clone(context.Background())
	r2.URL.Path = "/"
	http.StripPrefix("/", http.FileServer(http.FS(s.webRoot))).ServeHTTP(w, r2)
}

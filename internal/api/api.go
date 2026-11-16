// Package api wires FELDCORE's HTTP surface: a versioned JSON API under
// /api/v1 plus the embedded single-page front-end served for everything else.
package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"

	"github.com/Herculade0/feldcore/internal/system"
)

// Server holds the dependencies shared by HTTP handlers.
type Server struct {
	sys     *system.Collector
	webRoot fs.FS
}

// New returns a Server backed by the given metric collector and front-end FS.
func New(sys *system.Collector, webRoot fs.FS) *Server {
	return &Server{sys: sys, webRoot: webRoot}
}

// Router builds the full chi router.
func (s *Server) Router() http.Handler {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	r.Use(middleware.Recoverer)
	r.Use(middleware.Timeout(30 * time.Second))

	r.Route("/api/v1", func(r chi.Router) {
		r.Get("/health", s.handleHealth)
		r.Route("/system", func(r chi.Router) {
			r.Get("/info", s.handleSystemInfo)
			r.Get("/stats", s.handleSystemStats)
			r.Get("/stream", s.handleSystemStream)
		})
	})

	// Everything else is the SPA (static assets, or index.html fallback).
	r.NotFound(s.handleSPA)
	return r
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func (s *Server) handleHealth(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) handleSystemInfo(w http.ResponseWriter, r *http.Request) {
	info, err := s.sys.Info(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, info)
}

func (s *Server) handleSystemStats(w http.ResponseWriter, r *http.Request) {
	st, err := s.sys.Sample(r.Context())
	if err != nil {
		writeJSON(w, http.StatusInternalServerError, map[string]string{"error": err.Error()})
		return
	}
	writeJSON(w, http.StatusOK, st)
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

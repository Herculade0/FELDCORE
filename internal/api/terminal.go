package api

import (
	"context"
	"net/http"
	"net/url"
	"strings"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"

	"github.com/Herculade0/feldcore/internal/term"
)

// allowTerminal checks the admin session and that the browser's Origin matches
// this host. The WebSocket upgrade is refused otherwise.
func (s *Server) allowTerminal(w http.ResponseWriter, r *http.Request) bool {
	if _, ok := s.currentUser(r); !ok {
		writeError(w, http.StatusUnauthorized, "Sign in to continue.")
		return false
	}
	origin := r.Header.Get("Origin")
	u, err := url.Parse(origin)
	if origin == "" || err != nil || u.Host == "" || !strings.EqualFold(u.Host, r.Host) {
		writeError(w, http.StatusForbidden, "Cross-site request refused.")
		return false
	}
	return true
}

func (s *Server) handleHostTerminal(w http.ResponseWriter, r *http.Request) {
	if !s.allowTerminal(w, r) {
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	rw, resize, cleanup, err := term.StartHost()
	if err != nil {
		writeTermError(conn, "Could not open a shell on this server.")
		return
	}
	defer cleanup()
	term.Serve(r.Context(), conn, rw, resize)
}

func (s *Server) handleContainerTerminal(w http.ResponseWriter, r *http.Request) {
	if !s.allowTerminal(w, r) {
		return
	}
	if s.docker == nil {
		writeError(w, http.StatusServiceUnavailable, "Docker is not available.")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")

	rw, resize, cleanup, err := s.docker.Console(r.Context(), chi.URLParam(r, "id"))
	if err != nil {
		writeTermError(conn, "Could not open a console in that container. It needs to be running, with bash or sh installed.")
		return
	}
	defer cleanup()
	term.Serve(r.Context(), conn, rw, resize)
}

func writeTermError(conn *websocket.Conn, msg string) {
	ctx, cancel := context.WithTimeout(context.Background(), writeTimeout)
	defer cancel()
	_ = conn.Write(ctx, websocket.MessageBinary, []byte(msg+"\r\n"))
}

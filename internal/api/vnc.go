package api

import (
	"context"
	"net"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/go-chi/chi/v5"
)

// handleVMConsole proxies a guest's localhost VNC socket over an authenticated
// WebSocket. noVNC speaks the VNC protocol on that socket. The session check
// is the same one the host terminal uses, so a signed-out browser cannot
// attach, and a page on another origin cannot ride the cookie.
func (s *Server) handleVMConsole(w http.ResponseWriter, r *http.Request) {
	if !s.allowTerminal(w, r) {
		return
	}
	if s.vms == nil {
		writeError(w, http.StatusServiceUnavailable, "The VM manager is not available.")
		return
	}
	addr, err := s.vms.VNCAddr(r.Context(), chi.URLParam(r, "name"))
	if err != nil {
		s.fail(w, r, err)
		return
	}
	host, _, err := net.SplitHostPort(addr)
	if err != nil || !vncLoopback(host) {
		writeError(w, http.StatusBadGateway, "The console is only available for a VNC server on this machine.")
		return
	}
	conn, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close(websocket.StatusNormalClosure, "")
	proxyVNC(r.Context(), conn, addr)
}

func vncLoopback(host string) bool {
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

func proxyVNC(ctx context.Context, conn *websocket.Conn, addr string) {
	dialer := net.Dialer{Timeout: 5 * time.Second}
	raw, err := dialer.DialContext(ctx, "tcp", addr)
	if err != nil {
		return
	}
	defer raw.Close()
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	conn.SetReadLimit(8 << 20)

	go func() {
		defer cancel()
		buf := make([]byte, 32<<10)
		for {
			n, err := raw.Read(buf)
			if n > 0 {
				wctx, done := context.WithTimeout(context.Background(), 30*time.Second)
				werr := conn.Write(wctx, websocket.MessageBinary, buf[:n])
				done()
				if werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		_, data, err := conn.Read(ctx)
		if err != nil {
			return
		}
		if _, err := raw.Write(data); err != nil {
			return
		}
	}
}

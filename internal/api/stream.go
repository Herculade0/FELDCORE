package api

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"

	"github.com/Herculade0/feldcore/internal/system"
)

// writeTimeout bounds how long one frame may take to reach a client.
const writeTimeout = 5 * time.Second

// handleSystemStream replays the recent history window to a new client, then
// pushes each live sample as the sampler produces it.
func (s *Server) handleSystemStream(w http.ResponseWriter, r *http.Request) {
	// Accept enforces that the Origin header matches the Host, so another
	// site cannot open this stream with the visitor's session.
	c, err := websocket.Accept(w, r, nil)
	if err != nil {
		return
	}
	defer c.CloseNow()

	// We never expect client frames; CloseRead handles pings/close for us and
	// cancels ctx when the peer goes away.
	ctx := c.CloseRead(r.Context())

	history, live, cancel := s.sampler.Subscribe()
	defer cancel()

	for _, st := range history {
		if err := send(ctx, c, st); err != nil {
			return
		}
	}
	for {
		select {
		case <-ctx.Done():
			return
		case st := <-live:
			if err := send(ctx, c, st); err != nil {
				return
			}
		}
	}
}

func send(ctx context.Context, c *websocket.Conn, st *system.Stats) error {
	writeCtx, cancel := context.WithTimeout(ctx, writeTimeout)
	defer cancel()
	return wsjson.Write(writeCtx, c, st)
}

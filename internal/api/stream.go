package api

import (
	"context"
	"net/http"
	"time"

	"github.com/coder/websocket"
	"github.com/coder/websocket/wsjson"
)

// streamInterval is how often a live sample is pushed to the dashboard.
const streamInterval = 2 * time.Second

// handleSystemStream pushes a Stats sample over a WebSocket every streamInterval.
func (s *Server) handleSystemStream(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		// Dev server runs on a different origin (Vite); allow same-site use.
		InsecureSkipVerify: true,
	})
	if err != nil {
		return
	}
	defer c.CloseNow()

	ctx := r.Context()
	ticker := time.NewTicker(streamInterval)
	defer ticker.Stop()

	// Send one immediately so the UI paints without waiting a full interval.
	if err := s.push(ctx, c); err != nil {
		return
	}
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			if err := s.push(ctx, c); err != nil {
				return
			}
		}
	}
}

func (s *Server) push(ctx context.Context, c *websocket.Conn) error {
	st, err := s.sys.Sample(ctx)
	if err != nil {
		return err
	}
	writeCtx, cancel := context.WithTimeout(ctx, streamInterval)
	defer cancel()
	return wsjson.Write(writeCtx, c, st)
}

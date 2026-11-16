// Package term bridges a WebSocket to an interactive shell.
package term

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"os/exec"
	"sync"
	"time"

	"github.com/coder/websocket"
	"github.com/creack/pty"
)

// IdleTimeout closes a shell when the browser sends nothing for this long.
const IdleTimeout = 15 * time.Minute

// Shell is the host login shell: bash -l, or sh when bash is not installed.
func Shell() (string, []string) {
	if p, err := exec.LookPath("bash"); err == nil {
		return p, []string{"-l"}
	}
	if p, err := exec.LookPath("sh"); err == nil {
		return p, nil
	}
	return "/bin/sh", nil
}

// StartHost opens a PTY running the login shell.
// The cleanup function kills the shell and must be called when the socket closes.
func StartHost() (io.ReadWriteCloser, func(rows, cols uint16) error, func(), error) {
	path, args := Shell()
	cmd := exec.Command(path, args...)
	cmd.Env = append(os.Environ(), "TERM=xterm-256color")
	f, err := pty.Start(cmd)
	if err != nil {
		return nil, nil, nil, err
	}
	resize := func(rows, cols uint16) error {
		return pty.Setsize(f, &pty.Winsize{Rows: rows, Cols: cols})
	}
	cleanup := func() {
		_ = f.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}
	return f, resize, cleanup, nil
}

type control struct {
	Type string `json:"type"`
	Cols uint16 `json:"cols"`
	Rows uint16 `json:"rows"`
}

// Serve copies bytes between the socket and rw until either side closes or
// the browser is idle for IdleTimeout. Resize messages are JSON text frames.
// Terminal bytes are binary frames.
func Serve(ctx context.Context, conn *websocket.Conn, rw io.ReadWriteCloser, resize func(rows, cols uint16) error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()

	var writeMu sync.Mutex
	write := func(p []byte) {
		writeMu.Lock()
		defer writeMu.Unlock()
		wctx, done := context.WithTimeout(context.Background(), 5*time.Second)
		defer done()
		_ = conn.Write(wctx, websocket.MessageBinary, p)
	}

	go func() {
		buf := make([]byte, 32<<10)
		for {
			n, err := rw.Read(buf)
			if n > 0 {
				write(append([]byte(nil), buf[:n]...))
			}
			if err != nil {
				cancel()
				return
			}
		}
	}()

	timer := time.NewTimer(IdleTimeout)
	defer timer.Stop()
	msgs := make(chan readResult, 1)
	go func() {
		for {
			typ, data, err := conn.Read(ctx)
			select {
			case msgs <- readResult{typ, data, err}:
			case <-ctx.Done():
				return
			}
			if err != nil {
				return
			}
		}
	}()

	for {
		select {
		case <-ctx.Done():
			return
		case <-timer.C:
			write([]byte("\r\n\r\nSession closed after 15 minutes of inactivity.\r\n"))
			return
		case msg := <-msgs:
			if msg.err != nil {
				return
			}
			if !timer.Stop() {
				select {
				case <-timer.C:
				default:
				}
			}
			timer.Reset(IdleTimeout)
			if msg.typ == websocket.MessageText {
				var c control
				if json.Unmarshal(msg.data, &c) == nil && c.Type == "resize" && resize != nil && c.Cols > 0 && c.Cols <= 500 && c.Rows > 0 && c.Rows <= 500 {
					_ = resize(c.Rows, c.Cols)
				}
				continue
			}
			if _, err := rw.Write(msg.data); err != nil {
				return
			}
		}
	}
}

type readResult struct {
	typ  websocket.MessageType
	data []byte
	err  error
}

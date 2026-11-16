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
// The shell is bash -l when bash exists. A one-shot hook then colours a stock
// prompt (Campbell green/blue, red for root) without replacing a custom PS1.
func StartHost() (io.ReadWriteCloser, func(rows, cols uint16) error, func(), error) {
	return startHost(os.Environ())
}

func startHost(env []string) (io.ReadWriteCloser, func(rows, cols uint16) error, func(), error) {
	path, args := Shell()
	// The daemon's own PS1 is not the user's. Their prompt lives in the shell
	// startup files, which bash -l still reads.
	env = stripEnv(env, "PS1", "PROMPT_COMMAND", "FELDCORE_PREV_PROMPT", "FELDCORE_INIT")
	env = applyTermEnv(env)

	var extra []func()
	var cmd *exec.Cmd
	if bashLogin(args) {
		name, cleanupFile, err := writeTemp("feldcore-prompt-*.bash", hostHook())
		if err != nil {
			return nil, nil, nil, err
		}
		extra = append(extra, cleanupFile)
		env = append(env, "PROMPT_COMMAND="+". "+shellQuote(name))
		cmd = exec.Command(path, "-l")
	} else {
		name, cleanupFile, err := writeTemp("feldcore-sh-*.sh", shHook())
		if err != nil {
			return nil, nil, nil, err
		}
		extra = append(extra, cleanupFile)
		env = stripEnv(env, "ENV")
		env = append(env, "ENV="+name)
		cmd = exec.Command(path)
	}
	cmd.Env = env

	f, err := pty.Start(cmd)
	if err != nil {
		for _, fn := range extra {
			fn()
		}
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
		for _, fn := range extra {
			fn()
		}
	}
	return f, resize, cleanup, nil
}

func bashLogin(args []string) bool {
	return len(args) == 1 && args[0] == "-l"
}

func writeTemp(pattern, body string) (string, func(), error) {
	f, err := os.CreateTemp("", pattern)
	if err != nil {
		return "", nil, err
	}
	name := f.Name()
	cleanup := func() { _ = os.Remove(name) }
	if _, err := io.WriteString(f, body); err != nil {
		_ = f.Close()
		cleanup()
		return "", nil, err
	}
	if err := f.Close(); err != nil {
		cleanup()
		return "", nil, err
	}
	return name, cleanup, nil
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

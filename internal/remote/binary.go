package remote

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/appliance"
	"github.com/Herculade0/feldcore/internal/install"
	"github.com/Herculade0/feldcore/internal/secrets"
)

const (
	// BakedCloudflaredPath is the connector installed by the OS image build.
	BakedCloudflaredPath = "/usr/bin/cloudflared"
	// BakedCloudflaredVersion is the release pinned in os/cloudflared.sha256.
	BakedCloudflaredVersion = "2026.11.0"
)

// bakedCloudflared is the program FELDCORE OS runs. Tests point it at a stand-in.
var bakedCloudflared = BakedCloudflaredPath

// HostCloudflared supervises a cloudflared process. On FELDCORE OS that
// process is the baked binary. Elsewhere it is a binary downloaded into
// FELDCORE's data directory, used when the container is not.
//
// The supervisor follows the same shape as feldcore.service: start the
// program, restart it if it exits, and stop it when remote access is turned
// off. The token is never placed on the command line.
type HostCloudflared struct {
	Dir string
	// Bin, when set, is the only program Start will run. Tests use it.
	Bin string
	// TokenFile is a mode-0600 file passed with --token-file. When it is
	// empty, Start passes the token in TUNNEL_TOKEN instead.
	TokenFile string
	// Grace is how long a new process must stay up before Start reports
	// success. RestartAfter is the pause before it is started again.
	// Zero uses the defaults (400ms, 2s).
	Grace        time.Duration
	RestartAfter time.Duration

	mu     sync.Mutex
	opMu   sync.Mutex
	cmd    *exec.Cmd
	detail string
	last   string
	token  string
	cancel context.CancelFunc
	done   chan struct{}
	gen    int
	hookUp bool

	// Download and start replace the real download and process in tests.
	Download func(ctx context.Context, url, dest string) error
	start    func(bin, token string) error
}

// Available reports whether a cloudflared binary can be started.
func (h *HostCloudflared) Available() bool {
	if h == nil {
		return false
	}
	_, err := h.resolve()
	return err == nil
}

// Running reports whether this daemon's cloudflared is still running.
func (h *HostCloudflared) Running() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.hookUp || h.aliveLocked()
}

// Detail is the last connector error, with any token removed.
func (h *HostCloudflared) Detail() string {
	if h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.detail
}

// Start runs cloudflared and keeps it running until Stop. A process that is
// already up with the same token is left alone.
func (h *HostCloudflared) Start(ctx context.Context, token string) error {
	if h == nil {
		return fmt.Errorf("cloudflared is not installed")
	}
	h.opMu.Lock()
	defer h.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	same := token != "" && h.token == token && (h.aliveLocked() || h.hookUp)
	h.mu.Unlock()
	if same {
		return nil
	}
	h.halt()
	if err := ctx.Err(); err != nil {
		return err
	}
	bin, err := h.resolve()
	if err != nil {
		h.setDetail(err.Error())
		return err
	}
	if h.start != nil {
		return h.hookStart(bin, token)
	}

	h.mu.Lock()
	h.gen++
	gen := h.gen
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	h.cancel = cancel
	h.done = done
	h.token = token
	h.detail = ""
	h.last = ""
	h.mu.Unlock()

	ready := make(chan error, 1)
	go h.supervise(runCtx, bin, token, gen, ready, done)
	select {
	case err := <-ready:
		return err
	case <-ctx.Done():
		h.halt()
		return ctx.Err()
	}
}

// Stop ends the cloudflared this daemon started, and does not restart it.
func (h *HostCloudflared) Stop() error {
	if h == nil {
		return nil
	}
	h.opMu.Lock()
	defer h.opMu.Unlock()
	h.halt()
	return nil
}

// Install downloads the official cloudflared binary for this machine.
func (h *HostCloudflared) Install(ctx context.Context) error {
	url, err := CloudflaredURL(runtime.GOARCH)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(h.Dir, 0o755); err != nil {
		return err
	}
	dest := h.path()
	if h.Download != nil {
		if err := h.Download(ctx, url, dest); err != nil {
			return err
		}
	} else if err := download(ctx, url, dest); err != nil {
		return err
	}
	return os.Chmod(dest, 0o755)
}

func (h *HostCloudflared) supervise(ctx context.Context, bin, token string, gen int, ready chan<- error, done chan struct{}) {
	defer close(done)
	told := false
	tell := func(err error) {
		if told {
			return
		}
		told = true
		ready <- err
	}
	delay := time.Duration(0)
	for {
		if ctx.Err() != nil {
			tell(nil)
			return
		}
		if delay > 0 {
			timer := time.NewTimer(delay)
			select {
			case <-ctx.Done():
				timer.Stop()
				tell(nil)
				return
			case <-timer.C:
			}
		}
		err := h.runOnce(ctx, bin, token, gen, func() { tell(nil) })
		if ctx.Err() != nil {
			tell(nil)
			return
		}
		if !told {
			tell(err)
		}
		delay = h.restartAfter()
	}
}

// runOnce starts one process. onUp is called once the process has stayed up
// through the grace period. The returned error is set when that first start
// fails. A later exit returns nil so the supervisor can start it again.
func (h *HostCloudflared) runOnce(ctx context.Context, bin, token string, gen int, onUp func()) error {
	cmd, waited, err := h.spawn(bin, token)
	if err != nil {
		msg := friendlyExec(err, bin)
		h.setDetail(secrets.Redact(msg, token))
		return errors.New(h.Detail())
	}
	if !h.attach(gen, cmd) {
		_ = cmd.Process.Kill()
		<-waited
		return ctx.Err()
	}
	defer h.detach(gen, cmd)

	timer := time.NewTimer(h.grace())
	defer timer.Stop()
	select {
	case <-waited:
		return h.exitError(token)
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-waited
		return nil
	case <-timer.C:
		onUp()
		select {
		case <-waited:
			h.noteExit(token)
			return nil
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-waited
			return nil
		}
	}
}

func (h *HostCloudflared) spawn(bin, token string) (*exec.Cmd, <-chan struct{}, error) {
	args := tokenArgs(bin, h.TokenFile)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = envForConnector(os.Environ(), token, h.TokenFile)
	// A writer, not a pipe, so Wait copies every byte before it returns.
	// StdoutPipe's read end is closed inside Wait, which drops a fast exit's
	// error line. WaitDelay is left unset: a short delay would close the
	// copy while a healthy connector is still running.
	logs := &lineWriter{note: func(line string) { h.note(line, token) }}
	cmd.Stdout = logs
	cmd.Stderr = logs
	if err := cmd.Start(); err != nil {
		return nil, nil, err
	}
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		logs.flush()
		close(done)
	}()
	return cmd, done, nil
}

// lineWriter keeps the last log lines from cloudflared. Stdout and stderr
// are copied concurrently, so Write is safe for both.
type lineWriter struct {
	mu   sync.Mutex
	buf  []byte
	note func(string)
}

func (w *lineWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf = append(w.buf, p...)
	for {
		i := bytes.IndexByte(w.buf, '\n')
		if i < 0 {
			if len(w.buf) > 8192 {
				w.emit(string(w.buf[:300]))
				w.buf = w.buf[:0]
			}
			break
		}
		w.emit(string(w.buf[:i]))
		w.buf = w.buf[i+1:]
	}
	return len(p), nil
}

func (w *lineWriter) flush() {
	w.mu.Lock()
	defer w.mu.Unlock()
	if strings.TrimSpace(string(w.buf)) != "" {
		w.emit(string(w.buf))
	}
	w.buf = nil
}

func (w *lineWriter) emit(line string) {
	if w.note != nil {
		w.note(strings.TrimRight(line, "\r"))
	}
}

func (h *HostCloudflared) note(line, token string) {
	line = strings.TrimSpace(secrets.Redact(line, token))
	if line == "" {
		return
	}
	if len(line) > 300 {
		line = line[:300]
	}
	h.mu.Lock()
	h.last = line
	if looksBad(line) {
		h.detail = line
	}
	h.mu.Unlock()
}

func (h *HostCloudflared) exitError(token string) error {
	msg := h.Detail()
	if msg == "" {
		msg = h.lastLine()
	}
	if msg == "" {
		msg = "cloudflared exited"
	}
	msg = secrets.Redact(msg, token)
	h.setDetail(msg)
	return errors.New(msg)
}

func (h *HostCloudflared) noteExit(token string) {
	msg := h.lastLine()
	if msg == "" {
		msg = "cloudflared exited"
	}
	h.setDetail(secrets.Redact(msg, token))
}

func (h *HostCloudflared) hookStart(bin, token string) error {
	if err := h.start(bin, token); err != nil {
		h.setDetail(secrets.Redact(err.Error(), token))
		return err
	}
	h.mu.Lock()
	h.hookUp = true
	h.token = token
	h.detail = ""
	h.mu.Unlock()
	return nil
}

// halt stops the current process and supervisor. It does not take opMu.
func (h *HostCloudflared) halt() {
	h.mu.Lock()
	cancel := h.cancel
	done := h.done
	cmd := h.cmd
	h.cancel = nil
	h.done = nil
	h.cmd = nil
	h.token = ""
	h.hookUp = false
	h.gen++
	h.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
	if done != nil {
		select {
		case <-done:
		case <-time.After(3 * time.Second):
		}
	}
}

func (h *HostCloudflared) attach(gen int, cmd *exec.Cmd) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gen != gen {
		return false
	}
	h.cmd = cmd
	return true
}

func (h *HostCloudflared) detach(gen int, cmd *exec.Cmd) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gen == gen && h.cmd == cmd {
		h.cmd = nil
	}
}

func (h *HostCloudflared) aliveLocked() bool {
	return h.cmd != nil && h.cmd.Process != nil && h.cmd.ProcessState == nil
}

func (h *HostCloudflared) setDetail(msg string) {
	h.mu.Lock()
	h.detail = msg
	h.mu.Unlock()
}

func (h *HostCloudflared) lastLine() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

func (h *HostCloudflared) resolve() (string, error) {
	if h.Bin != "" {
		if !executable(h.Bin) {
			return "", fmt.Errorf("cloudflared is not installed at %s", h.Bin)
		}
		return h.Bin, nil
	}
	// FELDCORE OS runs only the baked program. A copy under the data
	// directory must not take its place, and a missing file is not a reason
	// to start a container.
	if appliance.Is() || executable(bakedCloudflared) {
		if !executable(bakedCloudflared) {
			return "", fmt.Errorf("cloudflared is not installed at %s", bakedCloudflared)
		}
		return bakedCloudflared, nil
	}
	if executable(h.path()) {
		return h.path(), nil
	}
	if found, err := exec.LookPath("cloudflared"); err == nil {
		return found, nil
	}
	return "", fmt.Errorf("cloudflared is not installed")
}

func (h *HostCloudflared) grace() time.Duration {
	if h != nil && h.Grace > 0 {
		return h.Grace
	}
	return 400 * time.Millisecond
}

func (h *HostCloudflared) restartAfter() time.Duration {
	if h != nil && h.RestartAfter > 0 {
		return h.RestartAfter
	}
	return 2 * time.Second
}

func (h *HostCloudflared) path() string {
	return filepath.Join(h.Dir, "cloudflared")
}

func looksBad(line string) bool {
	l := strings.ToLower(line)
	return strings.Contains(l, "error") || strings.Contains(l, "fail") || strings.Contains(l, "unable") || strings.Contains(l, "denied")
}

func friendlyExec(err error, bin string) string {
	if err == nil {
		return ""
	}
	var ee *exec.Error
	if errors.As(err, &ee) || strings.Contains(err.Error(), "executable file not found") || strings.Contains(err.Error(), "exec:") {
		return "cloudflared could not be started at " + bin
	}
	return "cloudflared did not start"
}

func executable(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

func envForConnector(base []string, token, tokenFile string) []string {
	// cloudflared prefers TUNNEL_TOKEN over --token-file, so a file-based
	// start strips both variables. The token stays in the mode-0600 file.
	env := dropEnv(base, "TUNNEL_TOKEN", "TUNNEL_TOKEN_FILE")
	if tokenFile == "" && token != "" {
		env = append(env, "TUNNEL_TOKEN="+token)
	}
	return env
}

func dropEnv(base []string, keys ...string) []string {
	skip := make(map[string]bool, len(keys))
	for _, k := range keys {
		skip[k] = true
	}
	out := make([]string, 0, len(base))
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if skip[k] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// byteProgress reports download size on the install job's log, a few
// megabytes at a time. With no job hooked up it only copies.
type byteProgress struct {
	ctx  context.Context
	r    io.Reader
	n    int64
	mark int64
}

func (b *byteProgress) Read(p []byte) (int, error) {
	n, err := b.r.Read(p)
	b.n += int64(n)
	if b.n-b.mark >= 4<<20 {
		b.mark = b.n
		install.Line(b.ctx, "Downloaded "+itoaMB(b.n)+" MB")
	}
	return n, err
}

func itoaMB(n int64) string {
	return fmt.Sprintf("%d", n>>20)
}

// CloudflaredURL is the official release for a GOARCH.
func CloudflaredURL(arch string) (string, error) {
	switch arch {
	case "amd64", "arm64", "arm", "386":
		return "https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-" + arch, nil
	default:
		return "", fmt.Errorf("there is no cloudflared build for %s", arch)
	}
}

func download(ctx context.Context, url, dest string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return fmt.Errorf("could not download cloudflared")
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("could not download cloudflared (HTTP %d)", resp.StatusCode)
	}
	tmp, err := os.CreateTemp(filepath.Dir(dest), ".cloudflared-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := io.Copy(tmp, &byteProgress{ctx: ctx, r: resp.Body}); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), dest)
}

// commandArgs is the argv used to start cloudflared, without a token file.
// It deliberately has no token.
func commandArgs(bin string) []string {
	return []string{bin, "--no-autoupdate", "tunnel", "run"}
}

func tokenArgs(bin, tokenFile string) []string {
	args := commandArgs(bin)
	if tokenFile != "" {
		args = append(args, "--token-file", tokenFile)
	}
	return args
}

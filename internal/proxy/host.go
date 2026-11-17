package proxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/appliance"
	"github.com/Herculade0/feldcore/internal/secrets"
)

const (
	// BakedCaddyPath is the proxy installed by the OS image build.
	BakedCaddyPath = "/usr/bin/caddy"
	// BakedCaddyVersion is the release pinned in os/caddy.sha256.
	BakedCaddyVersion = "2.11.9"
	// DNSModule is the Cloudflare DNS-01 provider the OS build compiles in.
	DNSModule = "dns.providers.cloudflare"
)

// bakedCaddy is the program FELDCORE OS runs. Tests point it at a stand-in.
var bakedCaddy = BakedCaddyPath

// HostCaddy supervises the Caddy process. On FELDCORE OS that process is the
// baked binary. Its config and certificate data stay under FELDCORE's data
// directory. The Cloudflare API token, when a public certificate needs it,
// is an environment variable, never an argument.
//
// The supervisor follows the same shape as the tunnel connector: start the
// program, restart it if it exits, and stop it when nothing is published.
type HostCaddy struct {
	// Bin, when set, is the only program Start will run. Tests use it.
	Bin string
	// Grace is how long a new process must stay up before Start reports
	// success. RestartAfter is the pause before it is started again.
	// Zero uses the defaults (400ms, 2s).
	Grace        time.Duration
	RestartAfter time.Duration

	mu         sync.Mutex
	opMu       sync.Mutex
	cmd        *exec.Cmd
	detail     string
	last       string
	stamp      string
	token      string
	cancel     context.CancelFunc
	done       chan struct{}
	gen        int
	modules    string
	modulesBin string
}

// runSpec is one Caddy process. Stamp identifies the config. The same stamp
// leaves a running process alone. Token may be empty.
type runSpec struct {
	Config    string
	DataDir   string
	ConfigDir string
	Home      string
	Token     string
	Stamp     string
}

// Available reports whether the baked Caddy program can be started.
func (h *HostCaddy) Available() bool {
	if h == nil {
		return false
	}
	_, err := h.resolve()
	return err == nil
}

// Running reports whether this daemon's Caddy is still running.
func (h *HostCaddy) Running() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.aliveLocked()
}

// Detail is the last proxy error, with any token removed.
func (h *HostCaddy) Detail() string {
	if h == nil {
		return ""
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.detail
}

// HasModule reports whether the binary includes a Caddy module id.
func (h *HostCaddy) HasModule(ctx context.Context, id string) (bool, error) {
	if h == nil {
		return false, fmt.Errorf("caddy is not installed")
	}
	bin, err := h.resolve()
	if err != nil {
		return false, err
	}
	h.mu.Lock()
	if h.modulesBin == bin && h.modules != "" {
		ok := strings.Contains(h.modules, id)
		h.mu.Unlock()
		return ok, nil
	}
	h.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(cctx, bin, "list-modules")
	out, err := cmd.Output()
	if err != nil {
		if cctx.Err() != nil {
			return false, errors.New("caddy did not report its modules")
		}
		return false, errors.New(friendlyExec(err, bin))
	}
	text := string(out)
	h.mu.Lock()
	h.modules = text
	h.modulesBin = bin
	h.mu.Unlock()
	return strings.Contains(text, id), nil
}

// Start runs Caddy and keeps it running until Stop. A process that is already
// up with the same config is left alone.
func (h *HostCaddy) Start(ctx context.Context, spec runSpec) error {
	if h == nil {
		return fmt.Errorf("caddy is not installed")
	}
	h.opMu.Lock()
	defer h.opMu.Unlock()
	if err := ctx.Err(); err != nil {
		return err
	}
	h.mu.Lock()
	same := spec.Stamp != "" && h.stamp == spec.Stamp && h.aliveLocked()
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
	h.mu.Lock()
	h.gen++
	gen := h.gen
	runCtx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	h.cancel = cancel
	h.done = done
	h.stamp = spec.Stamp
	h.token = spec.Token
	h.detail = ""
	h.last = ""
	h.mu.Unlock()

	ready := make(chan error, 1)
	go h.supervise(runCtx, bin, spec, gen, ready, done)
	select {
	case err := <-ready:
		return err
	case <-ctx.Done():
		h.halt()
		return ctx.Err()
	}
}

// Stop ends the Caddy this daemon started, and does not restart it.
func (h *HostCaddy) Stop() error {
	if h == nil {
		return nil
	}
	h.opMu.Lock()
	defer h.opMu.Unlock()
	h.halt()
	return nil
}

func (h *HostCaddy) supervise(ctx context.Context, bin string, spec runSpec, gen int, ready chan<- error, done chan struct{}) {
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
		err := h.runOnce(ctx, bin, spec, gen, func() { tell(nil) })
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
func (h *HostCaddy) runOnce(ctx context.Context, bin string, spec runSpec, gen int, onUp func()) error {
	cmd, waited, err := h.spawn(bin, spec)
	if err != nil {
		msg := friendlyExec(err, bin)
		h.setDetail(secrets.Redact(msg, spec.Token))
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
		return h.exitError(spec.Token)
	case <-ctx.Done():
		_ = cmd.Process.Kill()
		<-waited
		return nil
	case <-timer.C:
		onUp()
		select {
		case <-waited:
			h.noteExit(spec.Token)
			return nil
		case <-ctx.Done():
			_ = cmd.Process.Kill()
			<-waited
			return nil
		}
	}
}

func (h *HostCaddy) spawn(bin string, spec runSpec) (*exec.Cmd, <-chan struct{}, error) {
	args := caddyArgs(bin, spec.Config)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = spec.Home
	cmd.Env = envForCaddy(os.Environ(), spec.DataDir, spec.ConfigDir, spec.Home, spec.Token)
	logs := &lineWriter{note: func(line string) { h.note(line, spec.Token) }}
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

func (h *HostCaddy) note(line, token string) {
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

func (h *HostCaddy) exitError(token string) error {
	msg := h.Detail()
	if msg == "" {
		msg = h.lastLine()
	}
	if msg == "" {
		msg = "caddy exited"
	}
	msg = secrets.Redact(msg, token)
	h.setDetail(msg)
	return errors.New(msg)
}

func (h *HostCaddy) noteExit(token string) {
	msg := h.lastLine()
	if msg == "" {
		msg = "caddy exited"
	}
	h.setDetail(secrets.Redact(msg, token))
}

// halt stops the current process and supervisor. It does not take opMu.
func (h *HostCaddy) halt() {
	h.mu.Lock()
	cancel := h.cancel
	done := h.done
	cmd := h.cmd
	h.cancel = nil
	h.done = nil
	h.cmd = nil
	h.stamp = ""
	h.token = ""
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

func (h *HostCaddy) attach(gen int, cmd *exec.Cmd) bool {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gen != gen {
		return false
	}
	h.cmd = cmd
	return true
}

func (h *HostCaddy) detach(gen int, cmd *exec.Cmd) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.gen == gen && h.cmd == cmd {
		h.cmd = nil
	}
}

func (h *HostCaddy) aliveLocked() bool {
	return h.cmd != nil && h.cmd.Process != nil && h.cmd.ProcessState == nil
}

func (h *HostCaddy) setDetail(msg string) {
	h.mu.Lock()
	h.detail = msg
	h.mu.Unlock()
}

func (h *HostCaddy) lastLine() string {
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.last
}

func (h *HostCaddy) resolve() (string, error) {
	if h != nil && h.Bin != "" {
		if !executable(h.Bin) {
			return "", fmt.Errorf("caddy is not installed at %s", h.Bin)
		}
		return h.Bin, nil
	}
	// FELDCORE OS runs only the baked program. A copy elsewhere must not
	// take its place, and a missing file is not a reason to start a container.
	if appliance.Is() || executable(bakedCaddy) {
		if !executable(bakedCaddy) {
			return "", fmt.Errorf("caddy is not installed at %s", bakedCaddy)
		}
		return bakedCaddy, nil
	}
	return "", fmt.Errorf("caddy is not installed")
}

func (h *HostCaddy) grace() time.Duration {
	if h != nil && h.Grace > 0 {
		return h.Grace
	}
	return 400 * time.Millisecond
}

func (h *HostCaddy) restartAfter() time.Duration {
	if h != nil && h.RestartAfter > 0 {
		return h.RestartAfter
	}
	return 2 * time.Second
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
		return "Caddy could not be started at " + bin
	}
	return "caddy did not start"
}

func executable(path string) bool {
	if path == "" {
		return false
	}
	st, err := os.Stat(path)
	return err == nil && !st.IsDir() && st.Mode()&0o111 != 0
}

func envForCaddy(base []string, dataDir, configDir, home, token string) []string {
	env := dropEnv(base, "CF_API_TOKEN", "XDG_DATA_HOME", "XDG_CONFIG_HOME", "HOME")
	env = append(env,
		"XDG_DATA_HOME="+dataDir,
		"XDG_CONFIG_HOME="+configDir,
		"HOME="+home,
	)
	if token != "" {
		env = append(env, "CF_API_TOKEN="+token)
	}
	return env
}

func dropEnv(base []string, keys ...string) []string {
	skip := make(map[string]bool, len(keys))
	for _, k := range keys {
		skip[k] = true
	}
	out := make([]string, 0, len(base)+4)
	for _, e := range base {
		k, _, _ := strings.Cut(e, "=")
		if skip[k] {
			continue
		}
		out = append(out, e)
	}
	return out
}

// caddyArgs is the argv used to start Caddy. It has no token and no shell.
func caddyArgs(bin, config string) []string {
	return []string{bin, "run", "--config", config, "--adapter", "caddyfile"}
}

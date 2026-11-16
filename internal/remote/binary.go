package remote

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/Herculade0/feldcore/internal/install"
	"github.com/Herculade0/feldcore/internal/secrets"
)

// HostCloudflared is cloudflared installed under FELDCORE's data directory,
// used only when Docker is not running a connector container.
type HostCloudflared struct {
	Dir string

	mu     sync.Mutex
	cmd    *exec.Cmd
	detail string

	// Download and start replace the real download and process in tests.
	Download func(ctx context.Context, url, dest string) error
	start    func(bin, token string) error
}

// Available reports whether a cloudflared binary can be started.
func (h *HostCloudflared) Available() bool {
	if h == nil {
		return false
	}
	if _, err := os.Stat(h.path()); err == nil {
		return true
	}
	_, err := exec.LookPath("cloudflared")
	return err == nil
}

// Running reports whether this daemon started cloudflared and it is still running.
func (h *HostCloudflared) Running() bool {
	if h == nil {
		return false
	}
	h.mu.Lock()
	defer h.mu.Unlock()
	return h.cmd != nil && h.cmd.Process != nil && h.cmd.ProcessState == nil
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

// Start runs cloudflared. The token is passed in the environment, not argv.
func (h *HostCloudflared) Start(ctx context.Context, token string) error {
	if h.Running() {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	bin := h.path()
	if _, err := os.Stat(bin); err != nil {
		if found, err := exec.LookPath("cloudflared"); err == nil {
			bin = found
		} else {
			return fmt.Errorf("cloudflared is not installed")
		}
	}
	if h.start != nil {
		return h.start(bin, token)
	}
	args := commandArgs(bin)
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Env = append(os.Environ(), "TUNNEL_TOKEN="+token)
	cmd.Stdout = io.Discard
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return err
	}
	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cloudflared did not start")
	}
	h.mu.Lock()
	h.cmd = cmd
	h.detail = ""
	h.mu.Unlock()
	go func() {
		buf := make([]byte, 512)
		n, _ := io.ReadFull(stderr, buf)
		line := secrets.Redact(string(buf[:n]), token)
		h.mu.Lock()
		h.detail = line
		h.mu.Unlock()
		_ = cmd.Wait()
	}()
	return nil
}

// Stop ends a cloudflared this daemon started.
func (h *HostCloudflared) Stop() error {
	h.mu.Lock()
	cmd := h.cmd
	h.cmd = nil
	h.mu.Unlock()
	if cmd == nil || cmd.Process == nil {
		return nil
	}
	_ = cmd.Process.Kill()
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

func (h *HostCloudflared) path() string {
	return filepath.Join(h.Dir, "cloudflared")
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

// commandArgs is the argv used to start cloudflared. It deliberately has no token.
func commandArgs(bin string) []string {
	return []string{bin, "--no-autoupdate", "tunnel", "run"}
}

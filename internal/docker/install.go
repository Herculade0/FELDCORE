package docker

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/install"
)

// OpError is an operational failure whose message is safe to show the user.
type OpError struct{ Msg string }

func (e *OpError) Error() string { return e.Msg }

// Install installs Docker with apt and starts the daemon.
//
// It installs Debian/Ubuntu's `docker.io` package rather than Docker's own
// `docker-ce`: it needs no third-party apt repository, which keeps a one-click
// install dependable. Users who want Docker's official build can install it
// themselves and FELDCORE will use it.
func (m *Manager) Install(ctx context.Context) error {
	if os.Geteuid() != 0 {
		return &OpError{"FELDCORE must run as root to install Docker."}
	}
	if _, ok := lookBin("apt-get"); !ok {
		return &OpError{"Automatic install needs apt. Install Docker with your system's package manager, then reload this page."}
	}

	ctx = context.WithoutCancel(ctx)
	if m.log != nil {
		m.log.Info("installing docker.io")
	}
	if _, err := run(ctx, 5*time.Minute, "apt-get", "update"); err != nil && m.log != nil {
		m.log.Warn("apt-get update failed; trying the install anyway", "err", err)
	}
	if _, err := run(ctx, 15*time.Minute, "apt-get", "install", "-y", "docker.io"); err != nil {
		msg := err.Error()
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "the install timed out"
		}
		return &OpError{fmt.Sprintf("Could not install Docker: %s", msg)}
	}
	// enable --now both enables at boot and starts it immediately.
	if _, err := run(ctx, 60*time.Second, "systemctl", "enable", "--now", "docker"); err != nil {
		return &OpError{fmt.Sprintf("Docker was installed but would not start: %v", err)}
	}

	m.mu.Lock()
	m.cachedAt = time.Time{} // force the next Status to re-check
	m.mu.Unlock()

	// Give the daemon a moment to open its socket before the UI refreshes.
	for i := 0; i < 20; i++ {
		if m.Status(ctx).Running {
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	if m.log != nil {
		m.log.Info("docker installed")
	}
	return nil
}

// run executes a system command with a timeout and returns trimmed output.
func run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	path, ok := lookBin(name)
	if !ok {
		return "", fmt.Errorf("%s is not installed", name)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	if install.WantsLines(ctx) {
		text, err := install.Run(ctx, timeout, name, args...)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return text, context.DeadlineExceeded
			}
			if tail := lastLine(text); tail != "" {
				return text, fmt.Errorf("%s", tail)
			}
			return text, err
		}
		return text, nil
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if ctx.Err() == context.DeadlineExceeded {
			return text, context.DeadlineExceeded
		}
		if tail := lastLine(text); tail != "" {
			return text, fmt.Errorf("%s", tail)
		}
		return text, err
	}
	return text, nil
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			return l
		}
	}
	return ""
}

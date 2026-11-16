package vm

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
)

// Runner runs a host command. Tests substitute a fake.
type Runner interface {
	Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error)
	Has(name string) bool
}

type execRunner struct{}

func (execRunner) Has(name string) bool {
	_, ok := lookBin(name)
	return ok
}

func (execRunner) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	path, ok := lookBin(name)
	if !ok {
		return "", fmt.Errorf("%s is not installed", name)
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return text, fmt.Errorf("%s timed out", name)
		}
		if text != "" {
			return text, fmt.Errorf("%s", lastLine(text))
		}
		return text, fmt.Errorf("%s failed", name)
	}
	return text, nil
}

func lookBin(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, dir := range []string{"/usr/sbin", "/sbin", "/usr/bin", "/bin", "/usr/local/sbin", "/usr/local/bin", "/usr/libexec", "/usr/lib/qemu"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

func lastLine(s string) string {
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0; i-- {
		l := strings.TrimSpace(lines[i])
		if l == "" {
			continue
		}
		l = strings.TrimPrefix(l, "error: ")
		return l
	}
	return s
}

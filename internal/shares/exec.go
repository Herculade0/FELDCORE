package shares

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/install"
)

// Admin tools live in sbin directories that are not always on PATH.
var searchDirs = []string{
	"/usr/sbin", "/sbin", "/usr/bin", "/bin", "/usr/local/sbin", "/usr/local/bin",
}

// findBin locates an executable by name.
func findBin(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, dir := range searchDirs {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

// run executes a system command with a timeout, feeding stdin if given, and
// returns its combined output. Arguments are passed directly (no shell).
func run(ctx context.Context, timeout time.Duration, stdin, name string, args ...string) (string, error) {
	path, ok := findBin(name)
	if !ok {
		return "", fmt.Errorf("%s is not installed", name)
	}
	if stdin == "" && install.WantsLines(ctx) {
		text, err := install.Run(ctx, timeout, name, args...)
		if err != nil {
			if errors.Is(err, context.DeadlineExceeded) {
				return text, fmt.Errorf("%s timed out after %s", name, timeout)
			}
			if tail := lastLines(text, 3); tail != "" {
				return text, fmt.Errorf("%s failed: %s", name, tail)
			}
			return text, fmt.Errorf("%s failed: %w", name, err)
		}
		return text, nil
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive")
	if stdin != "" {
		cmd.Stdin = strings.NewReader(stdin)
	}
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if errors.Is(ctx.Err(), context.DeadlineExceeded) {
			return text, fmt.Errorf("%s timed out after %s", name, timeout)
		}
		if tail := lastLines(text, 3); tail != "" {
			return text, fmt.Errorf("%s failed: %s", name, tail)
		}
		return text, fmt.Errorf("%s failed: %w", name, err)
	}
	return text, nil
}

// lastLines returns the final n non-empty lines of s, joined with "; ".
func lastLines(s string, n int) string {
	var keep []string
	lines := strings.Split(s, "\n")
	for i := len(lines) - 1; i >= 0 && len(keep) < n; i-- {
		if l := strings.TrimSpace(lines[i]); l != "" {
			keep = append([]string{l}, keep...)
		}
	}
	return strings.Join(keep, "; ")
}

// writeAtomic replaces path with data via a temp file and rename.
func writeAtomic(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	tmp, err := os.CreateTemp(filepath.Dir(path), ".feldcore-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(perm); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(tmp.Name(), path)
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

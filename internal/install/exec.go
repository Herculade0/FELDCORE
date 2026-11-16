package install

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type stepKey struct{}
type lineKey struct{}

// WithHooks attaches step and log callbacks. Run and any install task that
// calls Line report through them. A context copied with context.WithoutCancel
// keeps the hooks, which is what the install tasks do so a closed browser
// request does not drop the log.
func WithHooks(ctx context.Context, step, line func(string)) context.Context {
	if step != nil {
		ctx = context.WithValue(ctx, stepKey{}, step)
	}
	if line != nil {
		ctx = context.WithValue(ctx, lineKey{}, line)
	}
	return ctx
}

// WantsLines reports whether ctx carries a log callback.
func WantsLines(ctx context.Context) bool {
	fn, ok := ctx.Value(lineKey{}).(func(string))
	return ok && fn != nil
}

// Line sends one log line when ctx has a hook, and does nothing otherwise.
func Line(ctx context.Context, text string) {
	fn, ok := ctx.Value(lineKey{}).(func(string))
	if ok && fn != nil {
		fn(text)
	}
}

func step(ctx context.Context, name string) {
	fn, ok := ctx.Value(stepKey{}).(func(string))
	if ok && fn != nil && strings.TrimSpace(name) != "" {
		fn(name)
	}
}

// Run executes a command and, when ctx has a log hook, streams each line of
// output as it is written. timeout is the cap for this one command. The
// returned text is the combined output, matching the callers that still show
// the tail in an error.
func Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	path, err := look(name)
	if err != nil {
		return "", err
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C", "DEBIAN_FRONTEND=noninteractive", "TERM=dumb")
	var onLine func(string)
	if fn, ok := ctx.Value(lineKey{}).(func(string)); ok {
		onLine = fn
	}
	if onLine != nil {
		if label := stepFor(name, args); label != "" {
			step(ctx, label)
		}
		onLine("$ " + strings.TrimSpace(name+" "+strings.Join(args, " ")))
	}
	text, err := combined(cmd, onLine)
	if err != nil && errors.Is(ctx.Err(), context.DeadlineExceeded) {
		return text, context.DeadlineExceeded
	}
	return text, err
}

func stepFor(name string, args []string) string {
	switch filepath.Base(name) {
	case "apt-get", "apt":
		if len(args) == 0 {
			return ""
		}
		switch args[0] {
		case "update":
			return "Updating package lists"
		case "install", "upgrade":
			return "Installing packages"
		}
	case "dpkg":
		return "Installing packages"
	case "systemctl":
		return "Starting the service"
	}
	return ""
}

func look(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, dir := range []string{"/usr/sbin", "/sbin", "/usr/bin", "/bin", "/usr/local/sbin", "/usr/local/bin"} {
		p := filepath.Join(dir, name)
		if st, err := os.Stat(p); err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", fmt.Errorf("%s is not installed", name)
}

// combined reads stdout and stderr as one stream. Apt and dpkg share a pipe
// so a progress line is not stuck behind the other descriptor, and a carriage
// return starts a new log line the way a terminal would overwrite one.
func combined(cmd *exec.Cmd, onLine func(string)) (string, error) {
	pr, pw := io.Pipe()
	cmd.Stdout = pw
	cmd.Stderr = pw
	if err := cmd.Start(); err != nil {
		_ = pw.Close()
		return "", err
	}
	var wg sync.WaitGroup
	var waitErr error
	wg.Add(1)
	go func() {
		defer wg.Done()
		waitErr = cmd.Wait()
		_ = pw.Close()
	}()
	text := readLines(pr, onLine)
	wg.Wait()
	return text, waitErr
}

func readLines(r io.Reader, onLine func(string)) string {
	br := bufio.NewReaderSize(r, 32*1024)
	var all strings.Builder
	var cur []byte
	flush := func() {
		text := cleanLine(string(cur))
		cur = cur[:0]
		if text == "" {
			return
		}
		all.WriteString(text)
		all.WriteByte('\n')
		if onLine != nil {
			onLine(text)
		}
	}
	for {
		b, err := br.ReadByte()
		if err != nil {
			break
		}
		if b == '\n' || b == '\r' {
			flush()
			continue
		}
		if len(cur) < 8192 {
			cur = append(cur, b)
		}
	}
	flush()
	return strings.TrimSpace(all.String())
}

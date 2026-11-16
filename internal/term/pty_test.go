package term

import (
	"bytes"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/creack/pty"
)

type deadlineReader interface {
	io.ReadWriter
	SetReadDeadline(time.Time) error
}

func readUntil(t *testing.T, r deadlineReader, timeout time.Duration, done func([]byte) bool) []byte {
	t.Helper()
	_ = r.SetReadDeadline(time.Now().Add(timeout))
	defer r.SetReadDeadline(time.Time{})
	var buf bytes.Buffer
	tmp := make([]byte, 4096)
	for {
		n, err := r.Read(tmp)
		if n > 0 {
			buf.Write(tmp[:n])
			if done(buf.Bytes()) {
				return buf.Bytes()
			}
		}
		if err != nil {
			return buf.Bytes()
		}
	}
}

func testEnv(home string) []string {
	return []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"USER=ubuntu",
		"LOGNAME=ubuntu",
		"SHELL=/bin/bash",
		"LANG=C.UTF-8",
		// A prompt inherited from the daemon must not become the user's prompt.
		"PS1=leak> ",
		"PROMPT_COMMAND=echo leaked",
		"TERM=dumb",
		"COLORTERM=",
	}
}

func hush(t *testing.T, home string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(home, ".hushlogin"), nil, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestHostShellColoredPromptAndTools(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip(err)
	}
	home := t.TempDir()
	hush(t, home)
	rw, _, cleanup, err := startHost(testEnv(home))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	f := rw.(deadlineReader)

	// Wait until the prompt is drawn, then ask the shell what it is running with.
	greet := readUntil(t, f, 6*time.Second, func(b []byte) bool {
		return bytes.Contains(b, []byte("38;2;"+promptGreen))
	})
	if !bytes.Contains(greet, []byte("38;2;"+promptGreen)) || !bytes.Contains(greet, []byte("38;2;"+promptBlue)) {
		t.Fatalf("stock prompt was not Campbell green/blue:\n%s", greet)
	}
	if bytes.Contains(greet, []byte("leak>")) || bytes.Contains(greet, []byte("leaked")) {
		t.Fatalf("daemon prompt leaked into the shell:\n%s", greet)
	}

	_, err = io.WriteString(f, "printf 'TERM=%s COLORTERM=%s\\n' \"$TERM\" \"$COLORTERM\"\nshopt -q login_shell && echo LOGIN_OK\nls --color=auto /\nprintf 'hello\\n' | grep --color=auto hello\necho MARK_DONE\n")
	if err != nil {
		t.Fatal(err)
	}
	out := readUntil(t, f, 6*time.Second, func(b []byte) bool {
		return bytes.Contains(b, []byte("MARK_DONE"))
	})
	text := string(out)
	if !stringsContainsAll(text, "TERM=xterm-256color", "COLORTERM=truecolor", "LOGIN_OK", "MARK_DONE") {
		t.Fatalf("shell report:\n%s", text)
	}
	// GNU ls paints directories bold blue; grep paints the match bold red.
	if !bytes.Contains(out, []byte("\x1b[01;34m")) && !bytes.Contains(out, []byte("\x1b[1;34m")) {
		t.Fatalf("ls --color produced no directory colour:\n%s", text)
	}
	if !bytes.Contains(out, []byte("\x1b[01;31m")) && !bytes.Contains(out, []byte("\x1b[1;31m")) {
		t.Fatalf("grep --color produced no match colour:\n%s", text)
	}
}

func TestHostShellKeepsCustomPS1(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip(err)
	}
	home := t.TempDir()
	hush(t, home)
	bashrc := "case $- in *i*) ;; *) return;; esac\nPS1='custom> '\n"
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(bashrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(". \"$HOME/.bashrc\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rw, _, cleanup, err := startHost(testEnv(home))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	out := readUntil(t, rw.(deadlineReader), 6*time.Second, func(b []byte) bool {
		return bytes.Contains(b, []byte("custom>"))
	})
	if !bytes.Contains(out, []byte("custom>")) {
		t.Fatalf("custom prompt missing:\n%s", out)
	}
	if bytes.Contains(out, []byte("38;2;"+promptGreen)) {
		t.Fatalf("custom prompt was replaced:\n%s", out)
	}
}

func TestHostShellReplacesDebianDefaultColorPrompt(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip(err)
	}
	home := t.TempDir()
	hush(t, home)
	bashrc := `
case $- in *i*) ;; *) return;; esac
PS1='${debian_chroot:+($debian_chroot)}\[\033[01;32m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\$ '
case "$TERM" in
xterm*|rxvt*) PS1="\[\e]0;${debian_chroot:+($debian_chroot)}\u@\h: \w\a\]$PS1" ;;
esac
`
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte(bashrc), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(home, ".profile"), []byte(". \"$HOME/.bashrc\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	rw, _, cleanup, err := startHost(testEnv(home))
	if err != nil {
		t.Fatal(err)
	}
	defer cleanup()
	out := readUntil(t, rw.(deadlineReader), 6*time.Second, func(b []byte) bool {
		return bytes.Contains(b, []byte("38;2;"+promptGreen))
	})
	if !bytes.Contains(out, []byte("38;2;"+promptGreen)) || !bytes.Contains(out, []byte("38;2;"+promptBlue)) {
		t.Fatalf("Debian's stock coloured prompt was not replaced:\n%s", out)
	}
}

func TestContainerLaunchColoredPrompt(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip(err)
	}
	home := t.TempDir()
	hush(t, home)
	cmd := exec.Command("sh", "-c", ContainerCommand())
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"USER=ubuntu",
		"LOGNAME=ubuntu",
		"LANG=C.UTF-8",
	}, ContainerEnv()...)
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = f.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()
	out := readUntil(t, f, 6*time.Second, func(b []byte) bool {
		return bytes.Contains(b, []byte("38;2;"+promptGreen))
	})
	if !bytes.Contains(out, []byte("38;2;"+promptGreen)) || !bytes.Contains(out, []byte("38;2;"+promptBlue)) {
		t.Fatalf("container prompt:\n%s", out)
	}
}

func TestContainerLaunchKeepsCustomPS1(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip(err)
	}
	home := t.TempDir()
	hush(t, home)
	if err := os.WriteFile(filepath.Join(home, ".bashrc"), []byte("PS1='box> '\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", ContainerCommand())
	cmd.Env = append([]string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"USER=ubuntu",
		"LOGNAME=ubuntu",
		"LANG=C.UTF-8",
	}, ContainerEnv()...)
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = f.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()
	out := readUntil(t, f, 6*time.Second, func(b []byte) bool {
		return bytes.Contains(b, []byte("box>"))
	})
	if !bytes.Contains(out, []byte("box>")) {
		t.Fatalf("container custom prompt missing:\n%s", out)
	}
	if bytes.Contains(out, []byte("38;2;"+promptGreen)) {
		t.Fatalf("container custom prompt replaced:\n%s", out)
	}
}

func TestShHookColorsStockPrompt(t *testing.T) {
	home := t.TempDir()
	hook := filepath.Join(home, "hook.sh")
	if err := os.WriteFile(hook, []byte(shHook()), 0o644); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("/bin/sh", "-i")
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"),
		"HOME=" + home,
		"ENV=" + hook,
		"TERM=dumb",
		"PS1=$ ",
	}
	f, err := pty.Start(cmd)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		_ = f.Close()
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
			_, _ = cmd.Process.Wait()
		}
	}()
	out := readUntil(t, f, 4*time.Second, func(b []byte) bool {
		return bytes.Contains(b, []byte("38;2;"+promptGreen))
	})
	if !bytes.Contains(out, []byte("38;2;"+promptGreen)) || !bytes.Contains(out, []byte("38;2;"+promptBlue)) {
		t.Fatalf("sh prompt:\n%s", out)
	}
}

func stringsContainsAll(s string, parts ...string) bool {
	for _, p := range parts {
		if !bytes.Contains([]byte(s), []byte(p)) {
			return false
		}
	}
	return true
}

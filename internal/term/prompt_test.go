package term

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestApplyTermEnvForcesColor(t *testing.T) {
	got := applyTermEnv([]string{"PATH=/bin", "TERM=dumb", "COLORTERM=no", "HOME=/tmp"})
	term, color := "", ""
	nTerm, nColor := 0, 0
	for _, e := range got {
		switch {
		case strings.HasPrefix(e, "TERM="):
			nTerm++
			term = strings.TrimPrefix(e, "TERM=")
		case strings.HasPrefix(e, "COLORTERM="):
			nColor++
			color = strings.TrimPrefix(e, "COLORTERM=")
		}
	}
	if nTerm != 1 || term != "xterm-256color" {
		t.Fatalf("TERM entries=%d value=%q", nTerm, term)
	}
	if nColor != 1 || color != "truecolor" {
		t.Fatalf("COLORTERM entries=%d value=%q", nColor, color)
	}
	if !strings.Contains(strings.Join(got, "\n"), "PATH=/bin") || !strings.Contains(strings.Join(got, "\n"), "HOME=/tmp") {
		t.Fatalf("unrelated env was dropped: %v", got)
	}
}

func TestStripEnvDropsDaemonPrompt(t *testing.T) {
	got := stripEnv([]string{"PS1=leak> ", "PROMPT_COMMAND=echo leaked", "PATH=/bin", "HOME=/tmp"}, "PS1", "PROMPT_COMMAND")
	for _, e := range got {
		if strings.HasPrefix(e, "PS1=") || strings.HasPrefix(e, "PROMPT_COMMAND=") {
			t.Fatalf("kept %q", e)
		}
	}
	if len(got) != 2 {
		t.Fatalf("env = %v", got)
	}
}

func TestStockPS1Detection(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip(err)
	}
	cases := []struct {
		ps1  string
		want string
	}{
		{"", "STOCK"},
		{`\s-\v\$ `, "STOCK"},
		{`${debian_chroot:+($debian_chroot)}\u@\h:\w\$ `, "STOCK"},
		{`${debian_chroot:+($debian_chroot)}\[\033[01;32m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\$ `, "STOCK"},
		{`${debian_chroot:+($debian_chroot)}\[\033[01;31m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\# `, "STOCK"},
		{`\[\e]0;${debian_chroot:+($debian_chroot)}\u@\h: \w\a\]${debian_chroot:+($debian_chroot)}\[\033[01;32m\]\u@\h\[\033[00m\]:\[\033[01;34m\]\w\[\033[00m\]\$ `, "STOCK"},
		{"custom> ", "CUSTOM"},
		{`(\u@\h) \w \$ `, "CUSTOM"},
	}
	script := promptLib() + `
PS1=$TEST_PS1
if feldcore_stock_ps1; then echo STOCK; else echo CUSTOM; fi
`
	for _, c := range cases {
		cmd := exec.Command("bash", "-c", script)
		cmd.Env = append(os.Environ(), "TEST_PS1="+c.ps1)
		out, err := cmd.Output()
		if err != nil {
			t.Fatalf("ps1 %q: %v", c.ps1, err)
		}
		if strings.TrimSpace(string(out)) != c.want {
			t.Fatalf("ps1 %q: got %q want %s", c.ps1, out, c.want)
		}
	}
}

func TestPromptColorFollowsUID(t *testing.T) {
	if _, err := exec.LookPath("bash"); err != nil {
		t.Skip(err)
	}
	script := promptLib() + `
id() { case "$1" in -u) echo "$FAKE_UID" ;; *) printf '%s\n' "$FAKE_NAME" ;; esac; }
PS1='\s-\v\$ '
feldcore_install_prompt
printf '%s' "$PS1"
`
	root := runBash(t, script, "FAKE_UID=0", "FAKE_NAME=root")
	if !strings.Contains(root, "38;2;"+promptRed) || !strings.Contains(root, promptBlue) {
		t.Fatalf("root prompt = %q", root)
	}
	if !strings.HasSuffix(root, "# ") {
		t.Fatalf("root prompt should end with hash: %q", root)
	}
	user := runBash(t, script, "FAKE_UID=1000", "FAKE_NAME=ubuntu")
	if !strings.Contains(user, "38;2;"+promptGreen) || !strings.Contains(user, "38;2;"+promptBlue) {
		t.Fatalf("user prompt = %q", user)
	}
	if strings.Contains(user, promptRed) {
		t.Fatalf("user prompt used root red: %q", user)
	}

	kept := runBash(t, promptLib()+`
PS1='custom> '
feldcore_install_prompt
printf '%s' "$PS1"
`)
	if kept != "custom> " {
		t.Fatalf("custom prompt changed to %q", kept)
	}
}

func TestHookScriptsParse(t *testing.T) {
	dir := t.TempDir()
	bashFile := filepath.Join(dir, "hook.bash")
	if err := os.WriteFile(bashFile, []byte(hostHook()), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", bashFile).CombinedOutput(); err != nil {
		t.Fatalf("host hook: %v\n%s", err, out)
	}
	initFile := filepath.Join(dir, "init.bash")
	if err := os.WriteFile(initFile, []byte(containerInit()), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("bash", "-n", initFile).CombinedOutput(); err != nil {
		t.Fatalf("container init: %v\n%s", err, out)
	}
	shFile := filepath.Join(dir, "cmd.sh")
	if err := os.WriteFile(shFile, []byte(ContainerCommand()), 0o644); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("sh", "-n", shFile).CombinedOutput(); err != nil {
		t.Fatalf("container command: %v\n%s", err, out)
	}
}

func TestContainerEnvAdvertisesColor(t *testing.T) {
	env := strings.Join(ContainerEnv(), "\n")
	if !strings.Contains(env, "TERM=xterm-256color") || !strings.Contains(env, "COLORTERM=truecolor") {
		t.Fatalf("env = %s", env)
	}
	if !strings.Contains(env, "38;2;"+promptGreen) || !strings.Contains(env, "38;2;"+promptRed) {
		t.Fatal("container init is missing the Campbell prompt")
	}
	if !strings.Contains(ContainerCommand(), "FELDCORE_INIT") || !strings.Contains(ContainerCommand(), "exec sh -i") {
		t.Fatalf("command = %s", ContainerCommand())
	}
}

func runBash(t *testing.T, script string, extra ...string) string {
	t.Helper()
	cmd := exec.Command("bash", "-c", script)
	cmd.Env = append(os.Environ(), extra...)
	out, err := cmd.Output()
	if err != nil {
		t.Fatalf("bash: %v\n%s", err, out)
	}
	return string(out)
}

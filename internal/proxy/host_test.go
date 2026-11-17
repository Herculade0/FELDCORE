package proxy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestBakedCaddyMatchesPin(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "os", "caddy.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(raw)
	if !strings.Contains(text, BakedCaddyVersion) || !strings.Contains(text, "v0.4.7") || !strings.Contains(text, "v0.2.4") {
		t.Fatalf("os/caddy.sha256 does not pin %s:\n%s", BakedCaddyVersion, text)
	}
	if BakedCaddyPath != "/usr/bin/caddy" {
		t.Fatal(BakedCaddyPath)
	}
	script, err := os.ReadFile(filepath.Join("..", "..", "os", "build.sh"))
	if err != nil {
		t.Fatal(err)
	}
	body := string(script)
	for _, want := range []string{
		"install_caddy",
		"github.com/caddyserver/xcaddy/cmd/xcaddy",
		"github.com/caddy-dns/cloudflare",
		"dns.providers.cloudflare",
		"/usr/bin/caddy",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("os/build.sh missing %s", want)
		}
	}
}

func TestHostCaddyArgsEnvAndRestart(t *testing.T) {
	const secret = "super-secret-api-token"
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	envPath := filepath.Join(dir, "env")
	countPath := filepath.Join(dir, "count")
	bin := writeStandIn(t, fmt.Sprintf("printf '%%s\\n' \"$0 $*\" > %q\nenv > %q\necho $$ >> %q\nsleep 30\n", argsPath, envPath, countPath))
	h := &HostCaddy{Bin: bin, Grace: 80 * time.Millisecond, RestartAfter: time.Hour}
	t.Cleanup(func() { _ = h.Stop() })
	spec := runSpec{
		Config: filepath.Join(dir, "Caddyfile"), DataDir: filepath.Join(dir, "data"),
		ConfigDir: filepath.Join(dir, "config"), Home: dir, Token: secret, Stamp: "one",
	}
	if err := h.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if !h.Running() {
		t.Fatal("caddy did not stay up")
	}
	if err := h.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	args := readFile(t, argsPath)
	env := readFile(t, envPath)
	if strings.Contains(args, secret) {
		t.Fatalf("token on argv %s", args)
	}
	for _, want := range []string{bin, "run", "--config", spec.Config, "--adapter", "caddyfile"} {
		if !strings.Contains(args, want) {
			t.Fatalf("args %s missing %s", args, want)
		}
	}
	if strings.Contains(args, "sh -c") {
		t.Fatalf("shell %s", args)
	}
	for _, want := range []string{
		"XDG_DATA_HOME=" + spec.DataDir,
		"XDG_CONFIG_HOME=" + spec.ConfigDir,
		"HOME=" + spec.Home,
		"CF_API_TOKEN=" + secret,
	} {
		if !strings.Contains(env, want) {
			t.Fatalf("env missing %s\n%s", want, env)
		}
	}
	if strings.Count(readFile(t, countPath), "\n") != 1 {
		t.Fatalf("restarted %s", readFile(t, countPath))
	}
	spec.Stamp = "two"
	spec.Token = ""
	if err := h.Start(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	if strings.Count(readFile(t, countPath), "\n") != 2 {
		t.Fatalf("config change did not restart %s", readFile(t, countPath))
	}
	env = readFile(t, envPath)
	if strings.Contains(env, secret) || strings.Contains(env, "CF_API_TOKEN=") {
		t.Fatalf("cleared token still in env %s", env)
	}
	pid := lastPID(t, countPath)
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	if h.Running() || processAlive(pid) {
		t.Fatalf("still running pid %d", pid)
	}
}

func TestHostCaddyRestartsAndStops(t *testing.T) {
	dir := t.TempDir()
	countPath := filepath.Join(dir, "count")
	bin := writeStandIn(t, fmt.Sprintf("echo $$ >> %q\necho proxy failed >&2\nexit 1\n", countPath))
	h := &HostCaddy{Bin: bin, Grace: 40 * time.Millisecond, RestartAfter: 30 * time.Millisecond}
	t.Cleanup(func() { _ = h.Stop() })
	err := h.Start(context.Background(), runSpec{Config: filepath.Join(dir, "Caddyfile"), Home: dir, Stamp: "one"})
	if err == nil || !strings.Contains(err.Error(), "proxy failed") || strings.Contains(err.Error(), "exec:") {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for strings.Count(readMaybe(countPath), "\n") < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	if strings.Count(readMaybe(countPath), "\n") < 2 {
		t.Fatalf("supervisor did not restart\n%s", readMaybe(countPath))
	}
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	stopped := strings.Count(readMaybe(countPath), "\n")
	time.Sleep(150 * time.Millisecond)
	if got := strings.Count(readMaybe(countPath), "\n"); got != stopped {
		t.Fatalf("kept restarting after stop: %d then %d", stopped, got)
	}
	if h.Running() {
		t.Fatal("reported running")
	}
}

func TestHostCaddyMissingBinary(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "caddy")
	h := &HostCaddy{Bin: missing}
	err := h.Start(context.Background(), runSpec{Stamp: "one"})
	if err == nil || h.Running() || strings.Contains(err.Error(), "exec:") || !strings.Contains(err.Error(), missing) {
		t.Fatal(err)
	}
}

func TestHostCaddyListsModules(t *testing.T) {
	bin := writeStandIn(t, "case \"$1\" in list-modules) echo dns.providers.cloudflare; exit 0;; esac\nexit 1\n")
	h := &HostCaddy{Bin: bin}
	ok, err := h.HasModule(context.Background(), DNSModule)
	if err != nil || !ok {
		t.Fatal(err, ok)
	}
	ok, err = h.HasModule(context.Background(), "dns.providers.missing")
	if err != nil || ok {
		t.Fatal(err, ok)
	}
}

func writeStandIn(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "caddy")
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"+body), 0o755); err != nil {
		t.Fatal(err)
	}
	return path
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func readMaybe(path string) string {
	b, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	return string(b)
}

func lastPID(t *testing.T, path string) int {
	t.Helper()
	lines := strings.Fields(readFile(t, path))
	if len(lines) == 0 {
		t.Fatal("no pid")
	}
	var pid int
	if _, err := fmt.Sscan(lines[len(lines)-1], &pid); err != nil || pid <= 0 {
		t.Fatal(lines)
	}
	return pid
}

func processAlive(pid int) bool {
	proc, err := os.FindProcess(pid)
	if err != nil {
		return false
	}
	return proc.Signal(syscall.Signal(0)) == nil
}

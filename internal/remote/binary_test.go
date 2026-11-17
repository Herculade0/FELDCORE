package remote

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

func TestBakedVersionMatchesImagePin(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "..", "os", "cloudflared.sha256"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), BakedCloudflaredVersion) {
		t.Fatalf("os/cloudflared.sha256 does not pin %s:\n%s", BakedCloudflaredVersion, raw)
	}
	if BakedCloudflaredPath != "/usr/bin/cloudflared" {
		t.Fatal(BakedCloudflaredPath)
	}
}

func TestHostCloudflaredPassesTokenFile(t *testing.T) {
	const secret = "super-secret-tunnel-token"
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	envPath := filepath.Join(dir, "env")
	countPath := filepath.Join(dir, "count")
	bin := writeStandIn(t, fmt.Sprintf("printf '%%s\\n' \"$0 $*\" > %q\nenv > %q\necho $$ >> %q\nsleep 30\n", argsPath, envPath, countPath))
	tokenFile := filepath.Join(dir, "tunnel.token")
	if err := os.WriteFile(tokenFile, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	h := &HostCloudflared{Bin: bin, TokenFile: tokenFile, Grace: 80 * time.Millisecond, RestartAfter: time.Hour}
	t.Cleanup(func() { _ = h.Stop() })
	if err := h.Start(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	if !h.Running() {
		t.Fatal("connector did not stay up")
	}
	if err := h.Start(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	args := readFile(t, argsPath)
	env := readFile(t, envPath)
	if strings.Contains(args, secret) || strings.Contains(env, secret) || strings.Contains(env, "TUNNEL_TOKEN=") {
		t.Fatalf("secret leaked\nargs %s\nenv %s", args, env)
	}
	if !strings.Contains(args, "--token-file") || !strings.Contains(args, tokenFile) || !strings.Contains(args, bin) {
		t.Fatalf("args %s", args)
	}
	if strings.Contains(args, " sh ") || strings.HasPrefix(args, "sh ") {
		t.Fatalf("shell %s", args)
	}
	if strings.Count(readFile(t, countPath), "\n") != 1 {
		t.Fatalf("restarted %s", readFile(t, countPath))
	}
	if err := h.Start(context.Background(), secret+"-rotated"); err != nil {
		t.Fatal(err)
	}
	if strings.Count(readFile(t, countPath), "\n") != 2 {
		t.Fatalf("token change did not restart %s", readFile(t, countPath))
	}
	pid := lastPID(t, countPath)
	if err := h.Stop(); err != nil {
		t.Fatal(err)
	}
	if h.Running() {
		t.Fatal("still running after stop")
	}
	if processAlive(pid) {
		t.Fatalf("pid %d still alive", pid)
	}
}

func TestHostCloudflaredEnvTokenStaysOffArgv(t *testing.T) {
	const secret = "super-secret-tunnel-token"
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	envPath := filepath.Join(dir, "env")
	bin := writeStandIn(t, fmt.Sprintf("printf '%%s\\n' \"$0 $*\" > %q\nenv > %q\nsleep 30\n", argsPath, envPath))
	h := &HostCloudflared{Bin: bin, Grace: 80 * time.Millisecond, RestartAfter: time.Hour}
	t.Cleanup(func() { _ = h.Stop() })
	if err := h.Start(context.Background(), secret); err != nil {
		t.Fatal(err)
	}
	args := readFile(t, argsPath)
	env := readFile(t, envPath)
	if strings.Contains(args, secret) || !strings.Contains(env, "TUNNEL_TOKEN="+secret) {
		t.Fatalf("args %s\nenv has token %v", args, strings.Contains(env, "TUNNEL_TOKEN="+secret))
	}
}

func TestHostCloudflaredRestartsAndStops(t *testing.T) {
	dir := t.TempDir()
	countPath := filepath.Join(dir, "count")
	bin := writeStandIn(t, fmt.Sprintf("echo $$ >> %q\necho connector failed >&2\nexit 1\n", countPath))
	h := &HostCloudflared{Bin: bin, Grace: 40 * time.Millisecond, RestartAfter: 30 * time.Millisecond}
	t.Cleanup(func() { _ = h.Stop() })
	err := h.Start(context.Background(), "super-secret-tunnel-token")
	if err == nil || !strings.Contains(err.Error(), "connector failed") || strings.Contains(err.Error(), "exec:") {
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

func TestHostCloudflaredMissingBinary(t *testing.T) {
	missing := filepath.Join(t.TempDir(), "cloudflared")
	h := &HostCloudflared{Bin: missing}
	err := h.Start(context.Background(), "super-secret-tunnel-token")
	if err == nil || h.Running() || strings.Contains(err.Error(), "exec:") || !strings.Contains(err.Error(), missing) {
		t.Fatal(err)
	}
}

func TestBakedPathBeatsDocker(t *testing.T) {
	const secret = "super-secret-tunnel-token"
	dir := t.TempDir()
	argsPath := filepath.Join(dir, "args")
	envPath := filepath.Join(dir, "env")
	bin := writeStandIn(t, fmt.Sprintf("printf '%%s\\n' \"$0 $*\" > %q\nenv > %q\nsleep 30\n", argsPath, envPath))
	prev := bakedCloudflared
	bakedCloudflared = bin
	t.Cleanup(func() { bakedCloudflared = prev })

	svc, eng, _ := newSvc(t)
	svc.PreferHost = nil
	h := &HostCloudflared{Grace: 80 * time.Millisecond, RestartAfter: time.Hour}
	svc.Binary = h
	t.Cleanup(func() { _ = h.Stop() })
	if err := svc.Save(context.Background(), SaveInput{Mode: "token", TunnelToken: secret}); err != nil {
		t.Fatal(err)
	}
	if len(eng.specs) != 0 {
		t.Fatalf("container %+v", eng.specs)
	}
	if !h.Running() || !svc.View(context.Background()).RunsOnHost {
		t.Fatalf("running %v view %+v", h.Running(), svc.View(context.Background()).Status)
	}
	args := readFile(t, argsPath)
	env := readFile(t, envPath)
	if strings.Contains(args, secret) || strings.Contains(env, secret) || !strings.Contains(args, "--token-file") || !strings.Contains(args, svc.tokenPath()) {
		t.Fatalf("args %s\nenv leaked %v", args, strings.Contains(env, secret))
	}
	got, err := os.ReadFile(svc.tokenPath())
	if err != nil || string(got) != secret {
		t.Fatalf("token file %q %v", got, err)
	}
	fi, err := os.Stat(svc.tokenPath())
	if err != nil || fi.Mode().Perm()&0o077 != 0 {
		t.Fatalf("mode %v %v", fi, err)
	}
}

func writeStandIn(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "cloudflared")
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

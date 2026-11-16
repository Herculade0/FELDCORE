package docker

import (
	"strings"
	"testing"
)

func TestConsoleCreateBodyRequestsColor(t *testing.T) {
	body := consoleCreateBody()
	if body["Tty"] != true {
		t.Fatal("console should allocate a TTY")
	}
	env, ok := body["Env"].([]string)
	if !ok {
		t.Fatalf("Env type %T", body["Env"])
	}
	joined := strings.Join(env, "\n")
	if !strings.Contains(joined, "TERM=xterm-256color") {
		t.Fatal("missing TERM=xterm-256color")
	}
	if !strings.Contains(joined, "COLORTERM=truecolor") {
		t.Fatal("missing COLORTERM=truecolor")
	}
	if !strings.Contains(joined, "38;2;22;198;12") || !strings.Contains(joined, "38;2;231;72;86") {
		t.Fatal("console env is missing the Campbell prompt")
	}
	cmd, ok := body["Cmd"].([]string)
	if !ok || len(cmd) != 3 || cmd[0] != "sh" || cmd[1] != "-c" {
		t.Fatalf("Cmd = %#v", body["Cmd"])
	}
	if !strings.Contains(cmd[2], "FELDCORE_INIT") || !strings.Contains(cmd[2], "exec sh -i") {
		t.Fatalf("shell command = %s", cmd[2])
	}
	for _, key := range []string{"AttachStdin", "AttachStdout", "AttachStderr"} {
		if body[key] != true {
			t.Fatalf("%s not attached", key)
		}
	}
}

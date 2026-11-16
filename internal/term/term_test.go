package term

import "testing"

func TestShellPrefersBashLogin(t *testing.T) {
	path, args := Shell()
	if path == "" {
		t.Fatal("no shell")
	}
	if len(args) == 1 && args[0] == "-l" {
		return
	}
	// bash is absent: the fallback is sh with no login flag.
	if len(args) != 0 {
		t.Fatalf("fallback shell args = %v", args)
	}
}

package osupdate

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

func TestEntryBodyQuietsTheConsoleWithoutChangingBootCounting(t *testing.T) {
	body := EntryBody("a", "0.1.2")
	if !strings.Contains(body, consoleOptions) {
		t.Fatalf("options missing %q\n%s", consoleOptions, body)
	}
	for _, want := range []string{
		"root=LABEL=feldcore-a",
		"feldcore.slot=a",
		"feldcore.version=0.1.2",
		"linux /feldcore/a/vmlinuz",
		"initrd /feldcore/a/initrd.img",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("missing %q\n%s", want, body)
		}
	}
	tty0 := strings.Index(body, "console=tty0")
	serial := strings.Index(body, "console=ttyS0,115200")
	if tty0 < 0 || serial < tty0 {
		t.Fatalf("ttyS0 must stay the last console so /dev/console is serial\n%s", body)
	}
	name := EntryName("b", BootTries)
	if name != "feldcore-b+3.conf" {
		t.Fatalf("boot counter name changed: %s", name)
	}
	if EntryName("a", 0) != "feldcore-a.conf" {
		t.Fatal("stable entry name changed")
	}
}

func TestBootScriptsUseTheSameConsoleOptions(t *testing.T) {
	root := repoRoot(t)
	for _, rel := range []string{
		"os/build.sh",
		"os/iso/grub.cfg",
	} {
		b, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(b), consoleOptions) {
			t.Fatalf("%s does not contain %q", rel, consoleOptions)
		}
	}
	grub, err := os.ReadFile(filepath.Join(root, "os/iso/grub.cfg"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(grub)
	if strings.Contains(text, "feldcore.slot=") || strings.Contains(text, "root=LABEL=") {
		t.Fatalf("live cmdline grew an appliance root:\n%s", text)
	}
	if !strings.Contains(text, "boot=feldcore") || !strings.Contains(text, "feldcore.live=1") {
		t.Fatalf("live cmdline lost its installer markers:\n%s", text)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("caller")
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "..", ".."))
}

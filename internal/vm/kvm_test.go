package vm

import (
	"strings"
	"testing"
)

func TestKVMMessage(t *testing.T) {
	cpuVirt := "flags : fpu vmx syscall"
	cpuNone := "flags : fpu syscall"
	wsl := "Linux version 6.6.0-microsoft-standard-WSL2"
	bare := "Linux version 6.12.0"

	if got := kvmMessage(cpuVirt, wsl); !strings.Contains(got, "nestedVirtualization=true") || !strings.Contains(got, "/dev/kvm") {
		t.Fatalf("wsl message: %s", got)
	}
	if strings.Contains(kvmMessage(cpuVirt, wsl), "%!") {
		t.Fatal("wsl message was treated as a format string")
	}
	if got := kvmMessage(cpuNone, bare); !strings.Contains(got, "vmx or svm") {
		t.Fatalf("no virt: %s", got)
	}
	if got := kvmMessage(cpuVirt, bare); !strings.Contains(got, "kvm module") {
		t.Fatalf("module: %s", got)
	}
	if cpuHasFlag(cpuVirt, "svm") || !cpuHasFlag(cpuVirt, "vmx") {
		t.Fatal("flag parse")
	}
}

func TestParseVNC(t *testing.T) {
	cases := map[string]string{
		":0":             "127.0.0.1:5900",
		"127.0.0.1:1":    "127.0.0.1:5901",
		"0.0.0.0:2":      "127.0.0.1:5902",
		"127.0.0.1:5910": "127.0.0.1:5910",
		"[::1]:3":        "[::1]:5903",
	}
	for in, want := range cases {
		got, err := parseVNC(in)
		if err != nil || got != want {
			t.Fatalf("%s -> %s %v, want %s", in, got, err, want)
		}
	}
	if _, err := parseVNC("192.168.1.20:1"); err == nil {
		t.Fatal("public vnc accepted")
	}
}

func TestStoragePath(t *testing.T) {
	if err := validateStoragePath("/etc/libvirt"); err == nil {
		t.Fatal("etc accepted")
	}
	if err := validateStoragePath("/srv/feldcore/domains"); err != nil {
		t.Fatal(err)
	}
	if err := validateStoragePath("relative"); err == nil {
		t.Fatal("relative accepted")
	}
	if err := validateStoragePath("/srv/feldcore/../etc"); err == nil {
		t.Fatal("traversal accepted")
	}
	if !within("/srv/feldcore/domains", "/srv/feldcore/domains/win/vdisk1.qcow2") {
		t.Fatal("child rejected")
	}
	if within("/srv/feldcore/domains", "/etc/passwd") {
		t.Fatal("outside path accepted")
	}
}

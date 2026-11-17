package osimage

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// requiredBase are packages the image must install. The smoke test checks
// the binaries; this test checks the package list cannot quietly drop one.
var requiredBase = []string{
	"docker.io",
	"docker-cli",
	"containerd",
	"docker-compose",
	"git",
	"samba",
	"nfs-kernel-server",
	"restic",
	"rclone",
	"cifs-utils",
	"wireguard-tools",
	"smartmontools",
	"mdadm",
	"btrfs-progs",
	"xfsprogs",
	"hdparm",
	"parted",
	"ufw",
	"rtl-sdr",
	"systemd-timesyncd",
	"iptables",
	"curl",
	"bash",
	"pciutils",
	"firmware-amd-graphics",
	"firmware-intel-graphics",
	"firmware-misc-nonfree",
}

var requiredLibvirt = []string{
	"libvirt-daemon-system",
	"libvirt-clients",
	"qemu-system-x86",
	"qemu-utils",
	"ovmf",
	"swtpm",
	"swtpm-tools",
	"virtiofsd",
}

func TestPackageLists(t *testing.T) {
	root := repoRoot(t)
	base := parseList(t, filepath.Join(root, "os", "packages.txt"))
	for _, pkg := range requiredBase {
		if !base[pkg] {
			t.Errorf("os/packages.txt is missing %s", pkg)
		}
	}
	mkosi := mkosiPackages(t, filepath.Join(root, "os", "mkosi", "mkosi.conf"))
	for pkg := range base {
		if !mkosi[pkg] {
			t.Errorf("mkosi.conf is missing %s from packages.txt", pkg)
		}
	}
	for pkg := range mkosi {
		if !base[pkg] {
			t.Errorf("mkosi.conf lists %s which is not in packages.txt", pkg)
		}
	}
	libvirt := parseList(t, filepath.Join(root, "os", "sysext", "libvirt.packages"))
	for _, pkg := range requiredLibvirt {
		if !libvirt[pkg] {
			t.Errorf("libvirt sysext is missing %s", pkg)
		}
	}
	if base["qemu-system-x86"] || base["libvirt-daemon-system"] || base["nvidia-container-toolkit"] {
		t.Fatal("libvirt and the NVIDIA toolkit belong in sysexts, not the base image")
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("go.mod not found")
		}
		dir = parent
	}
}

func parseList(t *testing.T, path string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		out[line] = true
	}
	return out
}

func mkosiPackages(t *testing.T, path string) map[string]bool {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	out := map[string]bool{}
	in := false
	for _, line := range strings.Split(string(b), "\n") {
		trim := strings.TrimSpace(line)
		if strings.HasPrefix(trim, "Packages=") {
			in = true
			rest := strings.TrimSpace(strings.TrimPrefix(trim, "Packages="))
			if rest != "" {
				out[rest] = true
			}
			continue
		}
		if !in {
			continue
		}
		if trim == "" || strings.HasPrefix(trim, "[") {
			break
		}
		if strings.HasPrefix(trim, "#") {
			continue
		}
		out[trim] = true
	}
	if len(out) == 0 {
		t.Fatal("mkosi.conf has no Packages")
	}
	return out
}

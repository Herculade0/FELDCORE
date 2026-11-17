package storage

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/install"
)

// Executor runs one command without a shell. Look reports whether name is on
// PATH, including the usual sbin directories. The daemon uses the host.
// Tests pass their own implementation and never point it at a real disk.
type Executor interface {
	Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error)
	Look(name string) bool
}

// guard is the last check before a command that can erase or retarget a disk.
// allow, when non-nil, is the only set of device paths that may be named.
// Tests set it to the loop devices they created. The daemon leaves it nil and
// sets permit, which still refuses the operating-system disk.
type guard struct {
	permit    bool
	allow     map[string]bool
	osDevices func() (map[string]bool, string, error)
}

func (g guard) check(name string, args []string) error {
	if !sensitive(name, args) {
		return nil
	}
	for _, a := range args {
		if isOSMount(a) {
			return input("That path belongs to the operating system. FELDCORE will not change it.")
		}
	}
	osDevs, rootID, err := g.lookupOS()
	if err != nil {
		return input("Could not identify the operating-system disk, so nothing was changed.")
	}
	if rootID != "" {
		for _, a := range args {
			if uuidEqual(a, rootID) || uuidEqual(strings.TrimPrefix(a, "UUID="), rootID) {
				return input("That is the operating-system filesystem. FELDCORE will not change it.")
			}
		}
	}
	devs := deviceArgs(args)
	if g.allow != nil {
		for _, d := range devs {
			if osDevs[d] {
				return inputf("%s holds the operating system. FELDCORE will not format, mount or change it.", d)
			}
			if !g.allow[d] {
				return inputf("Refusing to change %s. It is not one of the disks this operation is allowed to touch.", d)
			}
		}
		return nil
	}
	if !g.permit {
		return input("Refusing to change a disk. This process is not allowed to format host disks.")
	}
	for _, d := range devs {
		if osDevs[d] {
			return inputf("%s holds the operating system. FELDCORE will not format, mount or change it.", d)
		}
	}
	return nil
}

func (g guard) lookupOS() (map[string]bool, string, error) {
	if g.osDevices == nil {
		return map[string]bool{}, "", nil
	}
	devs, id, err := g.osDevices()
	if err != nil {
		return nil, "", err
	}
	if devs == nil {
		devs = map[string]bool{}
	}
	return devs, id, nil
}

func isOSMount(p string) bool {
	switch filepath.Clean(p) {
	case "/", "/boot", "/boot/efi", "/usr":
		return true
	default:
		return false
	}
}

func uuidEqual(a, b string) bool {
	return a != "" && strings.EqualFold(strings.TrimSpace(a), strings.TrimSpace(b))
}

func sensitive(name string, args []string) bool {
	base := filepath.Base(name)
	switch base {
	case "mkfs.btrfs", "mkfs.xfs", "mkfs.ext2", "mkfs.ext3", "mkfs.ext4", "mkfs.ntfs",
		"wipefs", "sgdisk", "sfdisk", "fdisk", "parted", "blkdiscard", "dd", "shred", "hdparm":
		return true
	case "mount", "umount":
		return true
	case "zpool":
		if len(args) == 0 {
			return false
		}
		switch args[0] {
		case "create", "add", "attach", "replace", "destroy", "labelclear", "remove", "split", "offline":
			return true
		default:
			return false
		}
	case "btrfs":
		if len(args) >= 2 && args[0] == "device" && (args[1] == "add" || args[1] == "delete" || args[1] == "remove") {
			return true
		}
		if len(args) >= 1 && args[0] == "replace" {
			return true
		}
		return false
	default:
		return false
	}
}

func deviceArgs(args []string) []string {
	var out []string
	for _, a := range args {
		if !strings.HasPrefix(a, "/") {
			continue
		}
		if strings.Contains(a, "..") {
			out = append(out, a)
			continue
		}
		resolved := a
		if r, err := filepath.EvalSymlinks(a); err == nil && r != "" {
			resolved = r
		}
		if strings.HasPrefix(resolved, "/dev/") || strings.HasPrefix(a, "/dev/") {
			out = append(out, resolved)
		}
	}
	return out
}

func newHost(permit bool, allow map[string]bool) *hostExec {
	h := &hostExec{}
	h.g = guard{
		permit: permit,
		allow:  allow,
		osDevices: func() (map[string]bool, string, error) {
			return scanOS(context.Background(), h)
		},
	}
	return h
}

type hostExec struct {
	g guard
}

func scanOS(ctx context.Context, run Executor) (map[string]bool, string, error) {
	devs := map[string]bool{}
	var rootID string
	for _, m := range osMountList() {
		out, err := run.Run(ctx, 3*time.Second, "findmnt", "-n", "-o", "SOURCE", m)
		if err != nil {
			if m == "/" {
				return nil, "", err
			}
			continue
		}
		src := strings.TrimSpace(out)
		if i := strings.IndexByte(src, '\n'); i >= 0 {
			src = src[:i]
		}
		if src == "" || !strings.HasPrefix(src, "/dev/") {
			continue
		}
		if r, err := filepath.EvalSymlinks(src); err == nil && r != "" {
			src = r
		}
		devs[src] = true
		if parent := parentDisk(src); parent != "" {
			devs[parent] = true
		}
		if m == "/" {
			id, err := run.Run(ctx, 3*time.Second, "findmnt", "-n", "-o", "UUID", "/")
			if err == nil {
				rootID = strings.TrimSpace(id)
			}
		}
	}
	return devs, rootID, nil
}

func (h hostExec) Look(name string) bool {
	_, err := lookBin(name)
	return err == nil
}

func (h hostExec) Run(ctx context.Context, timeout time.Duration, name string, args ...string) (string, error) {
	if err := h.g.check(name, args); err != nil {
		return "", err
	}
	out, err := install.Run(ctx, timeout, name, args...)
	if err == nil {
		return out, nil
	}
	detail := strings.TrimSpace(out)
	if detail == "" {
		detail = err.Error()
	}
	return out, opf("%s failed: %s", filepath.Base(name), clip(detail, 500))
}

func lookBin(name string) (string, error) {
	if p, err := exec.LookPath(name); err == nil {
		return p, nil
	}
	for _, dir := range []string{"/usr/sbin", "/sbin", "/usr/bin", "/bin", "/usr/local/sbin", "/usr/local/bin"} {
		p := filepath.Join(dir, name)
		st, err := os.Stat(p)
		if err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, nil
		}
	}
	return "", inputf("%s is not installed", name)
}

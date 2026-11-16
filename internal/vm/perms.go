package vm

import (
	"os"
	"os/user"
	"strconv"
)

// relaxPerms lets libvirt-qemu read a disk or a cloud-init seed. The seed is
// private (it can contain a password) and is group-readable only when the
// libvirt group exists.
func relaxPerms(path string, private bool) {
	gid := -1
	for _, name := range []string{"libvirt-qemu", "kvm"} {
		g, err := user.LookupGroup(name)
		if err != nil {
			continue
		}
		n, err := strconv.Atoi(g.Gid)
		if err != nil {
			continue
		}
		gid = n
		break
	}
	if gid >= 0 {
		_ = os.Chown(path, os.Getuid(), gid)
		if private {
			_ = os.Chmod(path, 0o640)
		} else {
			_ = os.Chmod(path, 0o664)
		}
		return
	}
	if private {
		_ = os.Chmod(path, 0o600)
		return
	}
	_ = os.Chmod(path, 0o644)
}

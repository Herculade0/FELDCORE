//go:build linux

package vpn

import (
	"fmt"
	"runtime"

	"golang.org/x/sys/unix"
)

// EnterNetNS runs fn in the network namespace of pid, then restores the
// caller's namespace. The goroutine stays pinned to one OS thread for the
// duration because namespaces are a thread property.
func EnterNetNS(pid int, fn func() error) error {
	if pid <= 0 {
		return fmt.Errorf("the VPN gateway is not running")
	}
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	self, err := unix.Open("/proc/self/ns/net", unix.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer unix.Close(self)
	target, err := unix.Open(fmt.Sprintf("/proc/%d/ns/net", pid), unix.O_RDONLY, 0)
	if err != nil {
		return err
	}
	defer unix.Close(target)
	if err := unix.Setns(target, unix.CLONE_NEWNET); err != nil {
		return err
	}
	defer unix.Setns(self, unix.CLONE_NEWNET)
	return fn()
}

//go:build !linux

package vpn

import "fmt"

// EnterNetNS is only implemented on Linux.
func EnterNetNS(pid int, fn func() error) error {
	return fmt.Errorf("NAT-PMP needs a Linux network namespace")
}

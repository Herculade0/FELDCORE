// Package appliance reports whether this process is running on the
// FELDCORE OS image. The image replaces apt with a stub, so a LookPath
// hit on apt-get is not a working package manager.
package appliance

import (
	"os"
	"os/exec"
	"strings"
	"sync"
)

var (
	mu          sync.RWMutex
	releasePath = "/etc/feldcore-os-release"
)

// SetReleasePath points the image check at path. The returned function
// restores the previous path. Tests use it.
func SetReleasePath(path string) func() {
	mu.Lock()
	prev := releasePath
	releasePath = path
	mu.Unlock()
	return func() {
		mu.Lock()
		releasePath = prev
		mu.Unlock()
	}
}

// Is reports whether /etc/feldcore-os-release says this is FELDCORE OS.
func Is() bool {
	mu.RLock()
	path := releasePath
	mu.RUnlock()
	b, err := os.ReadFile(path)
	if err != nil {
		return false
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok || key != "ID" {
			continue
		}
		val = strings.Trim(strings.TrimSpace(val), `"'`)
		return val == "feldcore"
	}
	return false
}

// RealApt reports whether apt-get is a package manager this process can use.
// The FELDCORE OS image ships a stub at the apt-get path, so the binary
// existing is not enough.
func RealApt() bool {
	if Is() {
		return false
	}
	_, err := exec.LookPath("apt-get")
	return err == nil
}

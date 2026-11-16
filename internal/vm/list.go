// Package vm lists libvirt domains when the VM layer is installed.
// Phase 1 is read-only: virsh is started with -r, which refuses changes.
package vm

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

// Domain is one virtual machine.
type Domain struct {
	ID    string `json:"id,omitempty"`
	Name  string `json:"name"`
	State string `json:"state"`
}

// List is the answer to "what VMs are there". Available is false when virsh
// is not installed, which is the case until the VM layer exists.
type List struct {
	Available bool     `json:"available"`
	Message   string   `json:"message,omitempty"`
	VMs       []Domain `json:"vms"`
}

// Probe looks up virsh and runs a read-only domain list. Look and Run are
// optional; leave them nil to use the host.
type Probe struct {
	Look func(name string) (path string, ok bool)
	Run  func(ctx context.Context, path string, args ...string) (string, error)
}

var virshLine = regexp.MustCompile(`^(\S+)\s+(\S+)\s+(.+)$`)

var searchDirs = []string{
	"/usr/sbin", "/sbin", "/usr/bin", "/bin", "/usr/local/sbin", "/usr/local/bin",
}

// List returns the domains virsh can see. A missing binary is not an error:
// the VM layer is simply not installed.
func (p Probe) List(ctx context.Context) List {
	look := p.Look
	if look == nil {
		look = findVirsh
	}
	path, ok := look("virsh")
	if !ok {
		return empty(false, "The VM layer is not installed on this server.")
	}
	run := p.Run
	if run == nil {
		run = runVirsh
	}
	// -r opens a read-only connection, so this cannot start or stop a domain.
	out, err := run(ctx, path, "-r", "-c", "qemu:///system", "list", "--all")
	if err != nil {
		return empty(true, "libvirt is installed, but the domain list failed: "+err.Error())
	}
	vms, err := parseVirshList(out)
	if err != nil {
		return empty(true, err.Error())
	}
	return List{Available: true, VMs: vms}
}

func empty(available bool, message string) List {
	return List{Available: available, Message: message, VMs: []Domain{}}
}

func parseVirshList(out string) ([]Domain, error) {
	lines := strings.Split(strings.ReplaceAll(out, "\r\n", "\n"), "\n")
	start := -1
	for i, ln := range lines {
		if strings.Contains(ln, "---") {
			start = i + 1
			break
		}
	}
	if start < 0 {
		return nil, fmt.Errorf("virsh did not return a domain list")
	}
	vms := []Domain{}
	for _, ln := range lines[start:] {
		ln = strings.TrimSpace(ln)
		if ln == "" {
			continue
		}
		m := virshLine.FindStringSubmatch(ln)
		if m == nil {
			continue
		}
		id := m[1]
		if id == "-" {
			id = ""
		}
		vms = append(vms, Domain{ID: id, Name: m[2], State: strings.TrimSpace(m[3])})
	}
	return vms, nil
}

func findVirsh(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, dir := range searchDirs {
		p := filepath.Join(dir, name)
		st, err := os.Stat(p)
		if err == nil && !st.IsDir() && st.Mode()&0o111 != 0 {
			return p, true
		}
	}
	return "", false
}

func runVirsh(ctx context.Context, path string, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Env = append(os.Environ(), "LC_ALL=C")
	out, err := cmd.CombinedOutput()
	text := strings.TrimSpace(string(out))
	if err != nil {
		if text != "" {
			return text, fmt.Errorf("%s", lastLine(text))
		}
		return "", err
	}
	return text, nil
}

package shares

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/Herculade0/feldcore/internal/appliance"
)

// ServiceStatus describes one file-sharing service on this machine.
type ServiceStatus struct {
	Installed  bool   `json:"installed"`
	Active     bool   `json:"active"`
	CanInstall bool   `json:"canInstall"`
	Error      string `json:"error,omitempty"` // why the last apply failed, if it did
}

// Services is the state of everything shares depend on.
type Services struct {
	// Privileged is false when FELDCORE lacks the rights to manage sharing.
	Privileged bool          `json:"privileged"`
	SMB        ServiceStatus `json:"smb"`
	NFS        ServiceStatus `json:"nfs"`
}

// Services reports what is installed and running.
func (m *Manager) Services(ctx context.Context) Services {
	root := os.Geteuid() == 0
	apt := appliance.RealApt()
	_, smbd := findBin("smbd")
	_, exportfs := findBin("exportfs")

	m.statusMu.RLock()
	smbErr, nfsErr := m.smbErr, m.nfsErr
	m.statusMu.RUnlock()

	return Services{
		Privileged: root,
		SMB: ServiceStatus{
			Installed:  smbd,
			Active:     smbd && smbActive(ctx),
			CanInstall: !smbd && root && apt,
			Error:      smbErr,
		},
		NFS: ServiceStatus{
			Installed:  exportfs,
			Active:     exportfs && nfsActive(ctx),
			CanInstall: !exportfs && root && apt,
			Error:      nfsErr,
		},
	}
}

func unitActive(ctx context.Context, units ...string) bool {
	for _, u := range units {
		if _, err := run(ctx, 10*time.Second, "", "systemctl", "is-active", "--quiet", u); err == nil {
			return true
		}
	}
	return false
}

// Unit names differ between distributions (Debian: smbd, Fedora/Arch: smb).
func smbActive(ctx context.Context) bool { return unitActive(ctx, "smbd", "smb") }
func nfsActive(ctx context.Context) bool { return unitActive(ctx, "nfs-server") }

// startService enables and starts the first of the candidate units that works.
func startService(ctx context.Context, units ...string) error {
	var last error
	for _, u := range units {
		if _, err := run(ctx, 60*time.Second, "", "systemctl", "enable", "--now", u); err == nil {
			return nil
		} else {
			last = err
		}
	}
	return last
}

var packages = map[string]string{
	"smb": "samba",
	"nfs": "nfs-kernel-server",
}

// Install installs the package behind a service ("smb" or "nfs") with apt,
// then re-applies share configuration.
func (m *Manager) Install(ctx context.Context, service string) error {
	pkg, ok := packages[service]
	if !ok {
		return ErrNotFound
	}
	if appliance.Is() {
		return &OpError{fmt.Sprintf("%s is part of FELDCORE OS and is not installed from this page.", pkg)}
	}
	if os.Geteuid() != 0 {
		return &OpError{"FELDCORE must run as root to install software."}
	}
	if _, ok := findBin("apt-get"); !ok {
		return &OpError{fmt.Sprintf("Automatic install needs apt. Install the %q package with your system's package manager.", pkg)}
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	ctx = context.WithoutCancel(ctx)
	m.log.Info("installing package", "package", pkg)
	// A stale package index is the usual cause of install failures; refresh
	// it first but do not give up if a third-party repository is down.
	if _, err := run(ctx, 5*time.Minute, "", "apt-get", "update"); err != nil {
		m.log.Warn("apt-get update failed; trying the install anyway", "err", err)
	}
	if _, err := run(ctx, 10*time.Minute, "", "apt-get", "install", "-y", pkg); err != nil {
		var msg string
		if errors.Is(err, context.DeadlineExceeded) {
			msg = "the install timed out"
		} else {
			msg = err.Error()
		}
		return &OpError{fmt.Sprintf("Could not install %s: %s", pkg, msg)}
	}
	m.log.Info("package installed", "package", pkg)
	m.applyLocked(ctx)
	return nil
}

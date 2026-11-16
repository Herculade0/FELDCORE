package shares

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

const managedHeader = "# Managed by FELDCORE. Do not edit: changes are overwritten.\n"

// RenderSMB produces the Samba configuration for every SMB-enabled share.
func RenderSMB(shares []store.Share, root string, id identity) string {
	enabled := smbEnabled(shares)
	sort.Slice(enabled, func(i, j int) bool {
		return strings.ToLower(enabled[i].Name) < strings.ToLower(enabled[j].Name)
	})

	var b strings.Builder
	b.WriteString(managedHeader)

	guest := false
	for _, sh := range enabled {
		if sh.SMB.Access != store.SMBPrivate {
			guest = true
		}
	}
	if guest {
		// Guest shares need unknown users mapped to the guest account.
		// Re-opening [global] here merges with the main configuration.
		b.WriteString("\n[global]\n   map to guest = Bad User\n")
	}

	for _, sh := range enabled {
		fmt.Fprintf(&b, "\n[%s]\n", sh.Name)
		if sh.Comment != "" {
			fmt.Fprintf(&b, "   comment = %s\n", sh.Comment)
		}
		fmt.Fprintf(&b, "   path = %s\n", root+"/"+sh.Name)
		b.WriteString("   browseable = yes\n")
		switch sh.SMB.Access {
		case store.SMBReadOnly:
			b.WriteString("   read only = yes\n   guest ok = yes\n")
		case store.SMBPrivate:
			b.WriteString("   read only = no\n   guest ok = no\n")
			fmt.Fprintf(&b, "   valid users = %s\n", strings.Join(sh.SMB.Users, " "))
		default: // public
			b.WriteString("   read only = no\n   guest ok = yes\n")
		}
		fmt.Fprintf(&b, "   force user = %s\n   force group = %s\n", id.User, id.Group)
		b.WriteString("   create mask = 0664\n   directory mask = 0775\n")
	}
	return b.String()
}

func smbEnabled(shares []store.Share) []store.Share {
	var out []store.Share
	for _, sh := range shares {
		if sh.SMB.Enabled {
			out = append(out, sh)
		}
	}
	return out
}

var errNotRoot = errors.New("FELDCORE must run as root to manage file sharing")

// applySMB writes the Samba include file, validates it, and reloads Samba.
func (m *Manager) applySMB(ctx context.Context, shares []store.Share) error {
	wanted := len(smbEnabled(shares)) > 0
	// Nothing to do, and nothing of ours on disk: leave the system untouched.
	if !wanted && !fileExists(m.p.SambaInclude) {
		return nil
	}
	if _, ok := findBin("smbd"); !ok {
		if wanted {
			return errors.New("Samba is not installed")
		}
		return nil
	}
	if os.Geteuid() != 0 {
		return errNotRoot
	}

	previous, readErr := os.ReadFile(m.p.SambaInclude)
	content := []byte(RenderSMB(shares, m.p.SharesRoot, m.id))
	if err := writeAtomic(m.p.SambaInclude, content, 0o644); err != nil {
		return fmt.Errorf("could not write %s: %v", m.p.SambaInclude, err)
	}
	if err := m.ensureInclude(); err != nil {
		return err
	}

	// Validate before reloading; put the old file back if Samba objects.
	if _, err := run(ctx, 30*time.Second, "", "testparm", "-s", m.p.SambaConf); err != nil {
		if readErr == nil {
			_ = writeAtomic(m.p.SambaInclude, previous, 0o644)
		} else {
			_ = os.Remove(m.p.SambaInclude)
		}
		return fmt.Errorf("Samba rejected the new configuration (%v); the previous one was kept", err)
	}

	if wanted && !smbActive(ctx) {
		if err := startService(ctx, "smbd", "smb"); err != nil {
			return fmt.Errorf("Samba could not be started: %v", err)
		}
		return nil // a fresh start reads the new configuration
	}
	if smbActive(ctx) {
		if _, err := run(ctx, 30*time.Second, "", "smbcontrol", "all", "reload-config"); err != nil {
			return fmt.Errorf("Samba could not reload its configuration: %v", err)
		}
	}
	return nil
}

// ensureInclude makes the system smb.conf include FELDCORE's file, backing up
// the original the first time it is touched.
func (m *Manager) ensureInclude() error {
	raw, err := os.ReadFile(m.p.SambaConf)
	if err != nil {
		return fmt.Errorf("could not read %s: %v", m.p.SambaConf, err)
	}
	want := "include = " + m.p.SambaInclude
	for _, line := range strings.Split(string(raw), "\n") {
		if strings.Join(strings.Fields(line), " ") == want {
			return nil
		}
	}
	backup := m.p.SambaConf + ".feldcore-backup"
	if !fileExists(backup) {
		if err := os.WriteFile(backup, raw, 0o644); err != nil {
			return fmt.Errorf("could not back up %s: %v", m.p.SambaConf, err)
		}
	}
	var b bytes.Buffer
	b.Write(raw)
	if len(raw) > 0 && raw[len(raw)-1] != '\n' {
		b.WriteByte('\n')
	}
	b.WriteString("\n# Added by FELDCORE: shares it manages live in the included file.\n")
	b.WriteString(want + "\n")
	if err := writeAtomic(m.p.SambaConf, b.Bytes(), 0o644); err != nil {
		return fmt.Errorf("could not update %s: %v", m.p.SambaConf, err)
	}
	m.log.Info("added FELDCORE include to samba configuration", "file", m.p.SambaConf, "backup", backup)
	return nil
}

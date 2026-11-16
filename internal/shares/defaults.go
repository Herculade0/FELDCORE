package shares

import (
	"context"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

// defaultShare is one of the user shares Unraid creates for Docker and VMs.
//
// Unraid's own documentation lists exactly these four: appdata and system when
// Docker is enabled, domains and isos (plus system) when the VM manager is
// enabled. They are created as public but not exported. media and data are
// not default shares; people add those themselves.
type defaultShare struct {
	Name    string
	Comment string
}

var defaultShares = []defaultShare{
	{"appdata", "Application data"},
	{"system", "Docker and VM system files"},
	{"domains", "Virtual machine disks"},
	{"isos", "ISO images"},
}

// EnsureShares creates the named default shares (appdata, system, domains,
// isos). Unknown names are ignored. The wizard uses this so the user can
// untick some of them. Nil names is not used; pass defaultShares via
// EnsureDefaults for the full set.
func (m *Manager) EnsureShares(ctx context.Context, names []string) {
	m.ensure(ctx, filterDefaults(names))
}

func filterDefaults(names []string) []defaultShare {
	byName := map[string]defaultShare{}
	for _, d := range defaultShares {
		byName[d.Name] = d
	}
	var out []defaultShare
	seen := map[string]bool{}
	for _, n := range names {
		n = strings.ToLower(strings.TrimSpace(n))
		if seen[n] {
			continue
		}
		if d, ok := byName[n]; ok {
			out = append(out, d)
			seen[n] = true
		}
	}
	return out
}

// EnsureDefaults creates Unraid's default shares when they are missing.
//
// It is idempotent. A share FELDCORE already has is left completely alone,
// including its export settings. A folder that already exists under the
// shares root is adopted as the share and its contents are not modified.
// Without root, or when the shares root cannot be created, it logs and
// returns; the daemon keeps running.
func (m *Manager) EnsureDefaults(ctx context.Context) {
	m.ensure(ctx, defaultShares)
}

func (m *Manager) ensure(ctx context.Context, wanted []defaultShare) {
	if m.asRoot == nil || !m.asRoot() {
		m.log.Info("skipping default shares: FELDCORE is not running as root, so it cannot manage shares")
		return
	}
	if len(wanted) == 0 {
		return
	}

	m.mu.Lock()
	defer m.mu.Unlock()

	added := 0
	err := m.st.Update(func(s *store.State) error {
		for _, d := range wanted {
			if hasShare(s.Shares, d.Name) {
				continue
			}
			if err := m.makeDir(d.Name); err != nil {
				// One bad path (a file where the folder should be, a
				// permissions problem) must not stop the others.
				m.log.Warn("skipping default share", "name", d.Name, "err", err)
				continue
			}
			s.Shares = append(s.Shares, store.Share{
				Name:      d.Name,
				Comment:   d.Comment,
				CreatedAt: time.Now().UTC(),
				// Not exported. Unraid's predefined shares are public but
				// Export is No; leaving both protocols off is that default,
				// and nothing is shared until someone turns it on.
				SMB: store.SMB{Enabled: false, Access: store.SMBPublic, Users: []string{}},
				NFS: store.NFS{Enabled: false},
			})
			added++
			m.log.Info("default share ready", "name", d.Name, "path", m.dir(d.Name))
		}
		return nil
	})
	if err != nil {
		m.log.Warn("could not save default shares", "err", err)
		return
	}
	if added > 0 {
		m.applyLocked(ctx)
	}
}

func hasShare(shares []store.Share, name string) bool {
	for _, sh := range shares {
		if strings.EqualFold(sh.Name, name) {
			return true
		}
	}
	return false
}

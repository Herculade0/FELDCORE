package shares

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

// RenderNFS produces the exports file for every NFS-enabled share.
func RenderNFS(shares []store.Share, root string, id identity) string {
	enabled := nfsEnabled(shares)
	sort.Slice(enabled, func(i, j int) bool {
		return strings.ToLower(enabled[i].Name) < strings.ToLower(enabled[j].Name)
	})

	var b strings.Builder
	b.WriteString(managedHeader)
	for _, sh := range enabled {
		mode := "rw"
		if sh.NFS.ReadOnly {
			mode = "ro"
		}
		// Every client is squashed onto the share identity so files written
		// over NFS match those written over SMB.
		opts := fmt.Sprintf("%s,sync,no_subtree_check,all_squash,anonuid=%d,anongid=%d",
			mode, id.UID, id.GID)

		path := root + "/" + sh.Name
		if strings.ContainsAny(path, " \t") {
			path = `"` + path + `"`
		}
		b.WriteString(path)
		for _, client := range strings.Fields(sh.NFS.Clients) {
			fmt.Fprintf(&b, " %s(%s)", client, opts)
		}
		b.WriteByte('\n')
	}
	return b.String()
}

func nfsEnabled(shares []store.Share) []store.Share {
	var out []store.Share
	for _, sh := range shares {
		if sh.NFS.Enabled {
			out = append(out, sh)
		}
	}
	return out
}

// applyNFS writes the exports drop-in and re-exports.
func (m *Manager) applyNFS(ctx context.Context, shares []store.Share) error {
	wanted := len(nfsEnabled(shares)) > 0
	if !wanted && !fileExists(m.p.ExportsFile) {
		return nil
	}
	if _, ok := findBin("exportfs"); !ok {
		if wanted {
			return errors.New("the NFS server is not installed")
		}
		return nil
	}
	if os.Geteuid() != 0 {
		return errNotRoot
	}

	content := []byte(RenderNFS(shares, m.p.SharesRoot, m.id))
	if err := writeAtomic(m.p.ExportsFile, content, 0o644); err != nil {
		return fmt.Errorf("could not write %s: %v", m.p.ExportsFile, err)
	}

	if wanted && !nfsActive(ctx) {
		if err := startService(ctx, "nfs-server", "nfs-kernel-server"); err != nil {
			return fmt.Errorf("the NFS server could not be started: %v", err)
		}
	}
	if nfsActive(ctx) {
		if _, err := run(ctx, 30*time.Second, "", "exportfs", "-ra"); err != nil {
			return fmt.Errorf("the NFS server rejected the exports: %v", err)
		}
	}
	return nil
}

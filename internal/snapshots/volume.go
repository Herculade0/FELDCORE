package snapshots

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// shell runs the filesystem tools used to detect btrfs and zfs.
type shell interface {
	Run(ctx context.Context, name string, args ...string) ([]byte, error)
	FSType(path string) (string, error)
}

type osShell struct{}

func (osShell) Run(ctx context.Context, name string, args ...string) ([]byte, error) {
	return exec.CommandContext(ctx, name, args...).CombinedOutput()
}

func (osShell) FSType(path string) (string, error) {
	out, err := exec.Command("stat", "-f", "-c", "%T", path).Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

// capture copies one host folder. btrfs and zfs are used when the folder is a
// subvolume or its own dataset; anything else, including a failed snapshot,
// falls back to a tar.zst archive.
func capture(ctx context.Context, sh shell, src, snapDir, id string, index int) (Volume, error) {
	vol := Volume{Host: src}
	fst, err := sh.FSType(src)
	if err == nil && fst == "btrfs" {
		if _, showErr := sh.Run(ctx, "btrfs", "subvolume", "show", src); showErr == nil {
			dest := filepath.Join(snapDir, fmt.Sprintf("btrfs-%d", index))
			if _, snapErr := sh.Run(ctx, "btrfs", "subvolume", "snapshot", "-r", src, dest); snapErr == nil {
				vol.Method = "btrfs"
				vol.FSSnapshot = dest
				return vol, nil
			}
		}
	}
	if err == nil && fst == "zfs" {
		out, listErr := sh.Run(ctx, "zfs", "list", "-H", "-o", "name,mountpoint", src)
		if listErr == nil {
			if dataset := datasetFor(string(out), src); dataset != "" {
				snap := dataset + "@feldcore-" + id
				if _, snapErr := sh.Run(ctx, "zfs", "snapshot", snap); snapErr == nil {
					vol.Method = "zfs"
					vol.FSSnapshot = snap
					return vol, nil
				}
			}
		}
	}
	rel := fmt.Sprintf("appdata-%d.tar.zst", index)
	n, err := writeTarZst(src, filepath.Join(snapDir, rel))
	if err != nil {
		return Volume{}, err
	}
	vol.Method = "tar.zst"
	vol.Archive = rel
	vol.Bytes = n
	return vol, nil
}

func datasetFor(out, mount string) string {
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) >= 2 && fields[1] == mount {
			return fields[0]
		}
	}
	return ""
}

func restoreVolume(ctx context.Context, sh shell, snapDir string, vol Volume) error {
	if vol.Skipped != "" || vol.Host == "" {
		return nil
	}
	switch vol.Method {
	case "btrfs":
		fresh := vol.Host + ".feldcore-restore"
		_ = os.RemoveAll(fresh)
		if _, err := sh.Run(ctx, "btrfs", "subvolume", "snapshot", vol.FSSnapshot, fresh); err != nil {
			return fmt.Errorf("could not restore the btrfs snapshot of %s", vol.Host)
		}
		if err := swapPath(fresh, vol.Host); err != nil {
			return err
		}
		_, _ = sh.Run(ctx, "btrfs", "subvolume", "delete", vol.Host+".feldcore-prev")
		return nil
	case "zfs":
		if _, err := sh.Run(ctx, "zfs", "rollback", "-r", vol.FSSnapshot); err != nil {
			return fmt.Errorf("could not roll %s back to %s", vol.Host, vol.FSSnapshot)
		}
		return nil
	default:
		return restoreTree(filepath.Join(snapDir, vol.Archive), vol.Host)
	}
}

func deleteVolume(ctx context.Context, sh shell, vol Volume) {
	switch vol.Method {
	case "btrfs":
		_, _ = sh.Run(ctx, "btrfs", "subvolume", "delete", vol.FSSnapshot)
	case "zfs":
		_, _ = sh.Run(ctx, "zfs", "destroy", vol.FSSnapshot)
	}
}

func swapPath(fresh, dest string) error {
	prev := dest + ".feldcore-prev"
	_ = os.RemoveAll(prev)
	if _, err := os.Stat(dest); err == nil {
		if err := os.Rename(dest, prev); err != nil {
			return fmt.Errorf("could not move %s aside: %v", dest, err)
		}
	}
	if err := os.Rename(fresh, dest); err != nil {
		_ = os.Rename(prev, dest)
		return err
	}
	return nil
}

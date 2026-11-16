package backup

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"strings"
)

// vmPrep is the consistent set of VM definitions and disks for one backup.
type vmPrep struct {
	Paths   []string
	Notes   []string
	Cleanup func(context.Context) error
}

var domainNameRe = regexp.MustCompile(`^[A-Za-z0-9_][A-Za-z0-9._+-]{0,79}$`)

type diskRef struct {
	Target string
	Source string
}

// prepareVMs copies domain XML and a consistent view of each disk.
// A running guest is snapshotted (disk-only) when libvirt allows it, and
// otherwise paused for the copy. Without libvirt, the domains folder is
// included as it sits.
func prepareVMs(ctx context.Context, cmd Commander, domains, staging string) (vmPrep, error) {
	noop := func(context.Context) error { return nil }
	if cmd == nil {
		return vmPrep{Cleanup: noop}, nil
	}
	if _, err := cmd.Look("virsh"); err != nil {
		if dirExists(domains) {
			return vmPrep{
				Paths:   []string{domains},
				Notes:   []string{"libvirt is not installed, so VM disks were copied as they are, without a pause or a snapshot."},
				Cleanup: noop,
			}, nil
		}
		return vmPrep{Notes: []string{"No virtual machine disks were found."}, Cleanup: noop}, nil
	}
	if err := os.MkdirAll(staging, 0o700); err != nil {
		return vmPrep{}, op("Could not prepare a folder for the VM definitions.")
	}
	out, err := cmd.Run(ctx, "virsh", "-r", "list", "--all", "--name")
	if err != nil {
		if dirExists(domains) {
			return vmPrep{
				Paths:   []string{domains},
				Notes:   []string{"The VM list could not be read, so the domains folder was copied without a snapshot."},
				Cleanup: func(context.Context) error { return os.RemoveAll(staging) },
			}, nil
		}
		return vmPrep{Notes: []string{"The VM list could not be read, so VM disks were left out."}, Cleanup: func(context.Context) error { return os.RemoveAll(staging) }}, nil
	}
	var paths []string
	var notes []string
	var cleanups []func(context.Context) error
	xmlDir := filepath.Join(staging, "definitions")
	if err := os.MkdirAll(xmlDir, 0o700); err != nil {
		return vmPrep{}, op("Could not prepare a folder for the VM definitions.")
	}
	for _, name := range strings.Split(out, "\n") {
		name = strings.TrimSpace(name)
		if name == "" {
			continue
		}
		if !domainNameRe.MatchString(name) {
			notes = append(notes, "Skipped a virtual machine with an unusual name.")
			continue
		}
		xml, err := cmd.Run(ctx, "virsh", "dumpxml", "--inactive", name)
		if err == nil && strings.TrimSpace(xml) != "" {
			file := filepath.Join(xmlDir, name+".xml")
			if err := os.WriteFile(file, []byte(xml), 0o600); err == nil {
				paths = append(paths, file)
			}
		}
		state, _ := cmd.Run(ctx, "virsh", "domstate", name)
		disks := parseBlklist(mustRun(ctx, cmd, "virsh", "domblklist", name, "--details"))
		state = strings.ToLower(strings.TrimSpace(state))
		if strings.Contains(state, "shut off") || state == "" {
			paths = append(paths, existingDisks(disks)...)
			continue
		}
		snap := "fc" + shortID()
		_, snapErr := cmd.Run(ctx, "virsh", "snapshot-create-as", name, snap, "--disk-only", "--atomic", "--quiesce", "--no-metadata")
		if snapErr != nil {
			_, snapErr = cmd.Run(ctx, "virsh", "snapshot-create-as", name, snap, "--disk-only", "--atomic", "--no-metadata")
		}
		if snapErr == nil {
			overlays := parseBlklist(mustRun(ctx, cmd, "virsh", "domblklist", name, "--details"))
			if len(overlays) == 0 {
				overlays = disks
			}
			for _, d := range overlays {
				backing := backingFile(ctx, cmd, d.Source)
				if backing != "" && fileExists(backing) {
					paths = append(paths, backing)
				} else if fileExists(d.Source) {
					paths = append(paths, d.Source)
					notes = append(notes, name+" was snapshotted, but the consistent disk could not be named, so the new overlay was copied.")
				}
				target := d.Target
				cleanups = append(cleanups, func(ctx context.Context) error {
					_, err := cmd.Run(ctx, "virsh", "blockcommit", name, target, "--active", "--pivot")
					if err != nil {
						return op(name + " was copied, but its disk snapshot could not be merged back. Check that virtual machine.")
					}
					return nil
				})
			}
			notes = append(notes, name+" was disk-snapshotted so the copy is a consistent disk.")
			continue
		}
		if _, err := cmd.Run(ctx, "virsh", "suspend", name); err != nil {
			notes = append(notes, name+" was running and could not be paused, so its disks were left out.")
			continue
		}
		dom := name
		cleanups = append(cleanups, func(ctx context.Context) error {
			_, err := cmd.Run(ctx, "virsh", "resume", dom)
			if err != nil {
				return op(dom + " was paused for the copy and could not be resumed.")
			}
			return nil
		})
		paths = append(paths, existingDisks(disks)...)
		notes = append(notes, name+" was paused so the copy is a consistent disk.")
	}
	if len(paths) == 0 && len(notes) == 0 {
		notes = append(notes, "No virtual machine disks were found.")
	}
	return vmPrep{
		Paths: uniqueStrings(paths),
		Notes: notes,
		Cleanup: func(ctx context.Context) error {
			var err error
			for _, fn := range cleanups {
				if e := fn(ctx); e != nil && err == nil {
					err = e
				}
			}
			_ = os.RemoveAll(staging)
			return err
		},
	}, nil
}

func mustRun(ctx context.Context, cmd Commander, name string, args ...string) string {
	out, err := cmd.Run(ctx, name, args...)
	if err != nil {
		return ""
	}
	return out
}

func parseBlklist(out string) []diskRef {
	var disks []diskRef
	for _, line := range strings.Split(out, "\n") {
		fields := strings.Fields(line)
		if len(fields) < 4 {
			continue
		}
		if fields[0] != "file" || fields[1] != "disk" {
			continue
		}
		src := fields[len(fields)-1]
		target := fields[2]
		if src == "-" || src == "" || !filepath.IsAbs(src) || strings.ContainsAny(src, "\n\r") {
			continue
		}
		disks = append(disks, diskRef{Target: target, Source: src})
	}
	return disks
}

func existingDisks(disks []diskRef) []string {
	var out []string
	for _, d := range disks {
		if fileExists(d.Source) {
			out = append(out, d.Source)
		}
	}
	return out
}

func backingFile(ctx context.Context, cmd Commander, overlay string) string {
	if overlay == "" {
		return ""
	}
	if _, err := cmd.Look("qemu-img"); err != nil {
		return ""
	}
	out, err := cmd.Run(ctx, "qemu-img", "info", "--output=json", "--force-share", overlay)
	if err != nil {
		return ""
	}
	var info struct {
		Backing string `json:"backing-filename"`
	}
	if json.Unmarshal([]byte(out), &info) != nil || info.Backing == "" {
		return ""
	}
	if filepath.IsAbs(info.Backing) {
		return info.Backing
	}
	return filepath.Join(filepath.Dir(overlay), info.Backing)
}

func shortID() string {
	id, err := newDestID()
	if err != nil {
		return "backup"
	}
	if len(id) > 8 {
		return id[:8]
	}
	return id
}

func fileExists(p string) bool {
	st, err := os.Stat(p)
	return err == nil && st.Mode().IsRegular()
}

func dirExists(p string) bool {
	if strings.TrimSpace(p) == "" {
		return false
	}
	st, err := os.Stat(p)
	return err == nil && st.IsDir()
}

func uniqueStrings(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		if s == "" || seen[s] {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	return out
}

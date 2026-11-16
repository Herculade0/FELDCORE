package osupdate

import (
	"os"
	"path/filepath"
	"strings"

	"golang.org/x/sys/unix"
)

// SlotInfo is one A/B root slot.
type SlotInfo struct {
	ID        string `json:"id"`
	Version   string `json:"version,omitempty"`
	Booted    bool   `json:"booted"`
	TriesLeft *int   `json:"triesLeft,omitempty"`
	Good      bool   `json:"good"`
	Present   bool   `json:"present"`
}

func (s *Service) espDir() string {
	if s.ESP != "" {
		return s.ESP
	}
	return s.join("efi")
}

func (s *Service) dataDir() string {
	if s.DataDir != "" {
		return s.DataDir
	}
	return s.join("var", "lib", "feldcore")
}

func (s *Service) join(parts ...string) string {
	root := s.Root
	if root == "" || root == "/" {
		return filepath.Join(append([]string{"/"}, parts...)...)
	}
	return filepath.Join(append([]string{root}, parts...)...)
}

func (s *Service) readLayout() layout {
	rel := readOSRelease(s.join("etc", "feldcore-os-release"))
	cmd, _ := os.ReadFile(s.join("proc", "cmdline"))
	slot := slotFromCmdline(string(cmd))
	out := layout{
		Appliance:     rel["ID"] == "feldcore",
		Version:       firstNonEmpty(rel["VERSION_ID"], rel["VERSION"]),
		DaemonVersion: s.DaemonVersion,
		Kernel:        s.kernel(rel["KERNEL"]),
		Slot:          slot,
		SecureBoot:    "off",
	}
	entries := s.readEntries()
	loader, _ := os.ReadFile(filepath.Join(s.espDir(), "loader", "loader.conf"))
	def := DefaultName(string(loader))
	if next, ok := NextBoot(def, entries); ok {
		out.NextBoot = next.Slot
		out.NextVersion = next.Version
		out.NextTries = next.TriesLeft
	}
	for _, id := range []string{"a", "b"} {
		info := SlotInfo{ID: id, Booted: id == slot, Good: true}
		dev := s.slotDevice(id)
		if st, err := os.Stat(dev); err == nil && !st.IsDir() {
			info.Present = true
		}
		best := bestEntry(entries, id)
		if best.Name != "" {
			info.Version = best.Version
			info.TriesLeft = best.TriesLeft
			info.Good = best.Good()
		}
		out.Slots = append(out.Slots, info)
	}
	if next, ok := NextBoot(def, entries); ok {
		for i := range out.Slots {
			info := out.Slots[i]
			if info.ID == next.Slot || info.Version == "" {
				continue
			}
			if next.Version != "" && compareVersion(info.Version, next.Version) > 0 {
				continue
			}
			cp := info
			out.Previous = &cp
			break
		}
	}
	return out
}

type layout struct {
	Appliance     bool
	Version       string
	DaemonVersion string
	Kernel        string
	Slot          string
	SecureBoot    string
	Slots         []SlotInfo
	Previous      *SlotInfo
	NextBoot      string
	NextVersion   string
	NextTries     *int
}

func (s *Service) kernel(fromRelease string) string {
	if s.Root != "" && s.Root != "/" {
		if b, err := os.ReadFile(s.join("proc", "sys", "kernel", "osrelease")); err == nil {
			return strings.TrimSpace(string(b))
		}
		return fromRelease
	}
	var u unix.Utsname
	if err := unix.Uname(&u); err == nil {
		return cString(u.Release[:])
	}
	return fromRelease
}

func cString(b []byte) string {
	for i, c := range b {
		if c == 0 {
			return string(b[:i])
		}
	}
	return string(b)
}

func (s *Service) slotDevice(id string) string {
	switch id {
	case "a":
		if s.SlotA != "" {
			return s.SlotA
		}
	case "b":
		if s.SlotB != "" {
			return s.SlotB
		}
	}
	return s.join("dev", "disk", "by-label", "feldcore-"+id)
}

func (s *Service) readEntries() []Entry {
	dir := filepath.Join(s.espDir(), "loader", "entries")
	list, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []Entry
	for _, e := range list {
		if e.IsDir() {
			continue
		}
		ent, ok := ParseEntryName(e.Name())
		if !ok {
			continue
		}
		body, err := os.ReadFile(filepath.Join(dir, e.Name()))
		if err != nil {
			continue
		}
		out = append(out, ParseEntryBody(ent, string(body)))
	}
	return out
}

func bestEntry(entries []Entry, slot string) Entry {
	var found []Entry
	for _, e := range entries {
		if e.Slot == slot {
			found = append(found, e)
		}
	}
	if len(found) == 0 {
		return Entry{}
	}
	// Prefer a blessed entry, then the one with the most tries left.
	best := found[0]
	for _, e := range found[1:] {
		if entryBetter(e, best) {
			best = e
		}
	}
	return best
}

func entryBetter(a, b Entry) bool {
	if a.Good() != b.Good() {
		return a.Good()
	}
	if (a.TriesLeft == nil) != (b.TriesLeft == nil) {
		return a.TriesLeft == nil
	}
	if a.TriesLeft != nil && b.TriesLeft != nil && *a.TriesLeft != *b.TriesLeft {
		return *a.TriesLeft > *b.TriesLeft
	}
	return compareVersion(a.Version, b.Version) > 0
}

func readOSRelease(path string) map[string]string {
	b, err := os.ReadFile(path)
	if err != nil {
		return map[string]string{}
	}
	out := map[string]string{}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		v = strings.Trim(strings.TrimSpace(v), `"'`)
		out[k] = v
	}
	return out
}

func slotFromCmdline(cmd string) string {
	for _, f := range strings.Fields(cmd) {
		if v, ok := strings.CutPrefix(f, "feldcore.slot="); ok && (v == "a" || v == "b") {
			return v
		}
		if v, ok := strings.CutPrefix(f, "root=LABEL="); ok {
			switch v {
			case "feldcore-a":
				return "a"
			case "feldcore-b":
				return "b"
			}
		}
	}
	return ""
}

func firstNonEmpty(v ...string) string {
	for _, s := range v {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func deviceSize(path string) (int64, error) {
	f, err := os.Open(path)
	if err != nil {
		return 0, err
	}
	defer f.Close()
	n, err := f.Seek(0, ioSeekEnd)
	return n, err
}

const ioSeekEnd = 2

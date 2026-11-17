package osupdate

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// BootTries is how many times a newly staged image may fail before
// systemd-boot stops preferring it. systemd-boot renames
// feldcore-b+3.conf to feldcore-b+2-1.conf, then +1-2, then +0-3.
// An entry with zero tries left sorts after a good entry, so the
// previous image is selected.
const BootTries = 3

// Entry is one systemd-boot type-1 loader entry for a slot.
type Entry struct {
	Name      string
	Slot      string
	Version   string
	SortKey   string
	TriesLeft *int // nil means the counter was removed and the boot is good
	TriesDone int
	Body      string
}

var entryNameRE = regexp.MustCompile(`^feldcore-([ab])(?:\+(\d+)(?:-(\d+))?)?\.conf$`)

// ParseEntryName reads a boot-counting file name.
func ParseEntryName(name string) (Entry, bool) {
	m := entryNameRE.FindStringSubmatch(name)
	if m == nil {
		return Entry{}, false
	}
	e := Entry{Name: name, Slot: m[1], SortKey: "feldcore"}
	if m[2] != "" {
		n, err := strconv.Atoi(m[2])
		if err != nil {
			return Entry{}, false
		}
		e.TriesLeft = &n
		if m[3] != "" {
			d, err := strconv.Atoi(m[3])
			if err != nil {
				return Entry{}, false
			}
			e.TriesDone = d
		}
	}
	return e, true
}

// EntryName is the loader file for a slot. tries > 0 adds the boot counter.
func EntryName(slot string, tries int) string {
	if tries > 0 {
		return fmt.Sprintf("feldcore-%s+%d.conf", slot, tries)
	}
	return fmt.Sprintf("feldcore-%s.conf", slot)
}

// consoleOptions keeps informational printk off the login screen.
// quiet on its own is log level 4; loglevel=3 follows it on the command
// line and wins, so KERN_INFO lines (bridge, XFRM, EVM) do not paint over
// tty1. ttyS0 stays last, which makes /dev/console the serial port.
// Debian 13's kernel has no per-console log level, so this applies to
// both consoles. Journal messages still go to ttyS0 via journald.
// The boot counter is the entry file name, not this line.
const consoleOptions = "quiet loglevel=3 console=tty0 console=ttyS0,115200"

// EntryBody is the type-1 entry systemd-boot reads.
func EntryBody(slot, version string) string {
	return fmt.Sprintf(`title FELDCORE %s
version %s
sort-key feldcore
linux /feldcore/%s/vmlinuz
initrd /feldcore/%s/initrd.img
options root=LABEL=feldcore-%s ro %s feldcore.slot=%s feldcore.version=%s
`, version, version, slot, slot, slot, consoleOptions, slot, version)
}

// ParseEntryBody fills Version and SortKey from the entry text.
func ParseEntryBody(e Entry, body string) Entry {
	e.Body = body
	for _, line := range strings.Split(body, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok {
			continue
		}
		val = strings.TrimSpace(val)
		switch key {
		case "version":
			e.Version = val
		case "sort-key":
			e.SortKey = val
		}
	}
	if e.SortKey == "" {
		e.SortKey = "feldcore"
	}
	return e
}

// Good reports whether the entry is not on its last failed try.
func (e Entry) Good() bool {
	return e.TriesLeft == nil || *e.TriesLeft > 0
}

// Bootable is an entry systemd-boot will still prefer over a failed one.
func (e Entry) Bootable() bool { return e.Good() }

// NextBoot picks the entry systemd-boot would boot.
// A default that still names a bootable entry wins. A default that names a
// failed entry, or a file that the counter rename has already replaced, does
// not: the newest bootable entry is used, and entries with zero tries sort last.
func NextBoot(defaultName string, entries []Entry) (Entry, bool) {
	if len(entries) == 0 {
		return Entry{}, false
	}
	if defaultName != "" {
		for _, e := range entries {
			if e.Name == defaultName && e.Bootable() {
				return e, true
			}
		}
	}
	sorted := append([]Entry(nil), entries...)
	sort.SliceStable(sorted, func(i, j int) bool {
		return entryBefore(sorted[i], sorted[j])
	})
	for _, e := range sorted {
		if e.Bootable() {
			return e, true
		}
	}
	return sorted[0], true
}

func entryBefore(a, b Entry) bool {
	aBad := a.TriesLeft != nil && *a.TriesLeft == 0
	bBad := b.TriesLeft != nil && *b.TriesLeft == 0
	if aBad != bBad {
		return !aBad
	}
	if cmp := compareVersion(a.Version, b.Version); cmp != 0 {
		return cmp > 0
	}
	if cmp := strings.Compare(a.Name, b.Name); cmp != 0 {
		return cmp > 0
	}
	return false
}

// compareVersion returns -1, 0, or 1 like strings but with numeric pieces,
// so 0.10 sorts after 0.9.
func compareVersion(a, b string) int {
	as := versionParts(a)
	bs := versionParts(b)
	n := len(as)
	if len(bs) > n {
		n = len(bs)
	}
	for i := 0; i < n; i++ {
		var ap, bp string
		if i < len(as) {
			ap = as[i]
		}
		if i < len(bs) {
			bp = bs[i]
		}
		an, aErr := strconv.Atoi(ap)
		bn, bErr := strconv.Atoi(bp)
		if aErr == nil && bErr == nil {
			if an != bn {
				if an < bn {
					return -1
				}
				return 1
			}
			continue
		}
		if ap != bp {
			if ap < bp {
				return -1
			}
			return 1
		}
	}
	return 0
}

func versionParts(v string) []string {
	return strings.FieldsFunc(v, func(r rune) bool {
		return r == '.' || r == '-' || r == '+' || r == '_'
	})
}

// ClearDefault drops a default line so version order selects the next boot.
func ClearDefault(loader string) string {
	return rewriteLoader(loader, "")
}

// SetDefault pins the next boot to one entry file name.
func SetDefault(loader, name string) string {
	return rewriteLoader(loader, name)
}

func rewriteLoader(loader, name string) string {
	var out []string
	for _, line := range strings.Split(strings.ReplaceAll(loader, "\r\n", "\n"), "\n") {
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "default ") || trim == "default" {
			continue
		}
		out = append(out, line)
	}
	if name != "" {
		out = append(out, "default "+name)
	}
	return strings.Join(out, "\n") + "\n"
}

// DefaultName reads the default entry from loader.conf.
func DefaultName(loader string) string {
	for _, line := range strings.Split(loader, "\n") {
		key, val, ok := strings.Cut(strings.TrimSpace(line), " ")
		if ok && key == "default" {
			return strings.TrimSpace(val)
		}
	}
	return ""
}

// OtherSlot returns the slot that is not booted.
func OtherSlot(slot string) string {
	if slot == "a" {
		return "b"
	}
	if slot == "b" {
		return "a"
	}
	return ""
}

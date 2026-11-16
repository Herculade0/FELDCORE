package backup

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ArchivePrefix is the file name rclone uploads.
const ArchivePrefix = "feldcore-"

var archiveName = regexp.MustCompile(`^feldcore-\d{8}T\d{6}Z\.tar\.gz$`)

// Source is one tree or file to put in the archive.
type Source struct {
	Path string // absolute path on disk
	Name string // path inside the archive
	Note string // why it was skipped, when Path is empty
}

// SelectSources decides what a backup contains. The config file is always
// included when it exists. Snapshot archives and appdata are included only
// when asked and the folder is there.
func SelectSources(dataDir, appdata string, includeSnap, includeApp bool, exists func(string) bool) []Source {
	var out []Source
	state := filepath.Join(dataDir, "state.json")
	if exists(state) {
		out = append(out, Source{Path: state, Name: "state.json"})
	} else {
		out = append(out, Source{Name: "state.json", Note: "No FELDCORE state file was found."})
	}
	sec := filepath.Join(dataDir, "secrets")
	if exists(sec) {
		out = append(out, Source{Path: sec, Name: "secrets"})
	}
	snap := filepath.Join(dataDir, "snapshots")
	if includeSnap {
		if exists(snap) {
			out = append(out, Source{Path: snap, Name: "snapshots"})
		} else {
			out = append(out, Source{Name: "snapshots", Note: "No snapshot archives folder was found, so none were included."})
		}
	}
	if includeApp {
		if appdata != "" && exists(appdata) {
			out = append(out, Source{Path: appdata, Name: "appdata"})
		} else {
			out = append(out, Source{Name: "appdata", Note: "No appdata folder was found, so it was not included."})
		}
	}
	// Compose stacks live in <data-dir>/compose. See docs/COMPOSE.md.
	// Each stack is a subdirectory with docker-compose.yml and an optional .env.
	comp := filepath.Join(dataDir, "compose")
	if exists(comp) {
		out = append(out, Source{Path: comp, Name: "compose"})
	}
	return out
}

// AppdataPath is the folder a backup looks in when the setting is empty.
func AppdataPath(explicit, sharesRoot string) string {
	if strings.TrimSpace(explicit) != "" {
		return explicit
	}
	if strings.TrimSpace(sharesRoot) == "" {
		sharesRoot = "/srv/feldcore"
	}
	return filepath.Join(sharesRoot, "appdata")
}

// ArchiveName is the object name for a backup taken at t.
func ArchiveName(t time.Time) string {
	return ArchivePrefix + t.UTC().Format("20060102T150405Z") + ".tar.gz"
}

// Prune returns archive names to delete so that keep of the newest remain.
// keep of 0 or less deletes nothing. Names that are not FELDCORE archives
// are ignored, so a shared Proton Drive folder is left alone.
func Prune(names []string, keep int) []string {
	var own []string
	for _, n := range names {
		base := n
		if i := strings.LastIndex(base, "/"); i >= 0 {
			base = base[i+1:]
		}
		if archiveName.MatchString(base) {
			own = append(own, base)
		}
	}
	sort.Sort(sort.Reverse(sort.StringSlice(own)))
	if keep <= 0 || len(own) <= keep {
		return nil
	}
	return own[keep:]
}

// Due reports whether a daily schedule should run now. An empty schedule
// never runs by itself.
func Due(schedule string, last, now time.Time) (bool, error) {
	if strings.TrimSpace(schedule) == "" {
		return false, nil
	}
	h, m, err := parseClock(schedule)
	if err != nil {
		return false, err
	}
	slot := time.Date(now.Year(), now.Month(), now.Day(), h, m, 0, 0, now.Location())
	if now.Before(slot) {
		return false, nil
	}
	return last.IsZero() || last.Before(slot), nil
}

func parseClock(schedule string) (int, int, error) {
	parts := strings.Split(strings.TrimSpace(schedule), ":")
	if len(parts) != 2 {
		return 0, 0, input("The schedule is a time of day, like 03:00, or leave it empty to back up only when you ask.")
	}
	h, errH := strconv.Atoi(parts[0])
	m, errM := strconv.Atoi(parts[1])
	if errH != nil || errM != nil || h < 0 || h > 23 || m < 0 || m > 59 || len(parts[1]) != 2 {
		return 0, 0, input("The schedule is a time of day, like 03:00, or leave it empty to back up only when you ask.")
	}
	return h, m, nil
}

// CleanRemote checks the folder on Proton Drive. Empty becomes "feldcore".
func CleanRemote(path string) (string, error) {
	path = strings.Trim(strings.TrimSpace(path), "/")
	if path == "" {
		return "feldcore", nil
	}
	if strings.Contains(path, "..") || strings.ContainsAny(path, "\\\x00 ") {
		return "", input("The Proton Drive folder can use letters, numbers and / _ . -")
	}
	for _, part := range strings.Split(path, "/") {
		if part == "" || part == "." || part == ".." {
			return "", input("The Proton Drive folder can use letters, numbers and / _ . -")
		}
	}
	if len(path) > 200 {
		return "", input("That Proton Drive folder name is too long.")
	}
	return path, nil
}

// Notes lists the human reasons a source was skipped.
func Notes(sources []Source) []string {
	var out []string
	for _, s := range sources {
		if s.Note != "" {
			out = append(out, s.Note)
		}
	}
	return out
}

func remoteFile(dir, name string) string {
	return fmt.Sprintf("feldcore:%s/%s", dir, name)
}

package backup

import (
	"path"
	"sort"
	"strings"
	"time"
)

// Entry is one row in the restore browser.
type Entry struct {
	Name  string    `json:"name"`
	Type  string    `json:"type"`
	Path  string    `json:"path"`
	Size  int64     `json:"size"`
	Mtime time.Time `json:"mtime,omitempty"`
}

// Browse is one directory of a snapshot.
type Browse struct {
	Path      string  `json:"path"`
	Parent    string  `json:"parent,omitempty"`
	Truncated bool    `json:"truncated,omitempty"`
	Entries   []Entry `json:"entries"`
}

const maxBrowse = 2000

// listDir keeps the immediate children of dir. Snapshot paths use slashes.
// dir "" or "/" is the top of the snapshot.
func listDir(nodes []Node, dir string) Browse {
	if dir == "" || dir == "." {
		dir = "/"
	}
	dir = path.Clean(dir)
	if !strings.HasPrefix(dir, "/") {
		dir = "/" + dir
	}
	seen := map[string]Entry{}
	var order []string
	truncated := false
	for _, n := range nodes {
		p := path.Clean(n.Path)
		if p == "" || p == "." || p == "/" {
			continue
		}
		var rel string
		switch {
		case dir == "/":
			rel = strings.TrimPrefix(p, "/")
		case p == dir:
			continue
		case strings.HasPrefix(p, dir+"/"):
			rel = strings.TrimPrefix(p, dir+"/")
		default:
			continue
		}
		if rel == "" {
			continue
		}
		first, rest, _ := strings.Cut(rel, "/")
		if first == "" || first == "." || first == ".." {
			continue
		}
		child := "/" + first
		if dir != "/" {
			child = dir + "/" + first
		}
		if rest == "" {
			typ := n.Type
			if typ != "dir" && typ != "file" {
				typ = "file"
			}
			name := n.Name
			if name == "" {
				name = first
			}
			seen[child] = Entry{Name: name, Type: typ, Path: child, Size: n.Size, Mtime: n.Mtime}
			if !contains(order, child) {
				order = append(order, child)
			}
			continue
		}
		if _, ok := seen[child]; ok {
			continue
		}
		seen[child] = Entry{Name: first, Type: "dir", Path: child}
		order = append(order, child)
	}
	if len(order) > maxBrowse {
		truncated = true
		order = order[:maxBrowse]
	}
	entries := make([]Entry, 0, len(order))
	for _, p := range order {
		entries = append(entries, seen[p])
	}
	sort.Slice(entries, func(i, j int) bool {
		if (entries[i].Type == "dir") != (entries[j].Type == "dir") {
			return entries[i].Type == "dir"
		}
		return strings.ToLower(entries[i].Name) < strings.ToLower(entries[j].Name)
	})
	parent := ""
	if dir != "/" {
		parent = path.Dir(dir)
		if parent == "." {
			parent = "/"
		}
	}
	if entries == nil {
		entries = []Entry{}
	}
	return Browse{Path: dir, Parent: parent, Truncated: truncated, Entries: entries}
}

func contains(in []string, s string) bool {
	for _, v := range in {
		if v == s {
			return true
		}
	}
	return false
}

// cleanSnapPath checks a path a caller wants to restore. It must be absolute
// and must not be a glob.
func cleanSnapPath(p string) (string, error) {
	p = strings.TrimSpace(p)
	if p == "" || !strings.HasPrefix(p, "/") || strings.Contains(p, "\x00") {
		return "", input("Choose a file or folder from the backup.")
	}
	if strings.ContainsAny(p, "*?[]\n\r") {
		return "", input("That path cannot be restored.")
	}
	for _, part := range strings.Split(p, "/") {
		if part == ".." {
			return "", input("That path cannot be restored.")
		}
	}
	c := path.Clean(p)
	if c == "/" {
		return "", input("That path cannot be restored.")
	}
	return c, nil
}

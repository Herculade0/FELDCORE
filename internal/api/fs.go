package api

import (
	"net/http"
	"os"
	"path"
	"path/filepath"
	"strings"
)

// maxSuggestions caps how many folders one lookup returns.
const maxSuggestions = 40

type pathSuggestion struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// handleFSSuggest powers the host-path picker. With no query it offers the
// FELDCORE shares; once the query is an absolute path it lists the real
// folders that match, so nobody has to type a path from memory.
//
// It only ever returns directory names, never file names or contents.
func (s *Server) handleFSSuggest(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query().Get("q")

	shares := []pathSuggestion{}
	for _, f := range s.shares.Folders() {
		if q == "" || strings.HasPrefix(q, "/") && strings.HasPrefix(f.Path, q) ||
			!strings.HasPrefix(q, "/") && strings.Contains(strings.ToLower(f.Name), strings.ToLower(q)) {
			shares = append(shares, pathSuggestion{Name: f.Name, Path: f.Path})
		}
	}

	dirs := []pathSuggestion{}
	if strings.HasPrefix(q, "/") {
		dirs = listDirs(q)
	}
	writeJSON(w, http.StatusOK, map[string]any{"shares": shares, "dirs": dirs})
}

// listDirs returns the folders matching a partially typed absolute path.
// "/srv/" lists what is inside /srv; "/srv/fe" lists folders in /srv whose
// names start with "fe".
func listDirs(q string) []pathSuggestion {
	dir, prefix := q, ""
	if !strings.HasSuffix(q, "/") {
		dir, prefix = path.Split(q)
	}
	dir = filepath.Clean(dir)
	prefix = strings.ToLower(prefix)

	out := []pathSuggestion{}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return out
	}
	for _, e := range entries {
		name := e.Name()
		// Hidden folders only when the user has started typing a dot.
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(prefix, ".") {
			continue
		}
		if !strings.HasPrefix(strings.ToLower(name), prefix) {
			continue
		}
		full := filepath.Join(dir, name)
		isDir := e.IsDir()
		if e.Type()&os.ModeSymlink != 0 {
			if st, err := os.Stat(full); err == nil {
				isDir = st.IsDir()
			}
		}
		if !isDir {
			continue
		}
		out = append(out, pathSuggestion{Name: name, Path: full})
		if len(out) >= maxSuggestions {
			break
		}
	}
	return out
}

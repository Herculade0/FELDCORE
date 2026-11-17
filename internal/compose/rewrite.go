package compose

import (
	"path"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"
)

// unraidMount matches the paths Unraid and its Compose plugin write.
var unraidMount = regexp.MustCompile(`^/mnt/(user|user0|cache|disk[0-9]+)(?:/(.*))?$`)

// rewriteUnraid walks every scalar and maps /mnt/user/... onto this server.
// Volume strings keep the container path; only the host side is rewritten.
func rewriteUnraid(doc *yaml.Node, paths PathInfo) (int, []string, bool) {
	if doc == nil {
		return 0, nil, false
	}
	n := rewriteNode(doc, paths)
	if n == 0 {
		return 0, nil, false
	}
	return n, []string{"Unraid paths under /mnt/user, /mnt/cache and /mnt/disk were rewritten onto this server's shares."}, true
}

func rewriteNode(n *yaml.Node, paths PathInfo) int {
	if n == nil {
		return 0
	}
	count := 0
	if n.Kind == yaml.ScalarNode && n.Tag != "!!bool" && n.Tag != "!!int" && n.Tag != "!!float" {
		if next, ok := rewriteScalar(n.Value, paths); ok {
			n.Value = next
			n.Tag = "!!str"
			count++
		}
	}
	for _, c := range n.Content {
		count += rewriteNode(c, paths)
	}
	return count
}

func rewriteScalar(s string, paths PathInfo) (string, bool) {
	s = strings.TrimSpace(s)
	if s == "" || !strings.Contains(s, "/mnt/") {
		return s, false
	}
	if next, ok := rewriteUnraidPath(s, paths); ok {
		return next, true
	}
	if src, bind := shortVolume(s); bind {
		if next, ok := rewriteUnraidPath(src, paths); ok {
			return next + s[len(src):], true
		}
	}
	return s, false
}

func rewriteUnraidPath(p string, m PathInfo) (string, bool) {
	p = strings.TrimSpace(p)
	clean := path.Clean(p)
	sub := unraidMount.FindStringSubmatch(clean)
	if sub == nil {
		return p, false
	}
	root := strings.TrimSpace(m.SharesRoot)
	if root == "" {
		root = "/srv/feldcore"
	}
	rest := sub[2]
	if rest == "" {
		return root, true
	}
	top, tail, _ := strings.Cut(rest, "/")
	base := ""
	if m.Shares != nil {
		if sp, ok := m.Shares[strings.ToLower(top)]; ok && sp != "" {
			base = sp
		}
	}
	if base == "" {
		base = filepath.Join(root, top)
	}
	if tail == "" {
		return base, true
	}
	joined := filepath.Join(base, filepath.FromSlash(tail))
	rel, err := filepath.Rel(base, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return base, true
	}
	return joined, true
}

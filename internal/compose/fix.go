package compose

import (
	"path"
	"strings"

	"gopkg.in/yaml.v3"
)

// applyFixes removes the settings the security check can undo. The compose
// file is edited in place. It reports whether anything changed.
func applyFixes(doc *yaml.Node, fixes []Fix) bool {
	if doc == nil || len(fixes) == 0 || doc.Kind != yaml.DocumentNode || len(doc.Content) == 0 {
		return false
	}
	root := doc.Content[0]
	services := mappingValue(root, "services")
	if services == nil || services.Kind != yaml.MappingNode {
		return false
	}
	want := map[string]map[string]bool{}
	for _, f := range fixes {
		if want[f.Service] == nil {
			want[f.Service] = map[string]bool{}
		}
		want[f.Service][f.ID] = true
	}
	changed := false
	for i := 0; i+1 < len(services.Content); i += 2 {
		name := services.Content[i].Value
		ids := want[name]
		if len(ids) == 0 {
			continue
		}
		body := services.Content[i+1]
		if body.Kind == yaml.AliasNode && body.Alias != nil {
			body = body.Alias
		}
		if body.Kind != yaml.MappingNode {
			continue
		}
		if ids["privileged"] && deleteKey(body, "privileged") {
			changed = true
		}
		if (ids["host-network"] || ids["shared-bridge"]) && deleteKey(body, "network_mode") {
			changed = true
		}
		if ids["host-pid"] && deleteKey(body, "pid") {
			changed = true
		}
		if ids["host-ipc"] && deleteKey(body, "ipc") {
			changed = true
		}
		for id := range ids {
			capName, ok := strings.CutPrefix(id, "cap:")
			if !ok {
				continue
			}
			if dropCap(body, capName) {
				changed = true
			}
		}
		if ids["docker.sock"] || ids["mount:/"] || ids["mount:/etc"] || ids["mount:/root"] {
			if dropMounts(body, func(host string) bool {
				if ids["docker.sock"] && isSock(host) {
					return true
				}
				b := broadPath(host)
				return b != "" && ids["mount:"+b]
			}) {
				changed = true
			}
		}
	}
	return changed
}

func mappingValue(m *yaml.Node, key string) *yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return m.Content[i+1]
		}
	}
	return nil
}

func deleteKey(m *yaml.Node, key string) bool {
	if m == nil || m.Kind != yaml.MappingNode {
		return false
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			m.Content = append(m.Content[:i], m.Content[i+2:]...)
			return true
		}
	}
	return false
}

func dropCap(m *yaml.Node, cap string) bool {
	node := mappingValue(m, "cap_add")
	if node == nil {
		return false
	}
	cap = normCap(cap)
	switch node.Kind {
	case yaml.ScalarNode:
		kept := filterCaps(stringList(*node), cap)
		if len(kept) == len(stringList(*node)) {
			return false
		}
		if len(kept) == 0 {
			return deleteKey(m, "cap_add")
		}
		node.Value = strings.Join(kept, ",")
		node.Tag = "!!str"
		return true
	case yaml.SequenceNode:
		var kept []*yaml.Node
		for _, c := range node.Content {
			if normCap(scalarString(*c)) == cap {
				continue
			}
			kept = append(kept, c)
		}
		if len(kept) == len(node.Content) {
			return false
		}
		if len(kept) == 0 {
			return deleteKey(m, "cap_add")
		}
		node.Content = kept
		return true
	default:
		return false
	}
}

func filterCaps(in []string, drop string) []string {
	var out []string
	for _, c := range in {
		if normCap(c) == drop {
			continue
		}
		out = append(out, c)
	}
	return out
}

func normCap(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	return strings.TrimPrefix(s, "CAP_")
}

func dropMounts(m *yaml.Node, drop func(string) bool) bool {
	node := mappingValue(m, "volumes")
	if node == nil || node.Kind != yaml.SequenceNode {
		return false
	}
	var kept []*yaml.Node
	removed := false
	for _, c := range node.Content {
		src, bind := volumeSource(*c)
		if bind && drop(src) {
			removed = true
			continue
		}
		kept = append(kept, c)
	}
	if !removed {
		return false
	}
	if len(kept) == 0 {
		return deleteKey(m, "volumes")
	}
	node.Content = kept
	return true
}

func isSock(host string) bool {
	if host == "" || !path.IsAbs(host) {
		return false
	}
	p := path.Clean(host)
	return p == "/var/run/docker.sock" || p == "/run/docker.sock" || strings.HasSuffix(p, "/docker.sock")
}

func broadPath(host string) string {
	if host == "" || !path.IsAbs(host) {
		return ""
	}
	switch path.Clean(host) {
	case "/", "/etc", "/root":
		return path.Clean(host)
	default:
		return ""
	}
}

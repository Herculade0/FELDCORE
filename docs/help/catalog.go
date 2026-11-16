package help

import (
	"encoding/json"
	"fmt"
	"io/fs"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
)

// Route is one deep link. Available routes open a page in the app. A route
// that is not available opens its help topic instead of a missing page.
type Route struct {
	Page      string `json:"page"`
	Tab       string `json:"tab,omitempty"`
	Anchor    string `json:"anchor,omitempty"`
	View      string `json:"view,omitempty"`
	Help      string `json:"help,omitempty"`
	Available bool   `json:"available"`
	Note      string `json:"note,omitempty"`
}

// Topic is one guide or reference article, without the leading frontmatter.
type Topic struct {
	ID         string   `json:"id"`
	Kind       string   `json:"kind"`
	Title      string   `json:"title"`
	Summary    string   `json:"summary"`
	Icon       string   `json:"icon"`
	Order      int      `json:"order"`
	Popular    bool     `json:"popular"`
	ComingSoon bool     `json:"comingSoon"`
	Related    []string `json:"related"`
	References []string `json:"references"`
	Page       string   `json:"page,omitempty"`
	Body       string   `json:"body"`
}

// Catalog is every article plus the route map, ready to send to the UI.
type Catalog struct {
	Topics []Topic          `json:"topics"`
	Routes map[string]Route `json:"routes"`
}

var (
	loadOnce sync.Once
	loaded   Catalog
	loadErr  error
)

// Load returns the embedded catalog. The first successful (or failed) read
// is reused for the life of the process.
func Load() (Catalog, error) {
	loadOnce.Do(func() {
		loaded, loadErr = load()
	})
	return loaded, loadErr
}

var (
	routeRef = regexp.MustCompile(`\[\[route:([a-z0-9]+(?:[.-][a-z0-9]+)*)\|`)
	helpRef  = regexp.MustCompile(`\[\[help:([a-z0-9]+(?:-[a-z0-9]+)*)\|`)
	helpLink = regexp.MustCompile(`\(help:([a-z0-9]+(?:-[a-z0-9]+)*)\)`)
	idPat    = regexp.MustCompile(`^[a-z0-9]+(?:-[a-z0-9]+)*$`)
)

var pages = map[string]bool{
	"dashboard": true, "shares": true, "docker": true, "apps": true,
	"vms": true, "audit": true, "settings": true, "help": true,
}

var tabs = map[string]bool{
	"general": true, "network": true, "remote": true, "publishing": true,
}

func load() (Catalog, error) {
	raw, err := FS.ReadFile("routes.json")
	if err != nil {
		return Catalog{}, err
	}
	var routes map[string]Route
	if err := json.Unmarshal(raw, &routes); err != nil {
		return Catalog{}, fmt.Errorf("routes.json: %w", err)
	}
	if len(routes) == 0 {
		return Catalog{}, fmt.Errorf("routes.json: no routes")
	}

	topics, err := readTopics()
	if err != nil {
		return Catalog{}, err
	}

	ids := map[string]bool{}
	pagesSeen := map[string]string{}
	for _, t := range topics {
		if ids[t.ID] {
			return Catalog{}, fmt.Errorf("duplicate help id %q", t.ID)
		}
		ids[t.ID] = true
		if t.Page != "" {
			if prev, ok := pagesSeen[t.Page]; ok {
				return Catalog{}, fmt.Errorf("page %q is claimed by %q and %q", t.Page, prev, t.ID)
			}
			pagesSeen[t.Page] = t.ID
		}
	}

	if err := validateRoutes(routes, ids); err != nil {
		return Catalog{}, err
	}
	for i := range topics {
		if err := checkBody(topics[i].ID, topics[i].Body, routes, ids); err != nil {
			return Catalog{}, err
		}
		for _, id := range topics[i].Related {
			if !ids[id] {
				return Catalog{}, fmt.Errorf("%s: related %q does not exist", topics[i].ID, id)
			}
		}
		for _, id := range topics[i].References {
			if !ids[id] {
				return Catalog{}, fmt.Errorf("%s: reference %q does not exist", topics[i].ID, id)
			}
		}
	}

	sort.Slice(topics, func(i, j int) bool {
		if topics[i].Order != topics[j].Order {
			return topics[i].Order < topics[j].Order
		}
		return topics[i].Title < topics[j].Title
	})
	return Catalog{Topics: topics, Routes: routes}, nil
}

func readTopics() ([]Topic, error) {
	var topics []Topic
	add := func(path string) error {
		b, err := FS.ReadFile(path)
		if err != nil {
			return err
		}
		t, err := parseTopic(path, b)
		if err != nil {
			return err
		}
		topics = append(topics, t)
		return nil
	}
	if err := add("CONTRIBUTING.md"); err != nil {
		return nil, err
	}
	for _, root := range []string{"guides", "reference"} {
		err := fs.WalkDir(FS, root, func(path string, d fs.DirEntry, err error) error {
			if err != nil || d.IsDir() || !strings.HasSuffix(path, ".md") {
				return err
			}
			return add(path)
		})
		if err != nil {
			return nil, err
		}
	}
	return topics, nil
}

func validateRoutes(routes map[string]Route, ids map[string]bool) error {
	for id, r := range routes {
		if !pages[r.Page] {
			return fmt.Errorf("route %q: unknown page %q", id, r.Page)
		}
		if r.Tab != "" && !tabs[r.Tab] {
			return fmt.Errorf("route %q: unknown tab %q", id, r.Tab)
		}
		if r.Page == "settings" && r.Available && r.Tab == "" {
			return fmt.Errorf("route %q: a settings route needs a tab", id)
		}
		if !r.Available {
			if r.Help == "" || !ids[r.Help] {
				return fmt.Errorf("route %q: unavailable routes need a help topic", id)
			}
		}
	}
	return nil
}

func checkBody(id, body string, routes map[string]Route, ids map[string]bool) error {
	for _, m := range routeRef.FindAllStringSubmatch(body, -1) {
		if _, ok := routes[m[1]]; !ok {
			return fmt.Errorf("%s: unknown route %q", id, m[1])
		}
	}
	for _, m := range helpRef.FindAllStringSubmatch(body, -1) {
		if !ids[m[1]] {
			return fmt.Errorf("%s: unknown help link %q", id, m[1])
		}
	}
	for _, m := range helpLink.FindAllStringSubmatch(body, -1) {
		if !ids[m[1]] {
			return fmt.Errorf("%s: unknown help link %q", id, m[1])
		}
	}
	return nil
}

func parseTopic(path string, data []byte) (Topic, error) {
	text := strings.ReplaceAll(string(data), "\r\n", "\n")
	if !strings.HasPrefix(text, "---\n") {
		return Topic{}, fmt.Errorf("%s: missing frontmatter", path)
	}
	rest := strings.TrimPrefix(text, "---\n")
	end := strings.Index(rest, "\n---\n")
	if end < 0 {
		return Topic{}, fmt.Errorf("%s: unclosed frontmatter", path)
	}
	meta, err := parseFront(rest[:end])
	if err != nil {
		return Topic{}, fmt.Errorf("%s: %w", path, err)
	}
	t := Topic{
		ID:         str(meta, "id"),
		Kind:       str(meta, "kind"),
		Title:      str(meta, "title"),
		Summary:    str(meta, "summary"),
		Icon:       str(meta, "icon"),
		Popular:    boolean(meta, "popular"),
		ComingSoon: boolean(meta, "comingSoon"),
		Related:    list(meta, "related"),
		References: list(meta, "references"),
		Page:       str(meta, "page"),
		Body:       strings.TrimLeft(rest[end+len("\n---\n"):], "\n"),
	}
	n, ok := meta["order"].(int)
	if !ok {
		return Topic{}, fmt.Errorf("%s: order must be a number", path)
	}
	t.Order = n
	if !idPat.MatchString(t.ID) {
		return Topic{}, fmt.Errorf("%s: id %q must be lowercase words", path, t.ID)
	}
	if t.Kind != "guide" && t.Kind != "reference" {
		return Topic{}, fmt.Errorf("%s: kind must be guide or reference", path)
	}
	if t.Title == "" || t.Summary == "" || t.Icon == "" {
		return Topic{}, fmt.Errorf("%s: title, summary and icon are required", path)
	}
	if t.Page != "" && !pages[t.Page] {
		return Topic{}, fmt.Errorf("%s: page %q is not a nav page", path, t.Page)
	}
	if strings.TrimSpace(t.Body) == "" {
		return Topic{}, fmt.Errorf("%s: empty body", path)
	}
	return t, nil
}

func parseFront(s string) (map[string]any, error) {
	out := map[string]any{}
	listKey := ""
	for _, line := range strings.Split(s, "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		if listKey != "" && (strings.HasPrefix(line, "  - ") || strings.HasPrefix(line, "- ")) {
			item := strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(line), "-"))
			out[listKey] = append(out[listKey].([]string), item)
			continue
		}
		listKey = ""
		key, val, ok := strings.Cut(line, ":")
		if !ok || strings.HasPrefix(line, " ") {
			return nil, fmt.Errorf("bad frontmatter line %q", line)
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if val == "" || val == "[]" {
			out[key] = []string{}
			if val == "" {
				listKey = key
			}
			continue
		}
		switch val {
		case "true":
			out[key] = true
		case "false":
			out[key] = false
		default:
			if n, err := strconv.Atoi(val); err == nil {
				out[key] = n
			} else {
				out[key] = strings.Trim(val, `"`)
			}
		}
	}
	return out, nil
}

func str(m map[string]any, k string) string {
	v, _ := m[k].(string)
	return v
}

func boolean(m map[string]any, k string) bool {
	v, _ := m[k].(bool)
	return v
}

func list(m map[string]any, k string) []string {
	v, _ := m[k].([]string)
	if v == nil {
		return []string{}
	}
	return v
}

package apps

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Herculade0/feldcore/internal/docker"
)

// FeedURL is the public Community Applications catalogue that Unraid's CA
// plugin downloads. It is generated from the template repositories and already
// includes each app's configuration, so FELDCORE does not clone those
// repositories or fetch a template per app.
const FeedURL = "https://raw.githubusercontent.com/Squidly271/AppFeed/master/applicationFeed.json"

// App is one installable Docker app, without the template body.
type App struct {
	ID         string   `json:"id"`
	Name       string   `json:"name"`
	Author     string   `json:"author,omitempty"`
	Image      string   `json:"image"`
	Icon       string   `json:"icon,omitempty"`
	Overview   string   `json:"overview"`
	Categories []string `json:"categories"`
	Beta       bool     `json:"beta,omitempty"`
	Deprecated bool     `json:"deprecated,omitempty"`
	Downloads  int      `json:"downloads,omitempty"`
	Stars      int      `json:"stars,omitempty"`
	Project    string   `json:"project,omitempty"`
	Support    string   `json:"support,omitempty"`
	// Requires is Unraid's free-text "this app needs" note, with markup removed.
	Requires string `json:"requires,omitempty"`
	// Purpose is set on a FELDCORE template, for example "AI integration".
	Purpose string `json:"purpose,omitempty"`
	// Origin is "feldcore" for a template shipped with FELDCORE. Community
	// Apps leave it empty.
	Origin string `json:"origin,omitempty"`
}

// Category is a filter entry. ID matches App.Categories.
type Category struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Count int    `json:"count"`
}

type appRecord struct {
	app  App
	tmpl docker.Template
}

type parsedFeed struct {
	apps       []appRecord
	byID       map[string]int
	categories []Category
	labels     map[string]string
}

func parseFeed(data []byte) (*parsedFeed, error) {
	var raw feedFile
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("catalogue was not valid JSON: %w", err)
	}
	if raw.AppList == nil {
		return nil, fmt.Errorf("catalogue has no app list")
	}
	labels := categoryLabels(raw.Categories)
	blocked := map[string]bool{}
	for k := range raw.Blacklisted {
		blocked[strings.ToLower(strings.TrimSpace(k))] = true
	}
	deprecated := map[string]bool{}
	for k := range raw.Deprecated {
		deprecated[strings.ToLower(strings.TrimSpace(k))] = true
	}

	out := &parsedFeed{byID: map[string]int{}, labels: labels}
	counts := map[string]int{}
	for _, a := range raw.AppList {
		if a.Plugin.True() || a.Blacklist.True() || a.CABlacklist.True() {
			continue
		}
		image := strings.TrimSpace(a.Repository)
		if !imageRef.MatchString(image) || strings.Contains(image, "://") || strings.HasSuffix(strings.ToLower(image), ".plg") {
			continue
		}
		if blocked[strings.ToLower(image)] || blocked[strings.ToLower(imageWithoutTag(image))] {
			continue
		}
		name := strings.TrimSpace(a.Name)
		if name == "" {
			continue
		}
		rec := appRecord{tmpl: a.template()}
		cats := cleanCategories(a.CategoryList)
		overview := a.Overview
		if strings.TrimSpace(overview) == "" {
			overview = a.Description
		}
		overview = plainOverview(overview)
		rec.app = App{
			ID:         appID(a.TemplateURL, name, image),
			Name:       name,
			Author:     authorOf(a.Repo),
			Image:      image,
			Icon:       dockerIcon(a.Icon),
			Overview:   overview,
			Categories: cats,
			Beta:       a.Beta.True(),
			Deprecated: a.Deprecated.True() || deprecated[strings.ToLower(image)] || deprecated[strings.ToLower(imageWithoutTag(image))],
			Downloads:  int(a.Downloads),
			Stars:      int(a.Stars),
			Project:    strings.TrimSpace(a.Project),
			Support:    strings.TrimSpace(a.Support),
			Requires:   plainRequires(string(a.Requires)),
		}
		if _, taken := out.byID[rec.app.ID]; taken {
			rec.app.ID = rec.app.ID + "-" + strconv.Itoa(len(out.apps))
		}
		out.byID[rec.app.ID] = len(out.apps)
		out.apps = append(out.apps, rec)
		seen := map[string]bool{}
		for _, c := range cats {
			if seen[c] {
				continue
			}
			seen[c] = true
			counts[c]++
		}
	}
	sortApps(out.apps)
	// The sort invalidates byID.
	out.byID = map[string]int{}
	for i := range out.apps {
		out.byID[out.apps[i].app.ID] = i
	}
	out.categories = categoryCounts(counts, labels)
	return out, nil
}

func (a feedApp) template() docker.Template {
	net := strings.TrimSpace(a.Network)
	if net == "" {
		net = strings.TrimSpace(a.Networking.Mode)
	}
	t := docker.Template{
		Name:        strings.TrimSpace(a.Name),
		Repository:  strings.TrimSpace(a.Repository),
		Network:     net,
		Privileged:  a.Privileged.True(),
		WebUI:       strings.TrimSpace(a.WebUI),
		Icon:        strings.TrimSpace(a.Icon),
		ExtraParams: strings.TrimSpace(a.ExtraParams),
	}
	for _, c := range a.Config {
		t.Configs = append(t.Configs, docker.TemplateConfig{
			Name:        c.Attrs.Name,
			Target:      c.Attrs.Target,
			Default:     c.Attrs.Default,
			Value:       c.Value,
			Mode:        c.Attrs.Mode,
			Description: c.Attrs.Description,
			Type:        c.Attrs.Type,
			Display:     c.Attrs.Display,
			Required:    truthy(c.Attrs.Required),
			Mask:        truthy(c.Attrs.Mask) || strings.EqualFold(strings.TrimSpace(c.Attrs.Display), "password"),
		})
	}
	return t
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

type feedFile struct {
	Categories  []feedCategory    `json:"categories"`
	AppList     []feedApp         `json:"applist"`
	Blacklisted map[string]string `json:"blacklisted"`
	Deprecated  map[string]string `json:"deprecated"`
}

type feedCategory struct {
	Cat string         `json:"Cat"`
	Des string         `json:"Des"`
	Sub []feedCategory `json:"Sub"`
}

type feedApp struct {
	Name         string      `json:"Name"`
	Repository   string      `json:"Repository"`
	Network      string      `json:"Network"`
	Privileged   flexBool    `json:"Privileged"`
	Support      string      `json:"Support"`
	Project      string      `json:"Project"`
	Overview     string      `json:"Overview"`
	Description  string      `json:"Description"`
	CategoryList []string    `json:"CategoryList"`
	WebUI        string      `json:"WebUI"`
	TemplateURL  string      `json:"TemplateURL"`
	Icon         string      `json:"Icon"`
	ExtraParams  string      `json:"ExtraParams"`
	Config       flexConfigs `json:"Config"`
	Repo         string      `json:"Repo"`
	Plugin       flexBool    `json:"Plugin"`
	Beta         flexBool    `json:"Beta"`
	Deprecated   flexBool    `json:"Deprecated"`
	Blacklist    flexBool    `json:"Blacklist"`
	CABlacklist  flexBool    `json:"CABlacklist"`
	Downloads    flexInt     `json:"downloads"`
	Stars        flexInt     `json:"stars"`
	Requires     flexText    `json:"Requires"`
	Networking   struct {
		Mode string `json:"Mode"`
	} `json:"Networking"`
}

type feedAttr struct {
	Name        string `json:"Name"`
	Target      string `json:"Target"`
	Default     string `json:"Default"`
	Mode        string `json:"Mode"`
	Description string `json:"Description"`
	Type        string `json:"Type"`
	Display     string `json:"Display"`
	Required    string `json:"Required"`
	Mask        string `json:"Mask"`
}

type feedConfig struct {
	Attrs feedAttr `json:"@attributes"`
	Value string   `json:"value"`
}

// flexConfigs accepts a JSON array or a single object. A template with one
// Config is occasionally emitted as an object rather than a one-element array.
type flexConfigs []feedConfig

func (f *flexConfigs) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	switch data[0] {
	case '[':
		var list []feedConfig
		if err := json.Unmarshal(data, &list); err != nil {
			return err
		}
		*f = list
	case '{':
		var one feedConfig
		if err := json.Unmarshal(data, &one); err != nil {
			return err
		}
		*f = []feedConfig{one}
	}
	return nil
}

// flexBool accepts the feed's mix of booleans, "true"/"false", and the odd
// list produced when a template repeats a tag.
type flexBool struct{ v bool }

func (b flexBool) True() bool { return b.v }

func (b *flexBool) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	switch data[0] {
	case 't', 'T':
		b.v = true
	case 'f', 'F':
		b.v = false
	case '"':
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		b.v = truthy(s)
	case '[':
		var arr []json.RawMessage
		if err := json.Unmarshal(data, &arr); err != nil {
			return err
		}
		for _, el := range arr {
			var inner flexBool
			if err := inner.UnmarshalJSON(el); err != nil {
				return err
			}
			if inner.v {
				b.v = true
			}
		}
	case 'n':
		return nil
	default:
		var n int
		if err := json.Unmarshal(data, &n); err != nil {
			return err
		}
		b.v = n != 0
	}
	return nil
}

type flexInt int

func (n *flexInt) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] == '"' {
		var s string
		if err := json.Unmarshal(data, &s); err != nil {
			return err
		}
		i, _ := strconv.Atoi(strings.TrimSpace(s))
		*n = flexInt(i)
		return nil
	}
	var f float64
	if err := json.Unmarshal(data, &f); err != nil {
		return err
	}
	*n = flexInt(f)
	return nil
}

func categoryLabels(cats []feedCategory) map[string]string {
	out := map[string]string{}
	var walk func(parentID, parentDes string, c feedCategory)
	walk = func(parentID, parentDes string, c feedCategory) {
		id := catID(c.Cat)
		label := strings.TrimSpace(c.Des)
		if parentID != "" {
			base := parentDes
			if base == "" || strings.Contains(base, "/") {
				base = parentID
			}
			if label == "" {
				label = id
			} else {
				label = base + " / " + label
			}
		}
		if id != "" && label != "" {
			out[id] = label
		}
		for _, sub := range c.Sub {
			walk(id, strings.TrimSpace(c.Des), sub)
		}
	}
	for _, c := range cats {
		walk("", "", c)
	}
	return out
}

func catID(cat string) string {
	cat = strings.TrimSpace(cat)
	cat = strings.TrimSuffix(cat, ":")
	return strings.ReplaceAll(cat, ":", "-")
}

func categoryCounts(counts map[string]int, labels map[string]string) []Category {
	out := make([]Category, 0, len(counts))
	for id, n := range counts {
		if id == "" || n == 0 {
			continue
		}
		label := labels[id]
		if label == "" {
			label = strings.ReplaceAll(id, "-", " / ")
		}
		out = append(out, Category{ID: id, Label: label, Count: n})
	}
	sortCategories(out)
	return out
}

func cleanCategories(in []string) []string {
	// An empty list must be a JSON array. A nil slice becomes null, and the
	// store page crashes when it spreads a missing category list during search.
	out := []string{}
	seen := map[string]bool{}
	for _, c := range in {
		c = strings.TrimSpace(c)
		c = strings.Trim(c, ":")
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

func authorOf(repo string) string {
	repo = strings.TrimSpace(repo)
	repo = strings.TrimSuffix(repo, "'s Repository")
	repo = strings.TrimSuffix(repo, "’s Repository")
	repo = strings.TrimSuffix(repo, " Repository")
	return strings.TrimSpace(repo)
}

func imageWithoutTag(ref string) string {
	if i := strings.LastIndex(ref, ":"); i > 0 && !strings.Contains(ref[i:], "/") {
		return ref[:i]
	}
	return ref
}

func appID(templateURL, name, image string) string {
	key := strings.TrimSpace(templateURL)
	if key == "" {
		key = strings.ToLower(name) + "\n" + image
	}
	sum := sha256.Sum256([]byte(key))
	return hex.EncodeToString(sum[:6])
}

var (
	mdLink     = regexp.MustCompile(`\[([^\[\]]+)\]\([^)]+\)`)
	bareURL    = regexp.MustCompile(`https?://[^\s)]+`)
	emptyParen = regexp.MustCompile(`\(\s*\)`)
)

// flexText accepts a string or null. A non-string is ignored so one odd
// template cannot reject the whole catalogue.
type flexText string

func (f *flexText) UnmarshalJSON(data []byte) error {
	data = bytes.TrimSpace(data)
	if len(data) == 0 || string(data) == "null" {
		return nil
	}
	if data[0] != '"' {
		return nil
	}
	var s string
	if err := json.Unmarshal(data, &s); err != nil {
		return err
	}
	*f = flexText(s)
	return nil
}

var (
	bbURLNamed = regexp.MustCompile(`(?i)\[url=[^\]]+\]([\s\S]*?)\[/url\]`)
	bbURLBare  = regexp.MustCompile(`(?i)\[url\]([\s\S]*?)\[/url\]`)
	bbTag      = regexp.MustCompile(`(?i)\[/?[a-z][a-z0-9]*\]`)
	htmlBreak  = regexp.MustCompile(`(?i)<br\s*/?>`)
	htmlTag    = regexp.MustCompile(`(?i)<[^>]*>`)
)

// plainRequires turns a template Requires note into text. Authors paste HTML,
// markdown, and phpBB-style [b] tags. Nothing is kept that could be a link
// or markup; the UI shows the words only.
func plainRequires(s string) string {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}
	s = strings.NewReplacer(
		"&nbsp;", " ",
		"&amp;", "&",
		"&lt;", "<",
		"&gt;", ">",
		"&quot;", `"`,
		"&#39;", "'",
		"&apos;", "'",
	).Replace(s)
	s = htmlBreak.ReplaceAllString(s, " ")
	s = bbURLNamed.ReplaceAllString(s, "$1")
	s = bbURLBare.ReplaceAllString(s, "$1")
	s = mdLink.ReplaceAllString(s, "$1")
	s = bbTag.ReplaceAllString(s, "")
	s = htmlTag.ReplaceAllString(s, "")
	return truncate(s, 500)
}

// plainOverview turns catalogue blurbs into text. Many of them paste a
// markdown link or a raw URL into the first sentence.
func plainOverview(s string) string {
	s = mdLink.ReplaceAllString(s, "$1")
	s = bareURL.ReplaceAllString(s, "")
	s = emptyParen.ReplaceAllString(s, "")
	return truncate(s, 360)
}

func truncate(s string, n int) string {
	s = strings.Join(strings.Fields(s), " ")
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return strings.TrimSpace(string(r[:n])) + "…"
}

// imageRef matches the image names the container setup page accepts.
var imageRef = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]{0,254}$`)

// libraryImage reports Docker Hub "official" images (nginx, postgres, redis).
// The feed's download count for those is the image's pull count, so every
// sidecar that uses nginx sorts above real applications. They still show up
// in search; they just don't lead the list.
func libraryImage(ref string) bool {
	name := ref
	if i := strings.LastIndex(name, ":"); i > 0 && !strings.Contains(name[i:], "/") {
		name = name[:i]
	}
	return !strings.Contains(name, "/")
}

func sortApps(apps []appRecord) {
	sort.SliceStable(apps, func(i, j int) bool {
		a, b := apps[i].app, apps[j].app
		if a.Deprecated != b.Deprecated {
			return !a.Deprecated
		}
		al, bl := libraryImage(a.Image), libraryImage(b.Image)
		if al != bl {
			return !al
		}
		if a.Downloads != b.Downloads {
			return a.Downloads > b.Downloads
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
}

func sortCategories(cats []Category) {
	sort.Slice(cats, func(i, j int) bool {
		if cats[i].Label != cats[j].Label {
			return cats[i].Label < cats[j].Label
		}
		return cats[i].ID < cats[j].ID
	})
}

func dockerIcon(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "https://") || strings.HasPrefix(s, "http://") {
		if !strings.ContainsAny(s, " \t") {
			return s
		}
	}
	return ""
}

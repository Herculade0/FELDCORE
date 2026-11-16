package docker

import (
	"encoding/xml"
	"fmt"
	"path"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
)

// Template is an Unraid container template: the fields Community Applications
// stores for one Docker app, whether they arrived as XML or as the JSON feed.
type Template struct {
	Name        string
	Repository  string // image reference
	Network     string
	Privileged  bool
	WebUI       string
	Icon        string
	ExtraParams string
	Configs     []TemplateConfig
}

// TemplateConfig is one <Config> entry. Value is the element's text; Default
// is the Default attribute. Unraid shows Value when it is set, otherwise Default.
type TemplateConfig struct {
	Name        string
	Target      string
	Default     string
	Value       string
	Mode        string
	Description string
	Type        string // Port, Path, Variable, Label, Device
	Display     string
	Required    bool
	Mask        bool
}

// PathMap tells the converter where Unraid's /mnt/user paths live on this
// Debian host. Shares is keyed by lower-case share name.
type PathMap struct {
	Appdata    string
	SharesRoot string
	Shares     map[string]string
}

var unraidMount = regexp.MustCompile(`^/mnt/(user|user0|cache|disk[0-9]+)(?:/(.*))?$`)

// ParseTemplateXML reads an Unraid docker template. The root may be
// <Container> or <Containers><Container>.
func ParseTemplateXML(data []byte) (Template, error) {
	var doc xmlTemplate
	if err := xml.Unmarshal(data, &doc); err != nil || (doc.Name == "" && doc.Repository == "" && len(doc.Config) == 0) {
		var wrap struct {
			Container xmlTemplate `xml:"Container"`
		}
		if err2 := xml.Unmarshal(data, &wrap); err2 != nil || (wrap.Container.Name == "" && wrap.Container.Repository == "") {
			if err != nil {
				return Template{}, err
			}
			return Template{}, fmt.Errorf("not an Unraid container template")
		}
		doc = wrap.Container
	}
	t := Template{
		Name:        strings.TrimSpace(doc.Name),
		Repository:  strings.TrimSpace(doc.Repository),
		Network:     strings.TrimSpace(doc.Network),
		Privileged:  truthy(doc.Privileged),
		WebUI:       strings.TrimSpace(doc.WebUI),
		Icon:        strings.TrimSpace(doc.Icon),
		ExtraParams: strings.TrimSpace(doc.ExtraParams),
	}
	for _, c := range doc.Config {
		t.Configs = append(t.Configs, TemplateConfig{
			Name:        strings.TrimSpace(c.Name),
			Target:      strings.TrimSpace(c.Target),
			Default:     strings.TrimSpace(c.Default),
			Value:       strings.TrimSpace(c.Value),
			Mode:        strings.TrimSpace(c.Mode),
			Description: strings.TrimSpace(c.Description),
			Type:        strings.TrimSpace(c.Type),
			Display:     strings.TrimSpace(c.Display),
			Required:    truthy(c.Required),
			Mask:        truthy(c.Mask),
		})
	}
	return t, nil
}

type xmlTemplate struct {
	XMLName     xml.Name    `xml:"Container"`
	Name        string      `xml:"Name"`
	Repository  string      `xml:"Repository"`
	Network     string      `xml:"Network"`
	Privileged  string      `xml:"Privileged"`
	WebUI       string      `xml:"WebUI"`
	Icon        string      `xml:"Icon"`
	ExtraParams string      `xml:"ExtraParams"`
	Config      []xmlConfig `xml:"Config"`
}

type xmlConfig struct {
	Name        string `xml:"Name,attr"`
	Target      string `xml:"Target,attr"`
	Default     string `xml:"Default,attr"`
	Mode        string `xml:"Mode,attr"`
	Description string `xml:"Description,attr"`
	Type        string `xml:"Type,attr"`
	Display     string `xml:"Display,attr"`
	Required    string `xml:"Required,attr"`
	Mask        string `xml:"Mask,attr"`
	Value       string `xml:",chardata"`
}

func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	default:
		return false
	}
}

// TemplateToSpec converts a template into the spec the container setup page
// edits. Nothing is created. notes explains Unraid assumptions that were
// changed for a normal Debian host.
func TemplateToSpec(t Template, paths PathMap) (Spec, []string) {
	s := Spec{
		Name:          sanitiseName(t.Name),
		Image:         strings.TrimSpace(t.Repository),
		RestartPolicy: "unless-stopped",
		Autostart:     true,
		WebUI:         strings.TrimSpace(t.WebUI),
		Icon:          httpURL(t.Icon),
		Privileged:    t.Privileged,
		Configs:       []Config{},
	}
	var notes []string
	network, note := mapNetwork(t.Network)
	s.Network = network
	if note != "" {
		notes = append(notes, note)
	}

	rewrote := false
	var skipped int
	seen := map[string]bool{}
	for _, raw := range t.Configs {
		for _, c := range expandConfig(raw, paths, &rewrote) {
			if !keepConfig(c) {
				if raw.Required {
					skipped++
				}
				continue
			}
			key := configKey(c)
			if c.Type == TypePort {
				key = fmt.Sprintf("port:%d/%s:%d", c.ContainerPort, c.Proto, c.HostPort)
			}
			if seen[key] {
				continue
			}
			seen[key] = true
			s.Configs = append(s.Configs, c)
		}
	}

	extra := parseExtra(t.ExtraParams)
	if extra.Restart != "" {
		s.RestartPolicy = extra.Restart
	}
	if extra.Privileged {
		s.Privileged = true
	}
	for i := range extra.Configs {
		c := extra.Configs[i]
		if c.Type == TypePath {
			if host, changed := rewriteUnraidPath(c.Host, paths); changed {
				c.Host = host
				rewrote = true
			}
		}
		key := configKey(c)
		if c.Type == TypePort {
			key = fmt.Sprintf("port:%d/%s:%d", c.ContainerPort, c.Proto, c.HostPort)
		}
		if seen[key] {
			continue
		}
		seen[key] = true
		s.Configs = append(s.Configs, c)
	}
	s.ExtraParams = strings.TrimSpace(extra.Cleaned)

	if rewrote {
		where := paths.Appdata
		if where == "" {
			where = "the app data folder"
		}
		notes = append(notes, "Unraid paths under /mnt/user, /mnt/cache and /mnt/disk were rewritten for this server. App data goes in "+where+".")
	}
	if unraidNobody(s) || strings.Contains(s.ExtraParams, "99:100") {
		notes = append(notes, "PUID 99 and PGID 100 are Unraid's nobody account. On Debian, change them to a user that owns the host folders.")
	}
	if len(extra.Unparsed) > 0 {
		shown := extra.Unparsed
		more := ""
		if len(shown) > 4 {
			more = fmt.Sprintf(" and %d more", len(shown)-4)
			shown = shown[:4]
		}
		notes = append(notes, "Some extra parameters are kept for review but not applied: "+strings.Join(shown, ", ")+more+".")
	}
	if skipped > 0 {
		notes = append(notes, "A required setting could not be read from the template. Check the list before applying.")
	}
	return s, notes
}

func unraidNobody(s Spec) bool {
	var puid, pgid bool
	for _, c := range s.Configs {
		if c.Type != TypeVariable {
			continue
		}
		if c.Key == "PUID" && strings.TrimSpace(c.Value) == "99" {
			puid = true
		}
		if c.Key == "PGID" && strings.TrimSpace(c.Value) == "100" {
			pgid = true
		}
	}
	return puid || pgid
}

func mapNetwork(n string) (string, string) {
	n = strings.ToLower(strings.TrimSpace(n))
	switch n {
	case "", "default", "bridge":
		return "bridge", ""
	case "host", "none":
		return n, ""
	default:
		if !networkRe.MatchString(n) {
			return "bridge", ""
		}
		return "bridge", fmt.Sprintf("The template uses Docker network %q. This setup uses bridge instead, which you can change below.", n)
	}
}

// expandConfig turns one template entry into one or two settings (a port
// marked tcp and udp becomes two).
func expandConfig(raw TemplateConfig, paths PathMap, rewrote *bool) []Config {
	display := normaliseDisplay(raw.Display)
	base := Config{
		Name:        strings.TrimSpace(raw.Name),
		Description: strings.TrimSpace(raw.Description),
		Display:     display,
		Required:    raw.Required,
		Mask:        raw.Mask || strings.EqualFold(raw.Display, "password"),
	}
	value := firstNonEmpty(raw.Value, raw.Default)
	switch strings.ToLower(strings.TrimSpace(raw.Type)) {
	case "path":
		c := base
		c.Type = TypePath
		c.Container = strings.TrimSpace(raw.Target)
		c.Host = value
		c.Mode = pathMode(raw.Mode)
		if host, changed := rewriteUnraidPath(c.Host, paths); changed {
			c.Host = host
			*rewrote = true
		}
		return []Config{c}
	case "port":
		protos := portProtos(raw.Mode)
		ctr, _ := strconv.Atoi(strings.TrimSpace(raw.Target))
		host := portNumber(value)
		if host == 0 {
			host = portNumber(raw.Default)
		}
		if host == 0 {
			host = ctr
		}
		if ctr == 0 {
			ctr = host
		}
		out := make([]Config, 0, len(protos))
		for _, proto := range protos {
			c := base
			c.Type = TypePort
			c.ContainerPort = ctr
			c.HostPort = host
			c.Proto = proto
			out = append(out, c)
		}
		return out
	case "variable":
		c := base
		c.Type = TypeVariable
		c.Key = strings.TrimSpace(raw.Target)
		c.Value = strings.ReplaceAll(value, "\x00", "")
		return []Config{c}
	case "label":
		c := base
		c.Type = TypeLabel
		c.Key = strings.TrimSpace(raw.Target)
		c.Value = strings.ReplaceAll(value, "\x00", "")
		return []Config{c}
	case "device":
		c := base
		c.Type = TypeDevice
		host := firstNonEmpty(raw.Value, raw.Default, raw.Target)
		ctr := strings.TrimSpace(raw.Target)
		if ctr == "" {
			ctr = host
		}
		c.Host = pathCleanDev(host)
		c.Container = pathCleanDev(ctr)
		c.Mode = strings.TrimSpace(raw.Mode)
		if c.Mode == "" {
			c.Mode = "rwm"
		}
		if c.Host == "/dev" || c.Container == "/dev" {
			return nil
		}
		return []Config{c}
	default:
		return nil
	}
}

func keepConfig(c Config) bool {
	switch c.Type {
	case TypePath:
		if c.Container == "" || !strings.HasPrefix(c.Container, "/") {
			return false
		}
		return c.Host != "" || c.Required
	case TypePort:
		return c.ContainerPort > 0
	case TypeVariable, TypeLabel:
		if c.Key == "" || strings.ContainsAny(c.Key, "= \t\r\n") {
			return false
		}
		if c.Type == TypeLabel && strings.HasPrefix(c.Key, labelPrefix) {
			return false
		}
		return c.Value != "" || c.Required
	case TypeDevice:
		return strings.HasPrefix(c.Host, "/dev/") && strings.HasPrefix(c.Container, "/")
	default:
		return false
	}
}

func normaliseDisplay(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	switch {
	case s == "advanced" || s == "advanced-show":
		return "advanced"
	case strings.Contains(s, "hide") || s == "hidden" || s == "none" || s == "false":
		return "hidden"
	default:
		return "always"
	}
}

func pathMode(mode string) string {
	mode = strings.ToLower(strings.TrimSpace(mode))
	if mode == "" || mode == "r" {
		if mode == "r" {
			return "ro"
		}
		return "rw"
	}
	return mode
}

func portProtos(mode string) []string {
	mode = strings.ToLower(mode)
	var out []string
	if strings.Contains(mode, "tcp") {
		out = append(out, "tcp")
	}
	if strings.Contains(mode, "udp") {
		out = append(out, "udp")
	}
	if len(out) == 0 {
		return []string{"tcp"}
	}
	return out
}

func portNumber(s string) int {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 1 || n > 65535 {
		return 0
	}
	return n
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		v = strings.TrimSpace(v)
		if v != "" {
			return v
		}
	}
	return ""
}

func httpURL(s string) string {
	s = strings.TrimSpace(s)
	i := strings.Index(s, "://")
	if i <= 0 {
		return ""
	}
	scheme := strings.ToLower(s[:i])
	if scheme != "http" && scheme != "https" {
		return ""
	}
	rest := s[i+3:]
	host, _, _ := strings.Cut(rest, "/")
	if host == "" || strings.ContainsAny(host, " \t") {
		return ""
	}
	return s
}

// rewriteUnraidPath maps /mnt/user/appdata/plex onto the host's app-data
// folder, and /mnt/user/<share>/... onto that share when FELDCORE has it.
func rewriteUnraidPath(p string, m PathMap) (string, bool) {
	p = strings.TrimSpace(p)
	if p == "" {
		return "", false
	}
	clean := path.Clean(p)
	sub := unraidMount.FindStringSubmatch(clean)
	if sub == nil {
		return p, false
	}
	rest := sub[2]
	if rest == "" {
		if m.SharesRoot != "" {
			return m.SharesRoot, true
		}
		return p, false
	}
	top, tail, _ := strings.Cut(rest, "/")
	var base string
	if strings.EqualFold(top, "appdata") && m.Appdata != "" {
		base = m.Appdata
	} else if m.Shares != nil {
		if sp, ok := m.Shares[strings.ToLower(top)]; ok && sp != "" {
			base = sp
		}
	}
	if base == "" {
		if m.SharesRoot == "" {
			return p, false
		}
		base = filepath.Join(m.SharesRoot, top)
	}
	if tail == "" {
		return base, true
	}
	return joinUnder(base, tail), true
}

// joinUnder joins sub onto base and refuses to let ".." climb out of base.
func joinUnder(base, sub string) string {
	joined := filepath.Join(base, filepath.FromSlash(sub))
	rel, err := filepath.Rel(base, joined)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return base
	}
	return joined
}

func sanitiseName(name string) string {
	var b strings.Builder
	dash := false
	for _, r := range strings.TrimSpace(name) {
		switch {
		case r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9':
			b.WriteRune(r)
			dash = false
		case r == '.' || r == '_' || r == '-':
			if b.Len() == 0 {
				continue
			}
			b.WriteRune(r)
			dash = true
		default:
			if b.Len() > 0 && !dash {
				b.WriteByte('-')
				dash = true
			}
		}
	}
	s := strings.Trim(b.String(), "._-")
	if len(s) > 63 {
		s = strings.Trim(s[:63], "._-")
	}
	if !nameRe.MatchString(s) {
		return ""
	}
	return s
}

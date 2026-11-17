package compose

import (
	"bytes"
	"path/filepath"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Herculade0/feldcore/internal/docker"
)

var (
	stackNameRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,30}$`)
	svcNameRe   = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,62}$`)
)

var reservedName = map[string]bool{
	"jobs": true, "preview": true, "import": true, "from-container": true, "new": true,
}

// CleanName checks a stack name. Names are lowercase so the directory, the
// Compose project and the network all match.
func CleanName(name string) (string, error) {
	name = strings.ToLower(strings.TrimSpace(name))
	if !stackNameRe.MatchString(name) || reservedName[name] {
		return "", inputf("Stack names use lowercase letters, numbers and hyphens, start with a letter, and are at most 31 characters.")
	}
	return name, nil
}

// Project is the Compose project name for a stack.
func Project(name string) string {
	return "feldcore-" + name
}

// PathInfo is where Unraid-style paths should land on this server.
type PathInfo struct {
	SharesRoot string
	Shares     map[string]string
}

type parsed struct {
	raw      string
	doc      *yaml.Node
	services []svc
	hasNets  bool
	version  string
	fileName string
	notes    []string
}

type svc struct {
	name      string
	image     string
	build     bool
	container string
	priv      bool
	netMode   string
	pid       string
	ipc       string
	caps      []string
	mounts    []string // bind sources, not yet resolved
	devices   []string
}

// Prepare validates a compose file, optionally rewrites Unraid paths and
// applies safer edits, then runs the container security check on every service.
func Prepare(compose, envText string, isolate bool, paths PathInfo, base string, rewrite bool, fixes []Fix) (Preview, error) {
	compose = strings.ReplaceAll(compose, "\r\n", "\n")
	envText = strings.ReplaceAll(envText, "\r\n", "\n")
	p := Preview{Compose: compose, Isolate: isolate, Services: []ServiceInfo{}, Findings: []Finding{}}
	if strings.TrimSpace(compose) == "" {
		p.Error = "Paste a compose file, or choose one to upload."
		return p, nil
	}
	if len(compose) > maxCompose {
		p.Error = "That compose file is larger than 512 KB."
		return p, nil
	}
	if len(envText) > maxEnv {
		p.Error = "That env file is larger than 256 KB."
		return p, nil
	}
	env, err := parseEnv(envText)
	if err != nil {
		p.Error = err.Error()
		return p, nil
	}
	f, err := parse(compose)
	if err != nil {
		p.Error = err.Error()
		return p, nil
	}
	changed := false
	if rewrite {
		n, notes, did := rewriteUnraid(f.doc, paths)
		if did {
			text, err := marshalNode(f.doc)
			if err != nil {
				p.Error = "The Unraid paths could not be rewritten."
				return p, nil
			}
			compose = text
			changed = true
			f, err = parse(compose)
			if err != nil {
				p.Error = err.Error()
				return p, nil
			}
			f.notes = append(f.notes, notes...)
			_ = n
		}
	}
	if len(fixes) > 0 {
		if applyFixes(f.doc, fixes) {
			text, err := marshalNode(f.doc)
			if err != nil {
				p.Error = "The safer edit could not be written back."
				return p, nil
			}
			compose = text
			changed = true
			f, err = parse(compose)
			if err != nil {
				p.Error = err.Error()
				return p, nil
			}
		}
	}
	if err := f.interpolate(env, base); err != nil {
		p.Error = err.Error()
		return p, nil
	}
	p.Valid = true
	p.Compose = compose
	p.Changed = changed
	p.Notes = append([]string{}, f.notes...)
	p.Services = f.infos()
	p.OwnNetworks = f.hasNets
	p.Findings = f.review(isolate)
	if p.Findings == nil {
		p.Findings = []Finding{}
	}
	net, note := f.networkPlan(isolate)
	p.Network = net
	if note != "" {
		p.Notes = append(p.Notes, note)
	}
	if f.version != "" {
		p.Notes = append(p.Notes, "The version key is from older Compose files. Compose v2 ignores it, and the file is still deployed.")
	}
	return p, nil
}

func parse(text string) (*parsed, error) {
	if strings.Count(text, "*") > 400 {
		return nil, inputf("This file uses too many YAML aliases.")
	}
	var doc yaml.Node
	if err := yaml.Unmarshal([]byte(text), &doc); err != nil {
		return nil, inputf("That is not valid YAML: %s", tidyYAML(err))
	}
	if doc.Kind != yaml.DocumentNode || len(doc.Content) != 1 {
		return nil, inputf("The compose file must be one YAML document.")
	}
	if nestedAlias(&doc) {
		return nil, inputf("Nested YAML aliases are not supported.")
	}
	root := doc.Content[0]
	if root.Kind != yaml.MappingNode {
		return nil, inputf("The compose file must be a mapping of services.")
	}
	if child(root, "include").Kind != 0 {
		return nil, inputf("FELDCORE deploys one compose file. Remove the include key and paste those services in.")
	}
	if child(root, "<<").Kind != 0 {
		return nil, inputf("YAML merge keys are not supported. Write each service out in full.")
	}
	svcNode := child(root, "services")
	if svcNode.Kind != yaml.MappingNode || len(svcNode.Content) == 0 {
		return nil, inputf("Add at least one service under services.")
	}
	if len(svcNode.Content)/2 > 100 {
		return nil, inputf("A stack can have at most 100 services.")
	}
	var raw struct {
		Version  string                `yaml:"version"`
		Services map[string]rawService `yaml:"services"`
		Networks map[string]any        `yaml:"networks"`
	}
	if err := doc.Decode(&raw); err != nil {
		return nil, inputf("That is not valid YAML: %s", tidyYAML(err))
	}
	f := &parsed{raw: text, doc: &doc, version: strings.TrimSpace(raw.Version), hasNets: len(raw.Networks) > 0}
	if len(raw.Services) == 0 {
		return nil, inputf("Add at least one service under services.")
	}
	order := mappingKeys(&svcNode)
	for _, name := range order {
		rs, ok := raw.Services[name]
		if !ok {
			continue
		}
		if !svcNameRe.MatchString(name) {
			return nil, inputf("Service names use letters, numbers and _ . - and start with a letter or number.")
		}
		if err := rs.check(name); err != nil {
			return nil, err
		}
		f.services = append(f.services, rs.toSvc(name))
	}
	if len(f.services) == 0 {
		return nil, inputf("Add at least one service under services.")
	}
	return f, nil
}

type rawService struct {
	Image         string      `yaml:"image"`
	Build         yaml.Node   `yaml:"build"`
	Privileged    yaml.Node   `yaml:"privileged"`
	NetworkMode   string      `yaml:"network_mode"`
	Pid           yaml.Node   `yaml:"pid"`
	Ipc           yaml.Node   `yaml:"ipc"`
	CapAdd        yaml.Node   `yaml:"cap_add"`
	Volumes       []yaml.Node `yaml:"volumes"`
	Devices       []yaml.Node `yaml:"devices"`
	ContainerName string      `yaml:"container_name"`
	Extends       yaml.Node   `yaml:"extends"`
}

func (r rawService) check(name string) error {
	if strings.TrimSpace(r.ContainerName) != "" && !svcNameRe.MatchString(strings.TrimSpace(r.ContainerName)) {
		return inputf("Service %s has a container name FELDCORE will not use. Use letters, numbers and _ . -.", name)
	}
	if r.Extends.Kind == yaml.MappingNode {
		if file := mappingScalar(r.Extends, "file"); file != "" {
			return inputf("Service %s pulls in another file with extends. Paste those services into this file.", name)
		}
	}
	hasImage := strings.TrimSpace(r.Image) != ""
	hasBuild := r.Build.Kind != 0
	hasExtends := r.Extends.Kind != 0
	if !hasImage && !hasBuild && !hasExtends {
		return inputf("Service %s needs an image or a build.", name)
	}
	for _, v := range r.Volumes {
		src, bind := volumeSource(v)
		if bind && strings.HasPrefix(src, "~") {
			return inputf("Service %s mounts %s. Use a full path instead of ~.", name, src)
		}
	}
	return nil
}

func (r rawService) toSvc(name string) svc {
	s := svc{
		name:      name,
		image:     strings.TrimSpace(r.Image),
		build:     r.Build.Kind != 0,
		container: strings.TrimSpace(r.ContainerName),
		priv:      boolNode(r.Privileged),
		netMode:   strings.TrimSpace(r.NetworkMode),
		pid:       scalarString(r.Pid),
		ipc:       scalarString(r.Ipc),
		caps:      stringList(r.CapAdd),
	}
	for _, v := range r.Volumes {
		if src, bind := volumeSource(v); bind {
			s.mounts = append(s.mounts, src)
		}
	}
	for _, d := range r.Devices {
		if src := deviceSource(d); src != "" {
			s.devices = append(s.devices, src)
		}
	}
	return s
}

func (f *parsed) interpolate(env map[string]string, base string) error {
	// Every scalar is checked so ${VAR:?message} fails in the editor, not
	// only after Compose starts. The stored file is left as the user wrote it.
	if err := walkScalars(f.doc, func(s string) error {
		_, err := interpolate(s, env)
		return err
	}); err != nil {
		return err
	}
	for i := range f.services {
		s := &f.services[i]
		var err error
		s.netMode, err = interpolate(s.netMode, env)
		if err != nil {
			return inputf("Service %s: %s", s.name, err.Error())
		}
		s.pid, err = interpolate(s.pid, env)
		if err != nil {
			return inputf("Service %s: %s", s.name, err.Error())
		}
		s.ipc, err = interpolate(s.ipc, env)
		if err != nil {
			return inputf("Service %s: %s", s.name, err.Error())
		}
		for j, c := range s.caps {
			s.caps[j], err = interpolate(c, env)
			if err != nil {
				return inputf("Service %s: %s", s.name, err.Error())
			}
		}
		for j, m := range s.mounts {
			m, err = interpolate(m, env)
			if err != nil {
				return inputf("Service %s: %s", s.name, err.Error())
			}
			s.mounts[j] = resolveHost(m, base)
		}
		for j, d := range s.devices {
			d, err = interpolate(d, env)
			if err != nil {
				return inputf("Service %s: %s", s.name, err.Error())
			}
			s.devices[j] = resolveHost(d, base)
		}
	}
	return nil
}

func (f *parsed) infos() []ServiceInfo {
	out := make([]ServiceInfo, 0, len(f.services))
	for _, s := range f.services {
		image := s.image
		if image == "" && s.build {
			image = "(build)"
		}
		out = append(out, ServiceInfo{Name: s.name, Image: image, Container: s.container})
	}
	return out
}

func (f *parsed) review(isolate bool) []Finding {
	var out []Finding
	for _, s := range f.services {
		spec := docker.Spec{
			Name:       s.name,
			Image:      "stack",
			Privileged: s.priv,
			Network:    "bridge",
		}
		mode := strings.ToLower(strings.TrimSpace(s.netMode))
		if mode == "host" {
			spec.Network = "host"
		}
		var extra []string
		for _, c := range s.caps {
			if c = strings.TrimSpace(c); c != "" {
				extra = append(extra, "--cap-add="+c)
			}
		}
		if strings.EqualFold(s.pid, "host") {
			extra = append(extra, "--pid=host")
		}
		if strings.EqualFold(s.ipc, "host") {
			extra = append(extra, "--ipc=host")
		}
		spec.ExtraParams = strings.Join(extra, " ")
		for _, m := range append(append([]string{}, s.mounts...), s.devices...) {
			spec.Configs = append(spec.Configs, docker.Config{Type: docker.TypePath, Host: m, Container: "/data"})
		}
		for _, found := range docker.Review(spec) {
			out = append(out, Finding{
				Service: s.name, ID: found.ID, Severity: found.Severity,
				Title: found.Title, Reason: found.Reason, Suggestion: found.Suggestion, Fix: found.Fix,
			})
		}
		if isolate && mode == "bridge" {
			out = append(out, Finding{
				Service: s.name, ID: "shared-bridge", Severity: docker.SeverityWarn,
				Title:      "Shared Docker bridge",
				Reason:     "This service uses the shared bridge, so it can reach other containers on that network.",
				Suggestion: "Remove network_mode so the service joins this stack's private network.",
				Fix:        true,
			})
		}
	}
	return out
}

// networkPlan is the bridge deploy will attach services to when the file
// does not define its own networks. An empty name means the file's networks
// are left as written.
func (f *parsed) networkPlan(isolate bool) (string, string) {
	if f.hasNets {
		if isolate {
			return "", "This file defines its own networks, so those are used. They stay private to this stack unless a network is marked external."
		}
		return "", "This file defines its own networks, so the shared-bridge switch does not move them."
	}
	return "", ""
}

func tidyYAML(err error) string {
	msg := err.Error()
	msg = strings.TrimPrefix(msg, "yaml: ")
	if len(msg) > 180 {
		msg = msg[:180]
	}
	return msg
}

func marshalNode(doc *yaml.Node) (string, error) {
	var buf bytes.Buffer
	enc := yaml.NewEncoder(&buf)
	enc.SetIndent(2)
	if err := enc.Encode(doc); err != nil {
		return "", err
	}
	if err := enc.Close(); err != nil {
		return "", err
	}
	return buf.String(), nil
}

func nestedAlias(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.AliasNode {
		return n.Alias != nil && containsAlias(n.Alias)
	}
	for _, c := range n.Content {
		if nestedAlias(c) {
			return true
		}
	}
	return false
}

func containsAlias(n *yaml.Node) bool {
	if n == nil {
		return false
	}
	if n.Kind == yaml.AliasNode {
		return true
	}
	for _, c := range n.Content {
		if containsAlias(c) {
			return true
		}
	}
	return false
}

func child(m *yaml.Node, key string) yaml.Node {
	if m == nil || m.Kind != yaml.MappingNode {
		return yaml.Node{}
	}
	for i := 0; i+1 < len(m.Content); i += 2 {
		if m.Content[i].Value == key {
			return *m.Content[i+1]
		}
	}
	return yaml.Node{}
}

func mappingKeys(m *yaml.Node) []string {
	if m == nil || m.Kind != yaml.MappingNode {
		return nil
	}
	var out []string
	for i := 0; i+1 < len(m.Content); i += 2 {
		out = append(out, m.Content[i].Value)
	}
	return out
}

func mappingScalar(m yaml.Node, key string) string {
	return scalarString(child(&m, key))
}

func scalarString(n yaml.Node) string {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return scalarString(*n.Alias)
	}
	if n.Kind != yaml.ScalarNode {
		return ""
	}
	return strings.TrimSpace(n.Value)
}

func boolNode(n yaml.Node) bool {
	v := strings.ToLower(scalarString(n))
	return v == "true" || v == "yes" || v == "y" || v == "on"
}

func stringList(n yaml.Node) []string {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return stringList(*n.Alias)
	}
	switch n.Kind {
	case yaml.ScalarNode:
		if strings.TrimSpace(n.Value) == "" {
			return nil
		}
		var out []string
		for _, part := range strings.Split(n.Value, ",") {
			part = strings.TrimSpace(part)
			if part != "" {
				out = append(out, part)
			}
		}
		return out
	case yaml.SequenceNode:
		var out []string
		for _, c := range n.Content {
			out = append(out, stringList(*c)...)
		}
		return out
	default:
		return nil
	}
}

func volumeSource(n yaml.Node) (string, bool) {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return volumeSource(*n.Alias)
	}
	switch n.Kind {
	case yaml.ScalarNode:
		return shortVolume(n.Value)
	case yaml.MappingNode:
		typ := strings.ToLower(mappingScalar(n, "type"))
		src := mappingScalar(n, "source")
		if src == "" {
			return "", false
		}
		switch typ {
		case "bind", "npipe":
			return src, true
		case "volume", "tmpfs":
			return "", false
		default:
			if isBindSource(src) {
				return src, true
			}
		}
	}
	return "", false
}

func shortVolume(spec string) (string, bool) {
	spec = strings.TrimSpace(spec)
	if spec == "" || !strings.Contains(spec, ":") {
		return "", false
	}
	parts := strings.Split(spec, ":")
	src := parts[0]
	if !isBindSource(src) {
		return "", false
	}
	return src, true
}

func isBindSource(s string) bool {
	if s == "" {
		return false
	}
	// A $VAR host side is reviewed after interpolation. That is how a socket
	// hidden in the env file is still caught before deploy.
	return s == "." || strings.HasPrefix(s, "./") || strings.HasPrefix(s, "../") ||
		strings.HasPrefix(s, "/") || strings.HasPrefix(s, "~") || strings.Contains(s, "/") ||
		strings.Contains(s, "$")
}

func deviceSource(n yaml.Node) string {
	if n.Kind == yaml.AliasNode && n.Alias != nil {
		return deviceSource(*n.Alias)
	}
	switch n.Kind {
	case yaml.ScalarNode:
		src, _ := shortVolume(n.Value)
		if src == "" && strings.HasPrefix(strings.TrimSpace(n.Value), "/") {
			return strings.TrimSpace(n.Value)
		}
		return src
	case yaml.MappingNode:
		return mappingScalar(n, "source")
	default:
		return ""
	}
}

func resolveHost(source, base string) string {
	source = strings.TrimSpace(source)
	if source == "" || strings.HasPrefix(source, "~") {
		return source
	}
	if filepath.IsAbs(source) {
		return filepath.Clean(source)
	}
	if base == "" {
		return filepath.Clean(source)
	}
	return filepath.Clean(filepath.Join(base, source))
}

func parseEnv(text string) (map[string]string, error) {
	out := map[string]string{}
	if strings.TrimSpace(text) == "" {
		return out, nil
	}
	for i, line := range strings.Split(text, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		line = strings.TrimPrefix(line, "export ")
		key, val, ok := strings.Cut(line, "=")
		key = strings.TrimSpace(key)
		if !ok || !envKeyRe.MatchString(key) {
			return nil, inputf("Line %d of the env file must look like NAME=value.", i+1)
		}
		val = strings.TrimSpace(val)
		if len(val) >= 2 {
			if (val[0] == '"' && val[len(val)-1] == '"') || (val[0] == '\'' && val[len(val)-1] == '\'') {
				val = val[1 : len(val)-1]
			}
		}
		if strings.ContainsRune(val, 0) {
			return nil, inputf("Line %d of the env file has an invalid value.", i+1)
		}
		out[key] = val
	}
	return out, nil
}

var envKeyRe = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

func walkScalars(n *yaml.Node, fn func(string) error) error {
	if n == nil {
		return nil
	}
	if n.Kind == yaml.ScalarNode {
		return fn(n.Value)
	}
	if n.Kind == yaml.AliasNode {
		return nil
	}
	for _, c := range n.Content {
		if err := walkScalars(c, fn); err != nil {
			return err
		}
	}
	return nil
}

func interpolate(s string, env map[string]string) (string, error) {
	if !strings.Contains(s, "$") {
		return s, nil
	}
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		if s[i] != '$' {
			b.WriteByte(s[i])
			continue
		}
		if i+1 < len(s) && s[i+1] == '$' {
			b.WriteByte('$')
			i++
			continue
		}
		if i+1 < len(s) && s[i+1] == '{' {
			end := strings.IndexByte(s[i+2:], '}')
			if end < 0 {
				return "", inputf("A ${...} variable is missing its closing brace.")
			}
			body := s[i+2 : i+2+end]
			i = i + 2 + end
			val, err := braced(body, env)
			if err != nil {
				return "", err
			}
			b.WriteString(val)
			continue
		}
		j := i + 1
		for j < len(s) && isEnvChar(s[j]) {
			j++
		}
		if j == i+1 {
			b.WriteByte('$')
			continue
		}
		name := s[i+1 : j]
		b.WriteString(env[name])
		i = j - 1
	}
	return b.String(), nil
}

func braced(body string, env map[string]string) (string, error) {
	name := body
	op, rest := "", ""
	for _, mark := range []string{":-", "-", ":?", "?"} {
		if i := strings.Index(body, mark); i > 0 {
			name, op, rest = body[:i], mark, body[i+len(mark):]
			break
		}
	}
	if !envKeyRe.MatchString(name) {
		return "", inputf("%q is not a valid variable name.", name)
	}
	val, ok := env[name]
	switch op {
	case "":
		return val, nil
	case "-", ":-":
		if !ok || (op == ":-" && val == "") {
			return rest, nil
		}
		return val, nil
	case "?", ":?":
		if !ok || (op == ":?" && val == "") {
			if rest == "" {
				rest = name + " is required"
			}
			return "", inputf("%s", rest)
		}
		return val, nil
	default:
		return val, nil
	}
}

func isEnvChar(c byte) bool {
	return c == '_' || (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9')
}

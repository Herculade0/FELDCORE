package docker

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"github.com/Herculade0/feldcore/internal/store"
)

// Labels FELDCORE sets on containers it manages.
const (
	GPULabel       = "feldcore.gpu"        // "true" when GPU access was granted
	gpuGroupsLabel = "feldcore.gpu.groups" // group ids added for GPU access
	labelPrefix    = "feldcore."
	composeLabel   = "com.docker.compose.project"
)

var (
	nameRe    = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
	imageRe   = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]{0,254}$`)
	networkRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.:-]{0,127}$`)
	volumeRe  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,127}$`)
	bindOptRe = regexp.MustCompile(`^[a-zA-Z]+(,[a-zA-Z]+)*$`)
	devPermRe = regexp.MustCompile(`^[rwm]{1,3}$`)
)

// normaliseSpec validates s and tidies it in place.
func normaliseSpec(s *Spec) error {
	s.Name = strings.TrimSpace(s.Name)
	if s.Name != "" && !nameRe.MatchString(s.Name) {
		return invalid("Container names use letters, numbers and _ . - and start with a letter or number.")
	}
	s.Image = strings.TrimSpace(s.Image)
	if !imageRe.MatchString(s.Image) {
		return invalid("Enter an image name, for example %q.", "nginx:latest")
	}
	s.Network = strings.TrimSpace(s.Network)
	if s.Network == "" || s.Network == "default" {
		s.Network = "bridge"
	}
	if !networkRe.MatchString(s.Network) {
		return invalid("%q is not a valid network name.", s.Network)
	}
	switch s.RestartPolicy {
	case "":
		s.RestartPolicy = "no"
	case "no", "always", "unless-stopped", "on-failure":
	default:
		return invalid("Unknown restart policy %q.", s.RestartPolicy)
	}

	seen := map[string]bool{}
	for i := range s.Configs {
		c := &s.Configs[i]
		c.Name = oneLine(c.Name, 80)
		c.Description = oneLine(c.Description, 400)
		label := c.Name
		if label == "" {
			label = "A " + c.Type + " setting"
		}

		switch c.Type {
		case TypePath:
			c.Host = trimSlash(strings.TrimSpace(c.Host))
			c.Container = strings.TrimSpace(c.Container)
			if c.Host == "" || c.Container == "" {
				return invalid("%s needs both a host path and a container path.", label)
			}
			// The host side is a folder on the server or a named Docker volume.
			if !path.IsAbs(c.Host) && !volumeRe.MatchString(c.Host) {
				return invalid("Host path %q must be absolute (start with /).", c.Host)
			}
			if !path.IsAbs(c.Container) {
				return invalid("Container path %q must be absolute (start with /).", c.Container)
			}
			if strings.Contains(c.Host+c.Container, ":") {
				return invalid("Paths cannot contain a colon.")
			}
			c.Container = path.Clean(c.Container)
			if path.IsAbs(c.Host) {
				c.Host = path.Clean(c.Host)
			}
			if c.Mode == "" {
				c.Mode = "rw"
			}
			if !bindOptRe.MatchString(c.Mode) {
				return invalid("%q is not a valid access mode.", c.Mode)
			}
		case TypePort:
			c.Proto = strings.ToLower(c.Proto)
			if c.Proto == "" {
				c.Proto = "tcp"
			}
			if c.Proto != "tcp" && c.Proto != "udp" {
				return invalid("Port type must be TCP or UDP.")
			}
			if c.ContainerPort < 1 || c.ContainerPort > 65535 {
				return invalid("%s needs a container port between 1 and 65535.", label)
			}
			if c.HostPort < 0 || c.HostPort > 65535 {
				return invalid("%s needs a host port between 1 and 65535.", label)
			}
		case TypeVariable, TypeLabel:
			c.Key = strings.TrimSpace(c.Key)
			if c.Key == "" || strings.ContainsAny(c.Key, "= \t\r\n") {
				return invalid("%q is not a valid %s name.", c.Key, c.Type)
			}
			if strings.ContainsRune(c.Value, 0) {
				return invalid("%s has an invalid value.", label)
			}
			if c.Type == TypeLabel && strings.HasPrefix(c.Key, labelPrefix) {
				return invalid("Labels starting with %q are reserved for FELDCORE.", labelPrefix)
			}
		case TypeDevice:
			c.Host = strings.TrimSpace(c.Host)
			c.Container = strings.TrimSpace(c.Container)
			if c.Container == "" {
				c.Container = c.Host
			}
			if !strings.HasPrefix(c.Host, "/dev/") || !path.IsAbs(c.Container) {
				return invalid("A device is a path under /dev, for example /dev/ttyUSB0.")
			}
			if strings.Contains(c.Host+c.Container, ":") {
				return invalid("Device paths cannot contain a colon.")
			}
			if c.Mode == "" {
				c.Mode = "rwm"
			}
			if !devPermRe.MatchString(c.Mode) {
				return invalid("Device permissions are some of r, w and m.")
			}
		default:
			return invalid("Unknown setting type %q.", c.Type)
		}

		key := configKey(*c)
		if c.Type == TypePort {
			// Two settings may target the same container port, but one host
			// port can only be published once.
			key = fmt.Sprintf("hostport:%d/%s", c.HostPort, c.Proto)
			if c.HostPort == 0 {
				continue
			}
		}
		if seen[key] {
			return invalid("%s is set twice: %s.", strings.ToUpper(c.Type[:1])+c.Type[1:], describeKey(*c))
		}
		seen[key] = true
	}
	return nil
}

// configKey identifies a setting stably across edits, for attaching the name
// and description FELDCORE stores for it.
func configKey(c Config) string {
	switch c.Type {
	case TypePath, TypeDevice:
		return c.Type + ":" + c.Container
	case TypePort:
		return fmt.Sprintf("port:%d/%s", c.ContainerPort, c.Proto)
	default:
		return c.Type + ":" + c.Key
	}
}

func describeKey(c Config) string {
	switch c.Type {
	case TypePath, TypeDevice:
		return c.Container
	case TypePort:
		return fmt.Sprintf("host port %d/%s", c.HostPort, c.Proto)
	default:
		return c.Key
	}
}

// metaOf extracts the names/descriptions worth storing from a spec.
func metaOf(s Spec) map[string]store.ConfigMeta {
	out := map[string]store.ConfigMeta{}
	for _, c := range s.Configs {
		if c.Name != "" || c.Description != "" {
			out[configKey(c)] = store.ConfigMeta{Name: c.Name, Description: c.Description}
		}
	}
	return out
}

// gpuGrant is what a container needs to use the host's GPU through /dev/dri.
type gpuGrant struct {
	Device string   // device directory to pass through
	Groups []string // numeric group ids that own the device nodes
}

// applySpec writes the settings FELDCORE manages onto raw Engine API Config and
// HostConfig maps, leaving every other key as it was. For a new container the
// maps start empty; for an edit they come from the existing container.
func applySpec(cfg, host map[string]any, s Spec, grant *gpuGrant) {
	prevGroups := strings.Split(strMapOf(cfg["Labels"])[gpuGroupsLabel], ",")

	env := []string{}
	labels := map[string]string{ManagedLabel: "true"}
	exposed := map[string]struct{}{}
	bindings := map[string][]portBinding{}
	binds := []string{}
	devices := []deviceMapping{}

	for _, c := range s.Configs {
		switch c.Type {
		case TypePath:
			binds = append(binds, c.Host+":"+c.Container+":"+c.Mode)
		case TypePort:
			key := fmt.Sprintf("%d/%s", c.ContainerPort, c.Proto)
			exposed[key] = struct{}{}
			hostPort := ""
			if c.HostPort > 0 {
				hostPort = strconv.Itoa(c.HostPort)
			}
			bindings[key] = append(bindings[key], portBinding{HostIP: c.HostIP, HostPort: hostPort})
		case TypeVariable:
			env = append(env, c.Key+"="+c.Value)
		case TypeLabel:
			labels[c.Key] = c.Value
		case TypeDevice:
			devices = append(devices, deviceMapping{c.Host, c.Container, c.Mode})
		}
	}

	// Group ids: keep any the user added themselves, drop the ones a previous
	// GPU grant added, then add the current grant's.
	groups := []string{}
	for _, g := range strSliceOf(host["GroupAdd"]) {
		if !contains(prevGroups, g) {
			groups = append(groups, g)
		}
	}
	if s.GPU && grant != nil {
		devices = append(devices, deviceMapping{grant.Device, grant.Device, "rwm"})
		labels[GPULabel] = "true"
		labels[gpuGroupsLabel] = strings.Join(grant.Groups, ",")
		for _, g := range grant.Groups {
			if !contains(groups, g) {
				groups = append(groups, g)
			}
		}
	}

	cfg["Image"] = s.Image
	cfg["Env"] = env
	cfg["Labels"] = labels
	cfg["ExposedPorts"] = exposed

	host["Binds"] = binds
	host["PortBindings"] = bindings
	host["Devices"] = devices
	host["GroupAdd"] = groups
	host["NetworkMode"] = s.Network
	policy := s.RestartPolicy
	if policy == "no" {
		policy = "" // the API wants "no" expressed as an empty name
	}
	host["RestartPolicy"] = map[string]any{"Name": policy, "MaximumRetryCount": 0}
}

// specFromInspect derives the editable Spec of an existing container.
//
// Environment variables and labels inherited unchanged from the image are
// left out: they are the image's defaults, not this container's settings, and
// Docker re-applies them on create anyway.
func specFromInspect(in *inspectContainer, imageCfg map[string]any, meta map[string]store.ConfigMeta) Spec {
	cfg, host := in.Config, in.HostConfig
	labels := strMapOf(cfg["Labels"])
	gpu := labels[GPULabel] == "true"

	s := Spec{
		Name:          strings.TrimPrefix(in.Name, "/"),
		Image:         strOf(cfg["Image"]),
		Network:       strOf(host["NetworkMode"]),
		RestartPolicy: strOf(mapOf(host["RestartPolicy"])["Name"]),
		GPU:           gpu,
		Configs:       []Config{},
	}
	if s.Network == "" || s.Network == "default" {
		s.Network = "bridge"
	}
	if s.RestartPolicy == "" {
		s.RestartPolicy = "no"
	}

	for _, b := range strSliceOf(host["Binds"]) {
		parts := strings.SplitN(b, ":", 3)
		if len(parts) < 2 {
			continue
		}
		mode := "rw"
		if len(parts) == 3 && parts[2] != "" {
			mode = parts[2]
		}
		s.Configs = append(s.Configs, Config{Type: TypePath, Host: parts[0], Container: parts[1], Mode: mode})
	}

	var ports []Config
	for key, v := range mapOf(host["PortBindings"]) {
		portStr, proto, _ := strings.Cut(key, "/")
		cport, err := strconv.Atoi(portStr)
		if err != nil {
			continue
		}
		if proto == "" {
			proto = "tcp"
		}
		list, _ := v.([]any)
		for _, b := range list {
			bm := mapOf(b)
			hport, _ := strconv.Atoi(strOf(bm["HostPort"]))
			ports = append(ports, Config{
				Type: TypePort, ContainerPort: cport, HostPort: hport,
				Proto: proto, HostIP: strOf(bm["HostIp"]),
			})
		}
	}
	sort.Slice(ports, func(i, j int) bool {
		if ports[i].ContainerPort != ports[j].ContainerPort {
			return ports[i].ContainerPort < ports[j].ContainerPort
		}
		return ports[i].Proto < ports[j].Proto
	})
	s.Configs = append(s.Configs, ports...)

	imageEnv := map[string]bool{}
	for _, e := range strSliceOf(imageCfg["Env"]) {
		imageEnv[e] = true
	}
	for _, e := range strSliceOf(cfg["Env"]) {
		if imageEnv[e] {
			continue
		}
		k, v, _ := strings.Cut(e, "=")
		s.Configs = append(s.Configs, Config{Type: TypeVariable, Key: k, Value: v})
	}

	imageLabels := strMapOf(imageCfg["Labels"])
	var keys []string
	for k, v := range labels {
		if strings.HasPrefix(k, labelPrefix) {
			continue
		}
		if iv, ok := imageLabels[k]; ok && iv == v {
			continue
		}
		keys = append(keys, k)
	}
	sort.Strings(keys)
	for _, k := range keys {
		s.Configs = append(s.Configs, Config{Type: TypeLabel, Key: k, Value: labels[k]})
	}

	devs, _ := host["Devices"].([]any)
	for _, d := range devs {
		dm := mapOf(d)
		hostPath := strOf(dm["PathOnHost"])
		if gpu && hostPath == driDir {
			continue // shown as the GPU switch, not as a raw device
		}
		perms := strOf(dm["CgroupPermissions"])
		if perms == "" {
			perms = "rwm"
		}
		s.Configs = append(s.Configs, Config{
			Type: TypeDevice, Host: hostPath, Container: strOf(dm["PathInContainer"]), Mode: perms,
		})
	}

	for i := range s.Configs {
		if m, ok := meta[configKey(s.Configs[i])]; ok {
			s.Configs[i].Name, s.Configs[i].Description = m.Name, m.Description
		}
	}
	return s
}

// recreateBody builds the create request for replacing an existing container:
// its full current configuration with the spec's settings applied on top.
func recreateBody(in *inspectContainer, imageCfg map[string]any, s Spec, grant *gpuGrant) map[string]any {
	cfg, host := cloneMap(in.Config), cloneMap(in.HostConfig)
	oldNetwork := strOf(in.HostConfig["NetworkMode"])
	if oldNetwork == "" || oldNetwork == "default" {
		oldNetwork = "bridge"
	}

	// Inspect reports image defaults as if they were set on the container.
	// Sending them back would pin them, so a later image update could not
	// change its own entrypoint or command. Drop anything equal to the image's.
	for _, k := range []string{"Cmd", "Entrypoint", "WorkingDir", "User", "Shell", "StopSignal", "Healthcheck", "Volumes"} {
		if jsonEqual(cfg[k], imageCfg[k]) {
			delete(cfg, k)
		}
	}
	// The default hostname is the old container's id; let the new one get its own.
	if hn := strOf(cfg["Hostname"]); hn != "" && strings.HasPrefix(in.ID, hn) {
		delete(cfg, "Hostname")
	}

	applySpec(cfg, host, s, grant)

	body := cfg
	body["HostConfig"] = host

	// On a user-defined network, carry over fixed addresses and aliases.
	if isCustomNetwork(s.Network) {
		endpoints := map[string]any{}
		if s.Network == oldNetwork {
			for name, settings := range in.NetworkSettings.Networks {
				ep := map[string]any{}
				if v, ok := settings["IPAMConfig"]; ok && v != nil {
					ep["IPAMConfig"] = v
				}
				var aliases []string
				for _, a := range strSliceOf(settings["Aliases"]) {
					if !strings.HasPrefix(in.ID, a) { // skip the old container id
						aliases = append(aliases, a)
					}
				}
				if len(aliases) > 0 {
					ep["Aliases"] = aliases
				}
				endpoints[name] = ep
			}
		}
		if _, ok := endpoints[s.Network]; !ok {
			endpoints[s.Network] = map[string]any{}
		}
		body["NetworkingConfig"] = map[string]any{"EndpointsConfig": endpoints}
	}
	return body
}

func isCustomNetwork(n string) bool {
	switch n {
	case "", "bridge", "default", "host", "none":
		return false
	}
	return !strings.HasPrefix(n, "container:")
}

// --- helpers for the raw (map[string]any) Engine API documents ---

func strOf(v any) string {
	switch t := v.(type) {
	case string:
		return t
	case json.Number:
		return t.String()
	}
	return ""
}

func mapOf(v any) map[string]any {
	m, _ := v.(map[string]any)
	return m
}

func strSliceOf(v any) []string {
	list, _ := v.([]any)
	out := make([]string, 0, len(list))
	for _, item := range list {
		if s, ok := item.(string); ok {
			out = append(out, s)
		}
	}
	return out
}

func strMapOf(v any) map[string]string {
	out := map[string]string{}
	for k, val := range mapOf(v) {
		if s, ok := val.(string); ok {
			out[k] = s
		}
	}
	return out
}

// decodeRaw parses JSON keeping numbers exact, so large values survive a
// round trip back to the daemon unchanged.
func decodeRaw(data []byte, out any) error {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	return dec.Decode(out)
}

func cloneMap(m map[string]any) map[string]any {
	out := map[string]any{}
	if raw, err := json.Marshal(m); err == nil {
		_ = decodeRaw(raw, &out)
	}
	return out
}

func jsonEqual(a, b any) bool {
	ra, _ := json.Marshal(a)
	rb, _ := json.Marshal(b)
	return bytes.Equal(ra, rb)
}

func contains(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

func trimSlash(p string) string {
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	return p
}

// oneLine collapses whitespace and control characters and limits length.
func oneLine(s string, max int) string {
	s = strings.Join(strings.Fields(strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return ' '
		}
		return r
	}, s)), " ")
	if r := []rune(s); len(r) > max {
		s = strings.TrimSpace(string(r[:max]))
	}
	return s
}

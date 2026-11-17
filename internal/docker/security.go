package docker

import (
	"path"
	"strings"
)

// Severity is how serious a finding is. High-risk findings have to be
// acknowledged with Deploy anyway. Warnings are shown and do not block.
const (
	SeverityHigh = "high"
	SeverityWarn = "warn"
)

// Finding is one risky thing about a container spec, in plain language.
type Finding struct {
	ID         string `json:"id"`
	Severity   string `json:"severity"`
	Title      string `json:"title"`
	Reason     string `json:"reason"`
	Suggestion string `json:"suggestion"`
	// Fix is true when ApplySafer can make the change itself.
	Fix bool `json:"fix"`
}

// rule is one row of the security table. Kind and Arg say how it matches
// and how the safer edit is applied. The words are what the setup page shows.
type rule struct {
	ID         string
	Severity   string
	Title      string
	Reason     string
	Suggestion string
	Kind       string
	Arg        string
}

// securityRules is the whole check. Review and ApplySafer both walk this
// table and nothing else.
var securityRules = []rule{
	{
		ID: "privileged", Severity: SeverityHigh, Kind: "privileged",
		Title:      "Privileged mode",
		Reason:     "Privileged mode lets this container change the server the way a root user can, including devices and other containers.",
		Suggestion: "Turn privileged mode off. Add only the device or folder the app needs.",
	},
	{
		ID: "docker.sock", Severity: SeverityHigh, Kind: "sock",
		Title:      "Docker socket",
		Reason:     "Mounting the Docker socket lets this container control every container and, through that, the server itself.",
		Suggestion: "Remove the Docker socket. An app that only needs to show its own files does not need it.",
	},
	{
		ID: "host-network", Severity: SeverityHigh, Kind: "network", Arg: "host",
		Title:      "Host network",
		Reason:     "Host networking places this container on the server's own network, so it can reach every local service and open ports directly on the server.",
		Suggestion: "Use a private network and publish only the ports you want to reach.",
	},
	{
		ID: "host-pid", Severity: SeverityHigh, Kind: "pid", Arg: "host",
		Title:      "Host processes",
		Reason:     "Sharing the host process namespace lets this container see and signal programs running on the server.",
		Suggestion: "Remove host PID mode so the container only sees its own processes.",
	},
	{
		ID: "host-ipc", Severity: SeverityHigh, Kind: "ipc", Arg: "host",
		Title:      "Host shared memory",
		Reason:     "Sharing the host IPC namespace lets this container read and write memory that other programs on the server use to talk to each other.",
		Suggestion: "Remove host IPC mode so the container keeps its own shared memory.",
	},
	{
		ID: "cap:SYS_ADMIN", Severity: SeverityHigh, Kind: "cap", Arg: "SYS_ADMIN",
		Title:      "SYS_ADMIN capability",
		Reason:     "SYS_ADMIN lets the container mount filesystems and change namespaces, which removes most of the boundary Docker puts around an app.",
		Suggestion: "Remove SYS_ADMIN.",
	},
	{
		ID: "cap:NET_ADMIN", Severity: SeverityHigh, Kind: "cap", Arg: "NET_ADMIN",
		Title:      "NET_ADMIN capability",
		Reason:     "NET_ADMIN lets the container change interfaces, routes and firewall rules.",
		Suggestion: "Remove NET_ADMIN unless this app is a VPN client that cannot start without it.",
	},
	{
		ID: "cap:SYS_MODULE", Severity: SeverityHigh, Kind: "cap", Arg: "SYS_MODULE",
		Title:      "SYS_MODULE capability",
		Reason:     "SYS_MODULE lets the container load and unload kernel modules.",
		Suggestion: "Remove SYS_MODULE.",
	},
	{
		ID: "cap:SYS_PTRACE", Severity: SeverityHigh, Kind: "cap", Arg: "SYS_PTRACE",
		Title:      "SYS_PTRACE capability",
		Reason:     "SYS_PTRACE lets the container inspect and change other processes.",
		Suggestion: "Remove SYS_PTRACE.",
	},
	{
		ID: "cap:SYS_RAWIO", Severity: SeverityHigh, Kind: "cap", Arg: "SYS_RAWIO",
		Title:      "SYS_RAWIO capability",
		Reason:     "SYS_RAWIO lets the container read and write disks directly, past the filesystem.",
		Suggestion: "Remove SYS_RAWIO.",
	},
	{
		ID: "cap:DAC_OVERRIDE", Severity: SeverityHigh, Kind: "cap", Arg: "DAC_OVERRIDE",
		Title:      "DAC_OVERRIDE capability",
		Reason:     "DAC_OVERRIDE lets the container ignore file permissions.",
		Suggestion: "Remove DAC_OVERRIDE.",
	},
	{
		ID: "cap:ALL", Severity: SeverityHigh, Kind: "cap", Arg: "ALL",
		Title:      "Every capability",
		Reason:     "Adding every capability gives the container the same kind of control as privileged mode.",
		Suggestion: "Remove the ALL capability and add only a specific one the app documents.",
	},
	{
		ID: "cap:NET_RAW", Severity: SeverityWarn, Kind: "cap", Arg: "NET_RAW",
		Title:      "NET_RAW capability",
		Reason:     "NET_RAW lets the container craft its own network packets.",
		Suggestion: "Remove NET_RAW unless the app needs ping or a similar raw socket.",
	},
	{
		ID: "cap:SYS_NICE", Severity: SeverityWarn, Kind: "cap", Arg: "SYS_NICE",
		Title:      "SYS_NICE capability",
		Reason:     "SYS_NICE lets the container change process priority on the server.",
		Suggestion: "Remove SYS_NICE unless the app needs to set its own priority.",
	},
	{
		ID: "mount:/", Severity: SeverityHigh, Kind: "mount", Arg: "/",
		Title:      "Whole filesystem mounted",
		Reason:     "Mounting / gives this container the server's entire disk, including other apps and system files.",
		Suggestion: "Remove this mount and map only the folder the app needs.",
	},
	{
		ID: "mount:/etc", Severity: SeverityHigh, Kind: "mount", Arg: "/etc",
		Title:      "Host configuration mounted",
		Reason:     "Mounting /etc gives this container the server's accounts, keys and service settings.",
		Suggestion: "Remove the /etc mount. Set the TZ variable if the app only needs the time zone.",
	},
	{
		ID: "mount:/root", Severity: SeverityHigh, Kind: "mount", Arg: "/root",
		Title:      "Administrator home mounted",
		Reason:     "Mounting /root gives this container the administrator's home directory, where keys and tokens are often stored.",
		Suggestion: "Remove the /root mount and use a folder created for this app.",
	},
}

type posture struct {
	Privileged bool
	Network    string
	Pid        string
	Ipc        string
	Caps       []string
	Mounts     []string
}

// Review reads a container spec and returns every rule that matches.
// The result is never nil.
func Review(s Spec) []Finding {
	p := postureOf(s)
	out := []Finding{}
	for _, rule := range securityRules {
		if !rule.hit(p) {
			continue
		}
		out = append(out, Finding{
			ID: rule.ID, Severity: rule.Severity, Title: rule.Title,
			Reason: rule.Reason, Suggestion: rule.Suggestion, Fix: true,
		})
	}
	return out
}

// NeedsAck reports whether deploying s requires an explicit Deploy anyway.
func NeedsAck(findings []Finding) bool {
	for _, f := range findings {
		if f.Severity == SeverityHigh {
			return true
		}
	}
	return false
}

// ApplySafer applies the trivial fix for each listed finding id and returns
// the updated spec. Unknown ids are ignored. The caller's spec is left as it was.
func ApplySafer(s Spec, ids []string) Spec {
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	if len(want) == 0 {
		return s
	}
	s.Configs = append([]Config(nil), s.Configs...)
	for _, rule := range securityRules {
		if want[rule.ID] {
			s = applyRule(s, rule)
		}
	}
	s.ExtraParams = strings.TrimSpace(s.ExtraParams)
	return s
}

func (r rule) hit(p posture) bool {
	switch r.Kind {
	case "privileged":
		return p.Privileged
	case "sock":
		for _, m := range p.Mounts {
			if isDockerSock(m) {
				return true
			}
		}
	case "network":
		return p.Network == r.Arg
	case "pid":
		return strings.EqualFold(p.Pid, r.Arg)
	case "ipc":
		return strings.EqualFold(p.Ipc, r.Arg)
	case "cap":
		for _, c := range p.Caps {
			if c == r.Arg {
				return true
			}
		}
	case "mount":
		for _, m := range p.Mounts {
			if broadPath(m) == r.Arg {
				return true
			}
		}
	}
	return false
}

func applyRule(s Spec, r rule) Spec {
	switch r.Kind {
	case "privileged":
		s.Privileged = false
		s.ExtraParams = stripFlag(s.ExtraParams, "privileged", func(string) bool { return true })
	case "network":
		if s.Network == "host" {
			s.Network = "bridge"
			s.Isolate = IsolatePrivate
		}
		s.ExtraParams = stripFlag(s.ExtraParams, "network", func(v string) bool { return strings.EqualFold(v, "host") })
		s.ExtraParams = stripFlag(s.ExtraParams, "net", func(v string) bool { return strings.EqualFold(v, "host") })
	case "pid":
		s.ExtraParams = stripFlag(s.ExtraParams, "pid", func(v string) bool { return strings.EqualFold(v, "host") })
	case "ipc":
		s.ExtraParams = stripFlag(s.ExtraParams, "ipc", func(v string) bool { return strings.EqualFold(v, "host") })
	case "cap":
		s.ExtraParams = stripCap(s.ExtraParams, r.Arg)
	case "mount":
		s.Configs = dropMounts(s.Configs, func(host string) bool { return broadPath(host) == r.Arg })
		s.ExtraParams = stripMountFlags(s.ExtraParams, func(host string) bool { return broadPath(host) == r.Arg })
	case "sock":
		s.Configs = dropMounts(s.Configs, isDockerSock)
		s.ExtraParams = stripMountFlags(s.ExtraParams, isDockerSock)
	}
	return s
}

func postureOf(s Spec) posture {
	extra := parseExtra(s.ExtraParams)
	p := posture{
		Privileged: s.Privileged || extra.Privileged,
		Network:    strings.TrimSpace(s.Network),
		Pid:        extra.pidMode,
		Ipc:        extra.ipcMode,
	}
	if n := flaggedValue(s.ExtraParams, "network", "net"); strings.EqualFold(n, "host") {
		p.Network = "host"
	}
	if p.Network == "" || p.Network == "default" {
		p.Network = "bridge"
	}
	seen := map[string]bool{}
	addCap := func(c string) {
		for _, part := range strings.Split(c, ",") {
			part = normCap(part)
			if part == "" || seen[part] {
				continue
			}
			seen[part] = true
			p.Caps = append(p.Caps, part)
		}
	}
	for _, c := range extra.capAdd {
		addCap(c)
	}
	addMount := func(host string) {
		host = strings.TrimSpace(host)
		if host != "" {
			p.Mounts = append(p.Mounts, host)
		}
	}
	for _, c := range s.Configs {
		if c.Type == TypePath || c.Type == TypeDevice {
			addMount(c.Host)
		}
	}
	for _, c := range extra.Configs {
		if c.Type == TypePath || c.Type == TypeDevice {
			addMount(c.Host)
		}
	}
	for _, src := range mountFlagSources(s.ExtraParams) {
		addMount(src)
	}
	return p
}

func normCap(s string) string {
	s = strings.ToUpper(strings.TrimSpace(s))
	s = strings.TrimPrefix(s, "CAP_")
	return s
}

func normCaps(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, c := range in {
		c = normCap(c)
		if c == "" || seen[c] {
			continue
		}
		seen[c] = true
		out = append(out, c)
	}
	return out
}

func isDockerSock(host string) bool {
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

func dropMounts(in []Config, drop func(string) bool) []Config {
	out := make([]Config, 0, len(in))
	for _, c := range in {
		if (c.Type == TypePath || c.Type == TypeDevice) && drop(c.Host) {
			continue
		}
		out = append(out, c)
	}
	return out
}

// stripFlag removes --name or --name=value (and the separate value argument)
// when the value matches. A bare flag with no value is removed when match is
// true for the empty string.
func stripFlag(extra, name string, match func(value string) bool) string {
	args := splitArgs(extra)
	var out []string
	for i := 0; i < len(args); i++ {
		flag, inline, kind := flagOf(args[i])
		if kind == flagNone || flag != name {
			out = append(out, args[i])
			continue
		}
		val := inline
		if kind == flagBare && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			val = args[i+1]
			if match(val) {
				i++
				continue
			}
			out = append(out, args[i])
			continue
		}
		if match(val) {
			continue
		}
		out = append(out, args[i])
	}
	return strings.Join(out, " ")
}

func stripCap(extra, cap string) string {
	cap = normCap(cap)
	args := splitArgs(extra)
	var out []string
	for i := 0; i < len(args); i++ {
		flag, inline, kind := flagOf(args[i])
		if kind == flagNone || flag != "cap-add" {
			out = append(out, args[i])
			continue
		}
		val := inline
		separate := false
		if kind == flagBare {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				continue
			}
			i++
			val = args[i]
			separate = true
		}
		kept := filterCaps(val, cap)
		if kept == "" {
			continue
		}
		if separate {
			out = append(out, "--cap-add", kept)
			continue
		}
		out = append(out, "--cap-add="+kept)
	}
	return strings.Join(out, " ")
}

func filterCaps(list, drop string) string {
	var kept []string
	for _, part := range strings.Split(list, ",") {
		if normCap(part) == "" || normCap(part) == drop {
			continue
		}
		kept = append(kept, strings.TrimSpace(part))
	}
	return strings.Join(kept, ",")
}

func stripMountFlags(extra string, drop func(string) bool) string {
	args := splitArgs(extra)
	var out []string
	for i := 0; i < len(args); i++ {
		flag, inline, kind := flagOf(args[i])
		if kind == flagNone || (flag != "volume" && flag != "v" && flag != "mount") {
			out = append(out, args[i])
			continue
		}
		val := inline
		separate := false
		if kind == flagBare {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				out = append(out, args[i])
				continue
			}
			i++
			val = args[i]
			separate = true
		}
		host := ""
		if flag == "mount" {
			host = mountSource(val)
		} else {
			host = volumeHost(val)
		}
		if host != "" && drop(host) {
			continue
		}
		if separate {
			out = append(out, args[i-1], val)
			continue
		}
		out = append(out, args[i])
	}
	return strings.Join(out, " ")
}

func volumeHost(spec string) string {
	parts := strings.Split(spec, ":")
	if len(parts) < 2 {
		return ""
	}
	return parts[0]
}

func mountSource(spec string) string {
	for _, part := range strings.Split(spec, ",") {
		k, v, ok := strings.Cut(part, "=")
		if !ok {
			continue
		}
		switch strings.ToLower(strings.TrimSpace(k)) {
		case "source", "src":
			return strings.TrimSpace(v)
		}
	}
	return ""
}

// flaggedValue returns the last value of any of the named flags.
func flaggedValue(extra string, names ...string) string {
	want := map[string]bool{}
	for _, n := range names {
		want[n] = true
	}
	args := splitArgs(extra)
	got := ""
	for i := 0; i < len(args); i++ {
		flag, inline, kind := flagOf(args[i])
		if kind == flagNone || !want[flag] {
			continue
		}
		if kind == flagInline {
			got = inline
			continue
		}
		if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			i++
			got = args[i]
		}
	}
	return got
}

func mountFlagSources(extra string) []string {
	args := splitArgs(extra)
	var out []string
	for i := 0; i < len(args); i++ {
		flag, inline, kind := flagOf(args[i])
		if kind == flagNone || flag != "mount" {
			continue
		}
		val := inline
		if kind == flagBare {
			if i+1 >= len(args) || strings.HasPrefix(args[i+1], "-") {
				continue
			}
			i++
			val = args[i]
		}
		if src := mountSource(val); src != "" {
			out = append(out, src)
		}
	}
	return out
}

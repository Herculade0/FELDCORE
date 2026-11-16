package docker

import (
	"strconv"
	"strings"
)

// extraEffect is the part of an Unraid ExtraParams string FELDCORE understands.
// Config-level flags (ports, paths, variables, devices) and the restart and
// privileged flags are lifted onto the spec so the setup page can show them
// in the right place. Everything else that we can express on the Engine API
// stays in Cleaned and is applied when the container is created.
type extraEffect struct {
	Restart    string
	Privileged bool
	Configs    []Config
	Unparsed   []string
	Cleaned    string

	capAdd, capDrop []string
	shmSize         *int64
	memory          *int64
	pidsLimit       *int64
	nanoCPUs        *int64
	stopTimeout     *int
	sysctls         map[string]string
	securityOpt     []string
	dns             []string
	tmpfs           map[string]string
	logOpts         map[string]string
	extraHosts      []string
	groupAdd        []string
	ulimits         []ulimitSpec
	hostname        string
	user            string
	cgroupns        string
	entrypoint      string
	pidMode         string
	ipcMode         string
	readOnly        bool
	init            bool
	noHealth        bool
}

type ulimitSpec struct {
	name       string
	soft, hard int64
}

// applyExtra writes extra parameters onto an Engine API body.
//
// previous is the extra string stored on the container being replaced. Its
// effects are removed first, so deleting a flag in the setup page actually
// drops it. Keys the string does not mention are left alone: an edit must
// not wipe memory limits or capabilities that came from somewhere else.
func applyExtra(cfg, host map[string]any, current, previous string) {
	if previous != "" {
		undoExtra(cfg, host, parseExtra(previous))
	}
	if current != "" {
		setExtra(cfg, host, parseExtra(current))
	}
}

func undoExtra(cfg, host map[string]any, e extraEffect) {
	if len(e.capAdd) > 0 {
		delete(host, "CapAdd")
	}
	if len(e.capDrop) > 0 {
		delete(host, "CapDrop")
	}
	if e.shmSize != nil {
		delete(host, "ShmSize")
	}
	if e.memory != nil {
		delete(host, "Memory")
	}
	if e.pidsLimit != nil {
		delete(host, "PidsLimit")
	}
	if e.nanoCPUs != nil {
		delete(host, "NanoCpus")
	}
	if e.stopTimeout != nil {
		delete(host, "StopTimeout")
	}
	if len(e.sysctls) > 0 {
		delete(host, "Sysctls")
	}
	if len(e.securityOpt) > 0 {
		delete(host, "SecurityOpt")
	}
	if len(e.dns) > 0 {
		delete(host, "Dns")
	}
	if len(e.tmpfs) > 0 {
		delete(host, "Tmpfs")
	}
	if len(e.logOpts) > 0 {
		delete(host, "LogConfig")
	}
	if len(e.extraHosts) > 0 {
		delete(host, "ExtraHosts")
	}
	if len(e.ulimits) > 0 {
		delete(host, "Ulimits")
	}
	if e.hostname != "" {
		delete(cfg, "Hostname")
	}
	if e.user != "" {
		delete(cfg, "User")
	}
	if e.cgroupns != "" {
		delete(host, "CgroupnsMode")
	}
	if e.entrypoint != "" {
		delete(cfg, "Entrypoint")
	}
	if e.pidMode != "" {
		delete(host, "PidMode")
	}
	if e.ipcMode != "" {
		delete(host, "IpcMode")
	}
	if e.readOnly {
		delete(host, "ReadonlyRootfs")
	}
	if e.init {
		delete(host, "Init")
	}
	if e.noHealth {
		delete(cfg, "Healthcheck")
	}
	if e.Privileged {
		delete(host, "Privileged")
	}
	if len(e.groupAdd) > 0 {
		drop := map[string]int{}
		for _, g := range e.groupAdd {
			drop[g]++
		}
		var next []string
		for _, g := range stringList(host["GroupAdd"]) {
			if drop[g] > 0 {
				drop[g]--
				continue
			}
			next = append(next, g)
		}
		host["GroupAdd"] = next
	}
	if e.Restart != "" {
		// The setup page's restart policy is the source of truth and is
		// written by applySpec. Nothing to undo here.
	}
}

func setExtra(cfg, host map[string]any, e extraEffect) {
	if len(e.capAdd) > 0 {
		host["CapAdd"] = e.capAdd
	}
	if len(e.capDrop) > 0 {
		host["CapDrop"] = e.capDrop
	}
	if e.shmSize != nil {
		host["ShmSize"] = *e.shmSize
	}
	if e.memory != nil {
		host["Memory"] = *e.memory
	}
	if e.pidsLimit != nil {
		host["PidsLimit"] = *e.pidsLimit
	}
	if e.nanoCPUs != nil {
		host["NanoCpus"] = *e.nanoCPUs
	}
	if e.stopTimeout != nil {
		host["StopTimeout"] = *e.stopTimeout
	}
	if len(e.sysctls) > 0 {
		host["Sysctls"] = e.sysctls
	}
	if len(e.securityOpt) > 0 {
		host["SecurityOpt"] = e.securityOpt
	}
	if len(e.dns) > 0 {
		host["Dns"] = e.dns
	}
	if len(e.tmpfs) > 0 {
		host["Tmpfs"] = e.tmpfs
	}
	if len(e.logOpts) > 0 {
		host["LogConfig"] = map[string]any{"Type": "json-file", "Config": e.logOpts}
	}
	if len(e.extraHosts) > 0 {
		host["ExtraHosts"] = e.extraHosts
	}
	if len(e.ulimits) > 0 {
		ul := make([]map[string]any, len(e.ulimits))
		for i, u := range e.ulimits {
			ul[i] = map[string]any{"Name": u.name, "Soft": u.soft, "Hard": u.hard}
		}
		host["Ulimits"] = ul
	}
	if e.hostname != "" {
		cfg["Hostname"] = e.hostname
	}
	if e.user != "" {
		cfg["User"] = e.user
	}
	if e.cgroupns != "" {
		host["CgroupnsMode"] = e.cgroupns
	}
	if e.entrypoint != "" {
		cfg["Entrypoint"] = []string{e.entrypoint}
	}
	if e.pidMode != "" {
		host["PidMode"] = e.pidMode
	}
	if e.ipcMode != "" {
		host["IpcMode"] = e.ipcMode
	}
	if e.readOnly {
		host["ReadonlyRootfs"] = true
	}
	if e.init {
		host["Init"] = true
	}
	if e.noHealth {
		cfg["Healthcheck"] = map[string]any{"Test": []string{"NONE"}}
	}
	if e.Privileged {
		host["Privileged"] = true
	}
	if e.Restart != "" {
		policy := e.Restart
		if policy == "no" {
			policy = ""
		}
		host["RestartPolicy"] = map[string]any{"Name": policy, "MaximumRetryCount": 0}
	}
	if len(e.groupAdd) > 0 {
		groups := stringList(host["GroupAdd"])
		for _, g := range e.groupAdd {
			if !contains(groups, g) {
				groups = append(groups, g)
			}
		}
		host["GroupAdd"] = groups
	}

	// Flags the user typed back in (--publish, --env, ...) after the template
	// converter had lifted the originals onto the form.
	exposed, _ := cfg["ExposedPorts"].(map[string]struct{})
	if exposed == nil {
		exposed = map[string]struct{}{}
	}
	bindings, _ := host["PortBindings"].(map[string][]portBinding)
	if bindings == nil {
		bindings = map[string][]portBinding{}
	}
	env := stringList(cfg["Env"])
	binds := stringList(host["Binds"])
	devs, _ := host["Devices"].([]deviceMapping)
	portsTouched, envTouched, bindsTouched, devsTouched := false, false, false, false
	for _, c := range e.Configs {
		switch c.Type {
		case TypePort:
			key := strconv.Itoa(c.ContainerPort) + "/" + c.Proto
			if _, ok := exposed[key]; ok {
				continue
			}
			exposed[key] = struct{}{}
			bindings[key] = append(bindings[key], portBinding{HostIP: c.HostIP, HostPort: strconv.Itoa(c.HostPort)})
			portsTouched = true
		case TypeVariable:
			if envHas(env, c.Key) {
				continue
			}
			env = append(env, c.Key+"="+c.Value)
			envTouched = true
		case TypePath:
			binds = append(binds, c.Host+":"+c.Container+":"+c.Mode)
			bindsTouched = true
		case TypeDevice:
			devs = append(devs, deviceMapping{c.Host, c.Container, c.Mode})
			devsTouched = true
		}
	}
	if portsTouched {
		cfg["ExposedPorts"] = exposed
		host["PortBindings"] = bindings
	}
	if envTouched {
		cfg["Env"] = env
	}
	if bindsTouched {
		host["Binds"] = binds
	}
	if devsTouched {
		host["Devices"] = devs
	}
}

func envHas(env []string, key string) bool {
	prefix := key + "="
	for _, e := range env {
		if e == key || strings.HasPrefix(e, prefix) {
			return true
		}
	}
	return false
}

func stringList(v any) []string {
	switch t := v.(type) {
	case []string:
		return append([]string(nil), t...)
	default:
		return strSliceOf(v)
	}
}

// parseExtra splits an Unraid extra-parameters string. Tokens FELDCORE shows
// elsewhere (restart, privileged, ports, paths, variables, devices) are
// removed from Cleaned; host-level flags stay there so the setup page still
// lists them and applyExtra can honour them.
func parseExtra(raw string) extraEffect {
	args := splitArgs(raw)
	consumed := make([]bool, len(args))
	var e extraEffect
	for i := 0; i < len(args); i++ {
		flagAt := i
		name, inline, kind := flagOf(args[i])
		if kind == flagNone {
			e.Unparsed = append(e.Unparsed, args[i])
			continue
		}
		take := func() (string, bool) {
			if kind == flagInline {
				return inline, true
			}
			if i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				return args[i], true
			}
			return "", false
		}
		consume := func() {
			consumed[flagAt] = true
			if i != flagAt {
				consumed[i] = true
			}
		}
		switch name {
		case "restart":
			v, ok := take()
			if ok {
				if pol := normaliseRestart(v); pol != "" {
					e.Restart = pol
					consume()
				} else {
					e.Unparsed = append(e.Unparsed, args[flagAt])
				}
			}
		case "privileged":
			e.Privileged = true
			consume()
		case "publish", "p":
			if v, ok := take(); ok {
				if c, ok := parsePublish(v); ok {
					e.Configs = append(e.Configs, c)
					consume()
				} else {
					e.Unparsed = append(e.Unparsed, "--publish "+v)
				}
			}
		case "volume", "v":
			if v, ok := take(); ok {
				if c, ok := parseVolume(v); ok {
					e.Configs = append(e.Configs, c)
					consume()
				} else {
					e.Unparsed = append(e.Unparsed, "--volume "+v)
				}
			}
		case "env", "e":
			if v, ok := take(); ok {
				if c, ok := parseEnvFlag(v); ok {
					e.Configs = append(e.Configs, c)
					consume()
				} else {
					e.Unparsed = append(e.Unparsed, "--env "+v)
				}
			}
		case "device":
			if v, ok := take(); ok {
				if c, ok := parseDeviceFlag(v); ok {
					e.Configs = append(e.Configs, c)
					consume()
				} else {
					e.Unparsed = append(e.Unparsed, "--device "+v)
				}
			}
		case "cap-add":
			if v, ok := take(); ok && v != "" {
				e.capAdd = append(e.capAdd, v)
			}
		case "cap-drop":
			if v, ok := take(); ok && v != "" {
				e.capDrop = append(e.capDrop, v)
			}
		case "shm-size":
			if v, ok := take(); ok {
				if n, ok := parseBytes(v); ok {
					e.shmSize = &n
				}
			}
		case "memory", "m":
			if v, ok := take(); ok {
				if n, ok := parseBytes(v); ok {
					e.memory = &n
				}
			}
		case "pids-limit":
			if v, ok := take(); ok {
				if n, err := strconv.ParseInt(v, 10, 64); err == nil && n >= 0 {
					e.pidsLimit = &n
				}
			}
		case "cpus":
			if v, ok := take(); ok {
				if n, err := strconv.ParseFloat(v, 64); err == nil && n >= 0 {
					nano := int64(n * 1e9)
					e.nanoCPUs = &nano
				}
			}
		case "stop-timeout":
			if v, ok := take(); ok {
				if n, err := strconv.Atoi(v); err == nil && n >= 0 {
					e.stopTimeout = &n
				}
			}
		case "sysctl":
			if v, ok := take(); ok {
				k, val, ok := strings.Cut(v, "=")
				if ok && k != "" {
					if e.sysctls == nil {
						e.sysctls = map[string]string{}
					}
					e.sysctls[k] = val
				}
			}
		case "security-opt":
			if v, ok := take(); ok && v != "" {
				e.securityOpt = append(e.securityOpt, v)
			}
		case "dns":
			if v, ok := take(); ok && v != "" {
				e.dns = append(e.dns, v)
			}
		case "tmpfs":
			if v, ok := take(); ok && v != "" {
				path, opt, _ := strings.Cut(v, ":")
				if path != "" {
					if e.tmpfs == nil {
						e.tmpfs = map[string]string{}
					}
					e.tmpfs[path] = opt
				}
			}
		case "log-opt":
			if v, ok := take(); ok {
				k, val, ok := strings.Cut(v, "=")
				if ok && k != "" {
					if e.logOpts == nil {
						e.logOpts = map[string]string{}
					}
					e.logOpts[k] = val
				}
			}
		case "add-host":
			if v, ok := take(); ok && v != "" {
				e.extraHosts = append(e.extraHosts, v)
			}
		case "group-add":
			if v, ok := take(); ok && v != "" {
				e.groupAdd = append(e.groupAdd, v)
			}
		case "ulimit":
			if v, ok := take(); ok {
				if u, ok := parseUlimit(v); ok {
					e.ulimits = append(e.ulimits, u)
				}
			}
		case "hostname", "h":
			if v, ok := take(); ok {
				e.hostname = v
			}
		case "user", "u":
			if v, ok := take(); ok {
				e.user = v
			}
		case "cgroupns":
			if v, ok := take(); ok {
				e.cgroupns = v
			}
		case "entrypoint":
			if v, ok := take(); ok {
				e.entrypoint = v
			}
		case "pid":
			if v, ok := take(); ok {
				e.pidMode = v
			}
		case "ipc":
			if v, ok := take(); ok {
				e.ipcMode = v
			}
		case "read-only":
			e.readOnly = true
		case "init":
			e.init = true
		case "no-healthcheck":
			e.noHealth = true
		default:
			item := args[flagAt]
			if kind == flagBare && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
				i++
				item += " " + args[i]
			}
			e.Unparsed = append(e.Unparsed, item)
		}
	}
	var kept []string
	for i, a := range args {
		if !consumed[i] {
			kept = append(kept, a)
		}
	}
	e.Cleaned = strings.Join(kept, " ")
	return e
}

const (
	flagNone = iota
	flagBare
	flagInline
)

// flagOf classifies one argv token. flagInline means --name=value.
func flagOf(a string) (name, inline string, kind int) {
	if strings.HasPrefix(a, "--") && len(a) > 2 {
		name = a[2:]
		if k, v, ok := strings.Cut(name, "="); ok {
			return k, v, flagInline
		}
		return name, "", flagBare
	}
	if strings.HasPrefix(a, "-") && len(a) == 2 && a[1] != '-' {
		return a[1:], "", flagBare
	}
	return "", "", flagNone
}

func normaliseRestart(v string) string {
	v, _, _ = strings.Cut(strings.ToLower(strings.TrimSpace(v)), ":")
	switch v {
	case "no", "always", "unless-stopped", "on-failure":
		return v
	default:
		return ""
	}
}

func splitArgs(s string) []string {
	var args []string
	var b strings.Builder
	var quote byte
	for i := 0; i < len(s); i++ {
		c := s[i]
		if quote != 0 {
			if c == quote {
				quote = 0
				continue
			}
			b.WriteByte(c)
			continue
		}
		if c == '"' || c == '\'' {
			quote = c
			continue
		}
		if c == ' ' || c == '\t' || c == '\n' || c == '\r' {
			if b.Len() > 0 {
				args = append(args, b.String())
				b.Reset()
			}
			continue
		}
		b.WriteByte(c)
	}
	if b.Len() > 0 {
		args = append(args, b.String())
	}
	return args
}

func parsePublish(s string) (Config, bool) {
	proto := "tcp"
	if i := strings.LastIndex(s, "/"); i >= 0 {
		proto = strings.ToLower(s[i+1:])
		s = s[:i]
		if proto != "tcp" && proto != "udp" {
			return Config{}, false
		}
	}
	parts := strings.Split(s, ":")
	var ip, hostStr, ctrStr string
	switch len(parts) {
	case 1:
		hostStr, ctrStr = parts[0], parts[0]
	case 2:
		hostStr, ctrStr = parts[0], parts[1]
	case 3:
		ip, hostStr, ctrStr = parts[0], parts[1], parts[2]
	default:
		return Config{}, false
	}
	host, err1 := strconv.Atoi(hostStr)
	ctr, err2 := strconv.Atoi(ctrStr)
	if err1 != nil || err2 != nil || host < 1 || host > 65535 || ctr < 1 || ctr > 65535 {
		return Config{}, false
	}
	return Config{
		Type: TypePort, Name: "Extra port", HostPort: host, ContainerPort: ctr,
		Proto: proto, HostIP: ip, Description: "From the template's extra parameters.",
	}, true
}

func parseVolume(s string) (Config, bool) {
	parts := strings.Split(s, ":")
	if len(parts) < 2 || len(parts) > 3 {
		return Config{}, false
	}
	mode := "rw"
	if len(parts) == 3 && parts[2] != "" {
		mode = parts[2]
	}
	if parts[0] == "" || !strings.HasPrefix(parts[1], "/") {
		return Config{}, false
	}
	return Config{
		Type: TypePath, Name: "Extra path", Host: parts[0], Container: parts[1], Mode: mode,
		Description: "From the template's extra parameters.",
	}, true
}

func parseEnvFlag(s string) (Config, bool) {
	k, v, _ := strings.Cut(s, "=")
	k = strings.TrimSpace(k)
	if k == "" || strings.ContainsAny(k, " \t=") {
		return Config{}, false
	}
	return Config{
		Type: TypeVariable, Name: k, Key: k, Value: v,
		Description: "From the template's extra parameters.",
	}, true
}

func parseDeviceFlag(s string) (Config, bool) {
	parts := strings.Split(s, ":")
	if len(parts) < 1 || len(parts) > 3 {
		return Config{}, false
	}
	host := pathCleanDev(parts[0])
	ctr := host
	mode := "rwm"
	if len(parts) >= 2 && parts[1] != "" {
		ctr = pathCleanDev(parts[1])
	}
	if len(parts) == 3 && parts[2] != "" {
		mode = parts[2]
	}
	if host == "/dev" || !strings.HasPrefix(host, "/dev/") || !strings.HasPrefix(ctr, "/") {
		return Config{}, false
	}
	return Config{
		Type: TypeDevice, Name: "Extra device", Host: host, Container: ctr, Mode: mode,
		Description: "From the template's extra parameters.",
	}, true
}

func pathCleanDev(p string) string {
	for len(p) > 1 && strings.HasSuffix(p, "/") {
		p = p[:len(p)-1]
	}
	return p
}

func parseUlimit(s string) (ulimitSpec, bool) {
	name, rest, ok := strings.Cut(s, "=")
	if !ok || name == "" {
		return ulimitSpec{}, false
	}
	a, b, two := strings.Cut(rest, ":")
	soft, err := strconv.ParseInt(a, 10, 64)
	if err != nil {
		return ulimitSpec{}, false
	}
	hard := soft
	if two {
		hard, err = strconv.ParseInt(b, 10, 64)
		if err != nil {
			return ulimitSpec{}, false
		}
	}
	return ulimitSpec{name: name, soft: soft, hard: hard}, true
}

// parseBytes reads a docker size such as 512m, 1g or 1gb.
func parseBytes(s string) (int64, bool) {
	s = strings.TrimSpace(strings.ToLower(s))
	i := 0
	for i < len(s) && (s[i] == '.' || (s[i] >= '0' && s[i] <= '9')) {
		i++
	}
	if i == 0 {
		return 0, false
	}
	n, err := strconv.ParseFloat(s[:i], 64)
	if err != nil || n < 0 {
		return 0, false
	}
	unit := strings.TrimSuffix(strings.TrimSuffix(s[i:], "ib"), "b")
	var mult float64 = 1
	switch unit {
	case "":
	case "k":
		mult = 1 << 10
	case "m":
		mult = 1 << 20
	case "g":
		mult = 1 << 30
	default:
		return 0, false
	}
	return int64(n * mult), true
}

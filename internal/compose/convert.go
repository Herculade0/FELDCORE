package compose

import (
	"fmt"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Herculade0/feldcore/internal/docker"
)

// FromSpec renders a single container as a compose file. Settings that have
// no compose field are returned in notes so they are not dropped silently.
func FromSpec(spec docker.Spec) (string, []string, error) {
	name := strings.TrimSpace(spec.Name)
	if name == "" {
		return "", nil, inputf("This container has no name, so it cannot become a stack.")
	}
	if spec.VPN != "" {
		return "", nil, inputf("This container is routed through a VPN. Compose conversion does not keep that route. Turn routing off first, or write the compose file yourself.")
	}
	if !svcNameRe.MatchString(name) {
		return "", nil, inputf("This container's name cannot be a compose service name.")
	}
	svc := svcOut{
		Image:         spec.Image,
		ContainerName: name,
	}
	switch spec.RestartPolicy {
	case "", "no":
	default:
		svc.Restart = spec.RestartPolicy
	}
	if spec.Privileged {
		svc.Privileged = true
	}
	var notes []string
	switch spec.Network {
	case "", "bridge", "default":
		if spec.Isolate == docker.IsolatePrivate {
			notes = append(notes, "The container's private network is replaced by this stack's network when you deploy. The other services in the stack can reach it. Other stacks cannot.")
		}
	case "host", "none":
		svc.NetworkMode = spec.Network
		notes = append(notes, "The container's "+spec.Network+" network is kept. Deploy will ask you to confirm it if it is high-risk.")
	default:
		if strings.HasPrefix(spec.Network, "container:") || strings.HasPrefix(spec.Network, "service:") {
			svc.NetworkMode = spec.Network
			notes = append(notes, "The container shares another container's network. That line was kept.")
		}
	}
	for _, c := range spec.Configs {
		switch c.Type {
		case docker.TypePort:
			if c.HostPort <= 0 {
				continue
			}
			proto := c.Proto
			if proto == "" {
				proto = "tcp"
			}
			if c.HostIP != "" {
				svc.Ports = append(svc.Ports, fmt.Sprintf("%s:%d:%d/%s", c.HostIP, c.HostPort, c.ContainerPort, proto))
			} else {
				svc.Ports = append(svc.Ports, fmt.Sprintf("%d:%d/%s", c.HostPort, c.ContainerPort, proto))
			}
		case docker.TypePath:
			mode := c.Mode
			if mode == "" {
				mode = "rw"
			}
			svc.Volumes = append(svc.Volumes, c.Host+":"+c.Container+":"+mode)
		case docker.TypeDevice:
			mode := c.Mode
			if mode == "" {
				mode = "rwm"
			}
			target := c.Container
			if target == "" {
				target = c.Host
			}
			svc.Devices = append(svc.Devices, c.Host+":"+target+":"+mode)
		case docker.TypeVariable:
			if svc.Environment == nil {
				svc.Environment = map[string]string{}
			}
			svc.Environment[c.Key] = c.Value
		case docker.TypeLabel:
			if strings.HasPrefix(c.Key, "feldcore.") {
				continue
			}
			if svc.Labels == nil {
				svc.Labels = map[string]string{}
			}
			svc.Labels[c.Key] = c.Value
		}
	}
	if spec.GPU {
		have := false
		for _, d := range svc.Devices {
			if strings.HasPrefix(d, "/dev/dri:") || d == "/dev/dri" {
				have = true
			}
		}
		if !have {
			svc.Devices = append(svc.Devices, "/dev/dri:/dev/dri:rwm")
		}
	}
	extraNotes := applyExtra(&svc, spec.ExtraParams)
	notes = append(notes, extraNotes...)
	if spec.Icon != "" {
		if svc.Labels == nil {
			svc.Labels = map[string]string{}
		}
		if svc.Labels["net.unraid.docker.icon"] == "" {
			svc.Labels["net.unraid.docker.icon"] = spec.Icon
		}
	}
	doc := map[string]any{"services": map[string]svcOut{name: svc}}
	raw, err := yaml.Marshal(doc)
	if err != nil {
		return "", nil, opf("Could not write the compose file.")
	}
	return string(raw), notes, nil
}

type svcOut struct {
	Image         string            `yaml:"image"`
	ContainerName string            `yaml:"container_name,omitempty"`
	Restart       string            `yaml:"restart,omitempty"`
	Privileged    bool              `yaml:"privileged,omitempty"`
	NetworkMode   string            `yaml:"network_mode,omitempty"`
	Pid           string            `yaml:"pid,omitempty"`
	Ipc           string            `yaml:"ipc,omitempty"`
	User          string            `yaml:"user,omitempty"`
	Hostname      string            `yaml:"hostname,omitempty"`
	ReadOnly      bool              `yaml:"read_only,omitempty"`
	Init          bool              `yaml:"init,omitempty"`
	ShmSize       string            `yaml:"shm_size,omitempty"`
	CapAdd        []string          `yaml:"cap_add,omitempty"`
	CapDrop       []string          `yaml:"cap_drop,omitempty"`
	SecurityOpt   []string          `yaml:"security_opt,omitempty"`
	ExtraHosts    []string          `yaml:"extra_hosts,omitempty"`
	GroupAdd      []string          `yaml:"group_add,omitempty"`
	DNS           []string          `yaml:"dns,omitempty"`
	Ports         []string          `yaml:"ports,omitempty"`
	Volumes       []string          `yaml:"volumes,omitempty"`
	Devices       []string          `yaml:"devices,omitempty"`
	Environment   map[string]string `yaml:"environment,omitempty"`
	Labels        map[string]string `yaml:"labels,omitempty"`
}

// applyExtra copies the flags FELDCORE stored on the container into compose
// fields. Anything left over is reported, not dropped without a word.
func applyExtra(svc *svcOut, extra string) []string {
	args := splitArgs(extra)
	var rest []string
	for i := 0; i < len(args); i++ {
		name, inline, kind := flagOf(args[i])
		if kind == 0 {
			rest = append(rest, args[i])
			continue
		}
		val := inline
		consumed := kind == 2
		if kind == 1 && i+1 < len(args) && !strings.HasPrefix(args[i+1], "-") {
			val = args[i+1]
			i++
			consumed = true
		}
		switch name {
		case "cap-add":
			svc.CapAdd = append(svc.CapAdd, splitComma(val)...)
		case "cap-drop":
			svc.CapDrop = append(svc.CapDrop, splitComma(val)...)
		case "pid":
			svc.Pid = val
		case "ipc":
			svc.Ipc = val
		case "shm-size":
			svc.ShmSize = val
		case "user":
			svc.User = val
		case "hostname":
			svc.Hostname = val
		case "security-opt":
			svc.SecurityOpt = append(svc.SecurityOpt, val)
		case "add-host":
			svc.ExtraHosts = append(svc.ExtraHosts, val)
		case "group-add":
			svc.GroupAdd = append(svc.GroupAdd, val)
		case "dns":
			svc.DNS = append(svc.DNS, val)
		case "read-only":
			svc.ReadOnly = true
		case "init":
			svc.Init = true
		case "privileged", "restart", "network", "net":
			// Already represented by the spec fields.
		default:
			if kind == 2 {
				rest = append(rest, "--"+name+"="+inline)
			} else if consumed {
				rest = append(rest, "--"+name, val)
			} else {
				rest = append(rest, "--"+name)
			}
		}
	}
	if len(svc.CapAdd) > 1 {
		sort.Strings(svc.CapAdd)
	}
	if len(rest) == 0 {
		return nil
	}
	return []string{"These extra parameters were not copied into the compose file: " + strings.Join(rest, " ")}
}

func splitComma(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
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

// flagOf reports a --name flag. kind 2 means --name=value, kind 1 means the
// value is the next argument, kind 0 means this is not a long flag.
func flagOf(arg string) (name, inline string, kind int) {
	if !strings.HasPrefix(arg, "--") || arg == "--" {
		return "", "", 0
	}
	body := strings.TrimPrefix(arg, "--")
	if n, v, ok := strings.Cut(body, "="); ok {
		return n, v, 2
	}
	return body, "", 1
}

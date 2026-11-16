package docker

import (
	"context"
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"os/exec"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Herculade0/feldcore/internal/store"
)

// ManagedLabel marks containers FELDCORE created, so the UI can tell them from
// ones started by hand or by compose.
const ManagedLabel = "feldcore.managed"

// InputError is a validation failure whose message is safe to show the user.
type InputError struct{ Msg string }

func (e *InputError) Error() string { return e.Msg }

func invalid(format string, a ...any) error {
	return &InputError{Msg: fmt.Sprintf(format, a...)}
}

// MetaStore keeps the names and descriptions of container settings, which
// Docker itself has nowhere to store.
type MetaStore interface {
	ContainerMeta(name string) map[string]store.ConfigMeta
	SetContainerMeta(name string, meta map[string]store.ConfigMeta) error
	DeleteContainerMeta(name string) error
}

// Manager is the high-level Docker surface used by the API layer.
type Manager struct {
	c    *Client
	meta MetaStore // may be nil
	log  *slog.Logger

	// editMu makes container replacement one-at-a-time.
	editMu sync.Mutex

	// statusCache avoids hammering the socket (and apt/dpkg checks) on every
	// poll; status rarely changes second to second.
	mu       sync.Mutex
	cached   Status
	cachedAt time.Time
	cacheFor time.Duration
}

// NewManager returns a Manager talking to the daemon at socket.
func NewManager(socket string, meta MetaStore, log *slog.Logger) *Manager {
	return &Manager{c: NewClient(socket), meta: meta, log: log, cacheFor: 3 * time.Second}
}

func (m *Manager) metaFor(name string) map[string]store.ConfigMeta {
	if m.meta == nil {
		return nil
	}
	return m.meta.ContainerMeta(name)
}

// Options returns the choices the container dialog offers.
func (m *Manager) Options(ctx context.Context) Options {
	gpus := DetectGPUs()
	opts := Options{
		Networks:     []string{"bridge", "host", "none"},
		GPUs:         gpus,
		GPUAvailable: gpuAvailable(gpus),
	}
	var nets []struct {
		Name string `json:"Name"`
	}
	if err := m.c.getJSON(ctx, "/networks", nil, &nets); err == nil {
		var custom []string
		for _, n := range nets {
			if isCustomNetwork(n.Name) {
				custom = append(custom, n.Name)
			}
		}
		sort.Strings(custom)
		opts.Networks = append(opts.Networks, custom...)
	}
	return opts
}

// Status reports whether Docker is installed, running and reachable.
func (m *Manager) Status(ctx context.Context) Status {
	m.mu.Lock()
	if time.Since(m.cachedAt) < m.cacheFor && m.cachedAt != (time.Time{}) {
		s := m.cached
		m.mu.Unlock()
		return s
	}
	m.mu.Unlock()

	s := m.computeStatus(ctx)

	m.mu.Lock()
	m.cached, m.cachedAt = s, time.Now()
	m.mu.Unlock()
	return s
}

func (m *Manager) computeStatus(ctx context.Context) Status {
	_, installed := lookBin("dockerd")
	_, hasApt := lookBin("apt-get")
	s := Status{
		Installed:  installed,
		CanInstall: !installed && os.Geteuid() == 0 && hasApt,
	}

	var v struct {
		Version    string
		ApiVersion string
	}
	if err := m.c.getJSON(ctx, "/version", nil, &v); err != nil {
		if !installed {
			s.Message = "Docker is not installed."
		} else {
			s.Message = capitalize(err.Error()) + "."
		}
		return s
	}
	s.Installed = true // reachable implies installed, even if dockerd wasn't on PATH
	s.Running = true
	s.Version = v.Version
	s.APIVersion = v.ApiVersion
	return s
}

// Containers lists all containers, newest first, with live stats for the
// running ones.
func (m *Manager) Containers(ctx context.Context) ([]Container, error) {
	var raw []apiContainer
	if err := m.c.getJSON(ctx, "/containers/json", url.Values{"all": {"1"}}, &raw); err != nil {
		return nil, err
	}

	out := make([]Container, len(raw))
	host := hostIP()
	for i, rc := range raw {
		out[i] = toContainer(rc, host)
	}

	// Fill in the per-container details concurrently; a slow or failing
	// lookup must not sink the whole list.
	//   running: live CPU and memory.
	//   stopped: the ports it is set up to publish. Docker only reports ports
	//            for running containers, but a stopped one still lays claim
	//            to its ports, which matters when choosing one for another.
	if len(out) > 0 {
		detailCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		for i := range out {
			wg.Add(1)
			go func(c *Container) {
				defer wg.Done()
				if c.State == "running" {
					if st, err := m.stat(detailCtx, c.ID); err == nil {
						c.CPUPct, c.MemUsed, c.MemLimit = st.cpu, st.mem, st.memLimit
					}
					return
				}
				if in, err := m.inspect(detailCtx, c.ID); err == nil {
					c.Ports = configuredPorts(in)
				}
			}(&out[i])
		}
		wg.Wait()
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out, nil
}

// configuredPorts returns the host ports a container is set up to publish,
// whether or not it is running.
func configuredPorts(in *inspectContainer) []Port {
	ports := []Port{}
	for key, v := range mapOf(in.HostConfig["PortBindings"]) {
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
			hport, _ := strconv.Atoi(strOf(mapOf(b)["HostPort"]))
			if hport > 0 {
				ports = append(ports, Port{Host: hport, Container: cport, Proto: proto})
			}
		}
	}
	sort.Slice(ports, func(i, j int) bool { return ports[i].Host < ports[j].Host })
	return ports
}

// toContainer converts a list entry for the UI. hostAddr is this server's
// address, shown for containers that answer on it (bridge and host modes).
func toContainer(rc apiContainer, hostAddr string) Container {
	c := Container{
		ID:      shortID(rc.ID),
		Name:    cleanName(rc.Names),
		Image:   rc.Image,
		State:   rc.State,
		Status:  rc.Status,
		Created: rc.Created,
		Managed: rc.Labels[ManagedLabel] == "true",
		Compose: rc.Labels[composeLabel] != "",
		GPU:     rc.Labels[GPULabel] == "true",
		IPs:     []string{},
	}

	// Where the container can be reached. On the default bridge and in host
	// mode that is the server's own address; on a user-defined network the
	// container has an address of its own.
	mode := rc.HostConfig.NetworkMode
	if mode == "" || mode == "default" {
		mode = "bridge"
	}
	var names []string
	for name := range rc.NetworkSettings.Networks {
		names = append(names, name)
	}
	sort.Strings(names)
	switch {
	case mode == "host" || mode == "bridge":
		c.Network = mode
		if hostAddr != "" {
			c.IPs = []string{hostAddr}
		}
	case mode == "none" || strings.HasPrefix(mode, "container:"):
		c.Network = mode
	default:
		c.Network = strings.Join(names, ", ")
		if c.Network == "" {
			c.Network = mode
		}
		for _, name := range names {
			if ip := rc.NetworkSettings.Networks[name].IPAddress; ip != "" {
				c.IPs = append(c.IPs, ip)
			}
		}
	}

	seen := map[string]bool{}
	for _, p := range rc.Ports {
		if p.PublicPort == 0 {
			continue
		}
		key := fmt.Sprintf("%d/%s", p.PublicPort, p.Type)
		if seen[key] {
			continue
		}
		seen[key] = true
		c.Ports = append(c.Ports, Port{Host: p.PublicPort, Container: p.PrivatePort, Proto: p.Type})
	}
	sort.Slice(c.Ports, func(i, j int) bool { return c.Ports[i].Host < c.Ports[j].Host })
	if web := resolveWebUI(rc.Labels[webUILabel], c.Ports); web != "" {
		c.WebURL = web
	} else {
		c.WebURL = guessWebURL(c.Ports)
	}
	if c.Ports == nil {
		// A nil slice marshals to JSON null; the UI expects an array. A
		// stopped container has no published ports, so this is the common path.
		c.Ports = []Port{}
	}
	return c
}

// Start, Stop, Restart and Remove act on a container by id or name.
func (m *Manager) Start(ctx context.Context, id string) error {
	return m.c.postJSON(ctx, "/containers/"+esc(id)+"/start", nil, nil, nil)
}

func (m *Manager) Stop(ctx context.Context, id string) error {
	// Give the container 10s to stop cleanly before the daemon kills it.
	return m.c.postJSON(ctx, "/containers/"+esc(id)+"/stop", url.Values{"t": {"10"}}, nil, nil)
}

func (m *Manager) Restart(ctx context.Context, id string) error {
	return m.c.postJSON(ctx, "/containers/"+esc(id)+"/restart", url.Values{"t": {"10"}}, nil, nil)
}

// Remove deletes a container. It must be stopped unless force is set.
func (m *Manager) Remove(ctx context.Context, id string, force bool) error {
	// Look the name up first so its stored setting names can be forgotten.
	name := ""
	if in, err := m.inspect(ctx, id); err == nil {
		name = strings.TrimPrefix(in.Name, "/")
	}
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	if err := m.c.do2(ctx, "DELETE", "/containers/"+esc(id), q); err != nil {
		return err
	}
	if name != "" && m.meta != nil {
		_ = m.meta.DeleteContainerMeta(name)
	}
	return nil
}

// hostIP returns this server's primary LAN address, or "" if none is found.
func hostIP() string {
	ifaces, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifc := range ifaces {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 {
			continue
		}
		skip := false
		for _, p := range []string{"docker", "veth", "br-", "virbr"} {
			if strings.HasPrefix(ifc.Name, p) {
				skip = true
			}
		}
		if skip {
			continue
		}
		addrs, err := ifc.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if ipnet, ok := a.(*net.IPNet); ok {
				if ip4 := ipnet.IP.To4(); ip4 != nil && ip4.IsPrivate() {
					return ip4.String()
				}
			}
		}
	}
	return ""
}

// Images lists local images that have a tag, largest first.
func (m *Manager) Images(ctx context.Context) ([]Image, error) {
	var raw []apiImage
	if err := m.c.getJSON(ctx, "/images/json", nil, &raw); err != nil {
		return nil, err
	}
	out := []Image{}
	for _, ri := range raw {
		for _, tag := range ri.RepoTags {
			if tag == "" || tag == "<none>:<none>" {
				continue
			}
			out = append(out, Image{
				ID:      shortID(strings.TrimPrefix(ri.ID, "sha256:")),
				Tag:     tag,
				Size:    ri.Size,
				Created: ri.Created,
				InUse:   ri.Containers > 0,
			})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Tag < out[j].Tag })
	return out, nil
}

// do2 is a DELETE/POST with no body and no result.
func (c *Client) do2(ctx context.Context, method, path string, query url.Values) error {
	resp, err := c.do(ctx, method, path, query, nil, "")
	if err != nil {
		return err
	}
	drain(resp)
	return nil
}

func lookBin(name string) (string, bool) {
	if p, err := exec.LookPath(name); err == nil {
		return p, true
	}
	for _, dir := range []string{"/usr/bin", "/usr/sbin", "/bin", "/sbin", "/usr/local/bin"} {
		p := dir + "/" + name
		if st, err := os.Stat(p); err == nil && !st.IsDir() {
			return p, true
		}
	}
	return "", false
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func cleanName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

func esc(s string) string { return url.PathEscape(s) }

func capitalize(s string) string {
	if s == "" {
		return s
	}
	return strings.ToUpper(s[:1]) + s[1:]
}

// webUIPort matches Unraid's [PORT:32400] placeholder.
var webUIPort = regexp.MustCompile(`\[PORT:(\d+)\]`)

// resolveWebUI turns an Unraid WebUI template into a link the UI can open.
// [IP] becomes the HOST token the front-end replaces with this server's name,
// and [PORT:n] becomes the published host port for container port n.
// Anything still unresolved is dropped, so a half-filled template is not
// offered as a link.
func resolveWebUI(tmpl string, ports []Port) string {
	tmpl = strings.TrimSpace(tmpl)
	if tmpl == "" {
		return ""
	}
	out := strings.ReplaceAll(tmpl, "[IP]", "HOST")
	out = webUIPort.ReplaceAllStringFunc(out, func(m string) string {
		sub := webUIPort.FindStringSubmatch(m)
		if len(sub) != 2 {
			return m
		}
		cport, _ := strconv.Atoi(sub[1])
		for _, p := range ports {
			if p.Container == cport && p.Proto == "tcp" && p.Host > 0 {
				return strconv.Itoa(p.Host)
			}
		}
		if cport > 0 {
			return strconv.Itoa(cport)
		}
		return m
	})
	if strings.ContainsAny(out, "[]") {
		return ""
	}
	if !strings.HasPrefix(out, "http://") && !strings.HasPrefix(out, "https://") {
		return ""
	}
	return out
}

// guessWebURL offers a browser link for a published HTTP-ish port. It is a
// convenience guess, not a guarantee the service speaks HTTP.
func guessWebURL(ports []Port) string {
	for _, p := range ports {
		if p.Proto != "tcp" {
			continue
		}
		switch p.Host {
		case 443:
			return "https://HOST"
		case 80:
			return "http://HOST"
		}
	}
	for _, p := range ports {
		if p.Proto == "tcp" {
			return fmt.Sprintf("http://HOST:%d", p.Host)
		}
	}
	return ""
}

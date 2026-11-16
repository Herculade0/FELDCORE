package docker

import (
	"context"
	"fmt"
	"log/slog"
	"net/url"
	"os"
	"os/exec"
	"sort"
	"strings"
	"sync"
	"time"
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

// Manager is the high-level Docker surface used by the API layer.
type Manager struct {
	c   *Client
	log *slog.Logger

	// statusCache avoids hammering the socket (and apt/dpkg checks) on every
	// poll; status rarely changes second to second.
	mu       sync.Mutex
	cached   Status
	cachedAt time.Time
	cacheFor time.Duration
}

// NewManager returns a Manager talking to the daemon at socket.
func NewManager(socket string, log *slog.Logger) *Manager {
	return &Manager{c: NewClient(socket), log: log, cacheFor: 3 * time.Second}
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
	running := 0
	for i, rc := range raw {
		out[i] = toContainer(rc)
		if out[i].State == "running" {
			running++
		}
	}

	// Fetch stats concurrently for running containers; a slow or failing
	// stat must not sink the whole list.
	if running > 0 {
		statCtx, cancel := context.WithTimeout(ctx, 4*time.Second)
		defer cancel()
		var wg sync.WaitGroup
		for i := range out {
			if out[i].State != "running" {
				continue
			}
			wg.Add(1)
			go func(c *Container) {
				defer wg.Done()
				if st, err := m.stat(statCtx, c.ID); err == nil {
					c.CPUPct, c.MemUsed, c.MemLimit = st.cpu, st.mem, st.memLimit
				}
			}(&out[i])
		}
		wg.Wait()
	}

	sort.SliceStable(out, func(i, j int) bool { return out[i].Created > out[j].Created })
	return out, nil
}

func toContainer(rc apiContainer) Container {
	c := Container{
		ID:      shortID(rc.ID),
		Name:    cleanName(rc.Names),
		Image:   rc.Image,
		State:   rc.State,
		Status:  rc.Status,
		Created: rc.Created,
		Managed: rc.Labels[ManagedLabel] == "true",
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
	c.WebURL = guessWebURL(c.Ports)
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
	q := url.Values{}
	if force {
		q.Set("force", "1")
	}
	return m.c.do2(ctx, "DELETE", "/containers/"+esc(id), q)
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

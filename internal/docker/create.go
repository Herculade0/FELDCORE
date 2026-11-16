package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"net/url"
	"path"
	"regexp"
	"sort"
	"strings"
)

var (
	nameRe  = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9_.-]{0,62}$`)
	imageRe = regexp.MustCompile(`^[a-zA-Z0-9][a-zA-Z0-9._/:@-]{0,254}$`)
)

// Create pulls the image if needed, creates the container, and (when
// requested) starts it. It returns the new container's short id.
func (m *Manager) Create(ctx context.Context, req CreateRequest) (string, error) {
	name := strings.TrimSpace(req.Name)
	if name != "" && !nameRe.MatchString(name) {
		return "", invalid("Container names use letters, numbers and _ . - and start with a letter or number.")
	}
	image := strings.TrimSpace(req.Image)
	if !imageRe.MatchString(image) {
		return "", invalid("Enter an image name, for example %q.", "nginx:latest")
	}
	body, err := buildCreate(req, image)
	if err != nil {
		return "", err
	}

	// Pull first so a missing image is reported clearly rather than as a
	// cryptic create failure.
	if err := m.Pull(ctx, image); err != nil {
		return "", err
	}

	q := url.Values{}
	if name != "" {
		q.Set("name", name)
	}
	var created createdResponse
	if err := m.c.postJSON(ctx, "/containers/create", q, body, &created); err != nil {
		return "", err
	}
	for _, w := range created.Warnings {
		m.log.Warn("docker create warning", "name", name, "warning", w)
	}
	m.log.Info("container created", "name", name, "image", image, "id", shortID(created.ID))

	if req.Autostart {
		if err := m.Start(ctx, created.ID); err != nil {
			return shortID(created.ID), fmt.Errorf("the container was created but would not start: %w", err)
		}
	}
	return shortID(created.ID), nil
}

// buildCreate turns the UI request into the Engine API create body.
func buildCreate(req CreateRequest, image string) (*createBody, error) {
	b := &createBody{
		Image:  image,
		Labels: map[string]string{ManagedLabel: "true"},
	}
	for k, v := range req.Labels {
		if k != ManagedLabel {
			b.Labels[k] = v
		}
	}

	for _, e := range req.Env {
		key := strings.TrimSpace(e.Key)
		if key == "" {
			continue
		}
		if strings.ContainsAny(key, "= \t\n") {
			return nil, invalid("%q is not a valid environment variable name.", key)
		}
		b.Env = append(b.Env, key+"="+e.Value)
	}

	ports := map[string]struct{}{}
	bindings := map[string][]portBinding{}
	for _, p := range req.Ports {
		proto := strings.ToLower(p.Proto)
		if proto != "tcp" && proto != "udp" {
			proto = "tcp"
		}
		if p.Container < 1 || p.Container > 65535 || p.Host < 1 || p.Host > 65535 {
			return nil, invalid("Port numbers must be between 1 and 65535.")
		}
		key := fmt.Sprintf("%d/%s", p.Container, proto)
		ports[key] = struct{}{}
		bindings[key] = append(bindings[key], portBinding{HostIP: "", HostPort: fmt.Sprintf("%d", p.Host)})
	}
	if len(ports) > 0 {
		b.ExposedPorts = ports
		b.HostConfig.PortBindings = bindings
	}

	for _, v := range req.Volumes {
		host := strings.TrimSpace(v.Host)
		ctr := strings.TrimSpace(v.Container)
		if host == "" && ctr == "" {
			continue
		}
		if !path.IsAbs(host) {
			return nil, invalid("Host path %q must be absolute (start with /).", host)
		}
		if !path.IsAbs(ctr) {
			return nil, invalid("Container path %q must be absolute (start with /).", ctr)
		}
		if strings.ContainsAny(host+ctr, ":") {
			return nil, invalid("Paths cannot contain a colon.")
		}
		bind := host + ":" + ctr
		if v.ReadOnly {
			bind += ":ro"
		}
		b.HostConfig.Binds = append(b.HostConfig.Binds, bind)
	}

	policy := req.RestartPolicy
	switch policy {
	case "", "no":
		policy = "no"
	case "always", "unless-stopped", "on-failure":
	default:
		return nil, invalid("Unknown restart policy %q.", policy)
	}
	// "no" must be sent as an empty name, not the string "no".
	if policy != "no" {
		b.HostConfig.RestartPolicy = restartPolicy{Name: policy}
	}
	return b, nil
}

// Pull downloads an image, blocking until done. Progress lines are drained;
// an error line in the stream is surfaced.
func (m *Manager) Pull(ctx context.Context, image string) error {
	repo, tag := splitRef(image)
	q := url.Values{"fromImage": {repo}, "tag": {tag}}
	resp, err := m.c.do(ctx, "POST", "/images/create", q, nil, "")
	if err != nil {
		return pullError(err, image)
	}
	defer drain(resp)

	sc := scanLines(resp.Body)
	for sc.Scan() {
		var line struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Error != "" {
			return invalid("Could not pull %q: %s", image, line.Error)
		}
	}
	return sc.Err()
}

func pullError(err error, image string) error {
	if NotFound(err) {
		return invalid("Image %q was not found.", image)
	}
	return err
}

// splitRef separates an image reference into a repository and a tag, defaulting
// the tag to "latest". A digest (name@sha256:...) is kept whole as the tag.
func splitRef(ref string) (repo, tag string) {
	if at := strings.IndexByte(ref, '@'); at >= 0 {
		return ref[:at], ref[at+1:]
	}
	// A colon is only a tag separator if it is after the last slash (otherwise
	// it is a registry port, e.g. registry:5000/img).
	slash := strings.LastIndexByte(ref, '/')
	colon := strings.LastIndexByte(ref, ':')
	if colon > slash {
		return ref[:colon], ref[colon+1:]
	}
	return ref, "latest"
}

// sortedEnv is a small helper used in tests to compare env slices stably.
func sortedEnv(env []string) []string {
	out := append([]string(nil), env...)
	sort.Strings(out)
	return out
}

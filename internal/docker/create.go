package docker

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"strings"
)

type pullKey struct{}

// SkipPull tells Create and Update not to contact a registry when the image
// is already present. Snapshot restore uses it: the pinned tag exists only
// on this machine.
func SkipPull(ctx context.Context) context.Context {
	return context.WithValue(ctx, pullKey{}, true)
}

func skipPull(ctx context.Context) bool {
	v, _ := ctx.Value(pullKey{}).(bool)
	return v
}

// Create pulls the image if needed, creates the container, and (when
// requested) starts it. It returns the new container's short id.
func (m *Manager) Create(ctx context.Context, s Spec) (string, error) {
	if err := normaliseSpec(&s); err != nil {
		return "", err
	}
	grant, err := m.grantFor(s)
	if err != nil {
		return "", err
	}

	// Pull first so a missing image is reported clearly rather than as a
	// cryptic create failure. A restore of a snapshot image skips the pull
	// when that image is already on this machine: the tag is local-only.
	if !(skipPull(ctx) && m.imagePresent(ctx, s.Image)) {
		if err := m.Pull(ctx, s.Image); err != nil {
			return "", err
		}
	}

	cfg, host := map[string]any{}, map[string]any{}
	applySpec(cfg, host, s, grant)
	body := cfg
	body["HostConfig"] = host

	id, err := m.createContainer(ctx, s.Name, body)
	if err != nil {
		return "", err
	}
	m.log.Info("container created", "name", s.Name, "image", s.Image, "id", shortID(id))
	m.saveMeta(ctx, id, s)

	if s.Autostart {
		if err := m.Start(ctx, id); err != nil {
			return shortID(id), &OpError{fmt.Sprintf("The container was created but would not start: %v", err)}
		}
	}
	return shortID(id), nil
}

// createContainer issues the create call and returns the full container id.
func (m *Manager) createContainer(ctx context.Context, name string, body map[string]any) (string, error) {
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
	return created.ID, nil
}

// saveMeta stores the spec's setting names and descriptions under the
// container's actual name (Docker picks one when none was given).
func (m *Manager) saveMeta(ctx context.Context, id string, s Spec) {
	name := s.Name
	if name == "" {
		if in, err := m.inspect(ctx, id); err == nil {
			name = strings.TrimPrefix(in.Name, "/")
		}
	}
	if name == "" || m.meta == nil {
		return
	}
	if err := m.meta.SetContainerMeta(name, metaOf(s)); err != nil {
		m.log.Warn("could not save container setting names", "name", name, "err", err)
	}
}

// inspect fetches a container's full configuration, keeping numbers exact.
func (m *Manager) inspect(ctx context.Context, id string) (*inspectContainer, error) {
	raw, err := m.c.getRaw(ctx, "/containers/"+esc(id)+"/json")
	if err != nil {
		return nil, err
	}
	var in inspectContainer
	if err := decodeRaw(raw, &in); err != nil {
		return nil, err
	}
	return &in, nil
}

// imageConfig returns an image's default Config, or an empty map when the
// image is no longer present locally.
func (m *Manager) imageConfig(ctx context.Context, ref string) map[string]any {
	raw, err := m.c.getRaw(ctx, "/images/"+esc(ref)+"/json")
	if err != nil {
		return map[string]any{}
	}
	var img struct {
		Config map[string]any `json:"Config"`
	}
	if decodeRaw(raw, &img) != nil || img.Config == nil {
		return map[string]any{}
	}
	return img.Config
}

// imagePresent reports whether an image reference exists locally.
func (m *Manager) imagePresent(ctx context.Context, ref string) bool {
	_, err := m.c.getRaw(ctx, "/images/"+esc(ref)+"/json")
	return err == nil
}

// getRaw performs a GET and returns the response body.
func (c *Client) getRaw(ctx context.Context, path string) ([]byte, error) {
	resp, err := c.do(ctx, "GET", path, nil, nil, "")
	if err != nil {
		return nil, err
	}
	defer drain(resp)
	return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
}

// Pull downloads an image, blocking until done. Progress lines are drained;
// an error line in the stream is surfaced.
func (m *Manager) Pull(ctx context.Context, image string) error {
	repo, tag := splitRef(image)
	q := url.Values{"fromImage": {repo}, "tag": {tag}}
	resp, err := m.c.do(ctx, "POST", "/images/create", q, nil, "")
	if err != nil {
		if NotFound(err) {
			return invalid("Image %q was not found.", image)
		}
		return err
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

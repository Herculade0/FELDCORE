package docker

import (
	"context"
	"net/url"
	"path"
	"strings"
)

// SnapBind is one folder a snapshot should include.
type SnapBind struct {
	Host      string
	Container string
	Named     bool // a Docker volume name, not a folder on disk
}

// SnapSource is everything a snapshot needs from a container.
type SnapSource struct {
	ID      string
	Name    string
	Image   string
	ImageID string
	Network string
	Role    string
	Running bool
	Spec    Spec
	Binds   []SnapBind
}

// SnapshotSource reads a container's config, image and mapped folders.
func (m *Manager) SnapshotSource(ctx context.Context, id string) (SnapSource, error) {
	info, err := m.Spec(ctx, id)
	if err != nil {
		return SnapSource{}, err
	}
	in, err := m.inspect(ctx, id)
	if err != nil {
		return SnapSource{}, err
	}
	name := strings.TrimPrefix(in.Name, "/")
	mode := strOf(in.HostConfig["NetworkMode"])
	if mode == "" || mode == "default" {
		mode = "bridge"
	}
	src := SnapSource{
		ID:      in.ID,
		Name:    name,
		Image:   info.Spec.Image,
		ImageID: in.Image,
		Network: mode,
		Role:    strMapOf(in.Config["Labels"])[RoleLabel],
		Running: in.State.Running,
		Spec:    info.Spec,
		Binds:   bindsFrom(in),
	}
	if src.Image == "" {
		src.Image = strOf(in.Config["Image"])
	}
	return src, nil
}

func bindsFrom(in *inspectContainer) []SnapBind {
	if len(in.Mounts) > 0 {
		var out []SnapBind
		for _, m := range in.Mounts {
			b := SnapBind{Host: m.Source, Container: m.Destination}
			if m.Type == "volume" || (m.Source != "" && !path.IsAbs(m.Source)) {
				b.Named = true
				b.Host = m.Name
				if b.Host == "" {
					b.Host = m.Source
				}
			}
			if b.Container == "" {
				continue
			}
			out = append(out, b)
		}
		return out
	}
	var out []SnapBind
	for _, raw := range strSliceOf(in.HostConfig["Binds"]) {
		host, rest, ok := strings.Cut(raw, ":")
		if !ok {
			continue
		}
		container, _, _ := strings.Cut(rest, ":")
		b := SnapBind{Host: host, Container: container}
		if !path.IsAbs(host) {
			b.Named = true
		}
		out = append(out, b)
	}
	return out
}

// TagImage points ref (repo:tag) at an image id so a later prune cannot
// delete the image a snapshot depends on.
func (m *Manager) TagImage(ctx context.Context, imageID, ref string) error {
	if !strings.Contains(ref, ":") {
		return invalid("A snapshot image needs a tag.")
	}
	repo, tag := splitRef(ref)
	q := url.Values{"repo": {repo}, "tag": {tag}}
	if err := m.c.do2(ctx, "POST", "/images/"+esc(imageID)+"/tag", q); err != nil {
		return &OpError{"Could not keep the snapshot image: " + err.Error()}
	}
	return nil
}

// RemoveImageRef removes a snapshot tag. An image a container is still using
// is left in place and the error is returned.
func (m *Manager) RemoveImageRef(ctx context.Context, ref string) error {
	err := m.c.do2(ctx, "DELETE", "/images/"+esc(ref), nil)
	if err == nil || NotFound(err) {
		return nil
	}
	return err
}

// ApplySpec recreates a container from a snapshot spec, or creates it when
// the name is gone. The image is not pulled when it is already present.
func (m *Manager) ApplySpec(ctx context.Context, spec Spec) (string, error) {
	ctx = SkipPull(ctx)
	if spec.Name == "" {
		return "", invalid("A snapshot needs a container name to restore.")
	}
	// A snapshot records the network the container had. An empty isolation
	// choice must not move an old bridge container onto a private network.
	if spec.Isolate == "" {
		spec.Isolate = IsolateShared
	}
	in, err := m.inspect(ctx, spec.Name)
	if NotFound(err) {
		return m.Create(ctx, spec)
	}
	if err != nil {
		return "", err
	}
	return m.Update(ctx, in.ID, spec)
}

package docker

import (
	"context"
	"fmt"
	"net/url"
	"strings"
)

// oldSuffix names the previous container while its replacement is proven.
const oldSuffix = "-feldcore-old"

// Spec returns a container's current settings in editable form.
func (m *Manager) Spec(ctx context.Context, id string) (SpecInfo, error) {
	in, err := m.inspect(ctx, id)
	if err != nil {
		return SpecInfo{}, err
	}
	name := strings.TrimPrefix(in.Name, "/")
	var meta = m.metaFor(name)
	info := SpecInfo{
		Spec:     specFromInspect(in, m.imageConfig(ctx, in.Image), meta),
		Editable: true,
		Running:  in.State.Running,
	}
	if reason := notEditable(in); reason != "" {
		info.Editable, info.Reason = false, reason
	}
	return info, nil
}

// notEditable explains why a container should not be recreated by FELDCORE.
func notEditable(in *inspectContainer) string {
	if strMapOf(in.Config["Labels"])[composeLabel] != "" {
		return "This container belongs to a Docker Compose project. Change it in its compose file, or Compose and FELDCORE will overwrite each other."
	}
	if strings.HasSuffix(in.Name, oldSuffix) {
		return "This is a leftover from an interrupted edit. Remove it once the new container works."
	}
	return ""
}

// Update applies new settings to a container the way Unraid does: by
// replacing it. Docker cannot change ports, paths or variables on an existing
// container, so the container is recreated from its full current
// configuration with the new settings on top.
//
// Unlike a plain remove-and-create, the old container is kept (renamed and
// stopped) until the new one has been created and started. If anything fails
// the new one is discarded and the old one is put back exactly as it was.
func (m *Manager) Update(ctx context.Context, id string, s Spec) (string, error) {
	m.editMu.Lock()
	defer m.editMu.Unlock()
	// Finish (or roll back) even if the browser goes away mid-way.
	ctx = context.WithoutCancel(ctx)

	in, err := m.inspect(ctx, id)
	if err != nil {
		return "", err
	}
	if reason := notEditable(in); reason != "" {
		return "", invalid("%s", reason)
	}
	oldName := strings.TrimPrefix(in.Name, "/")
	if s.Name == "" {
		s.Name = oldName
	}
	if err := normaliseSpec(&s); err != nil {
		return "", err
	}
	prevMode := strOf(in.HostConfig["NetworkMode"])
	if prevMode == "" || prevMode == "default" {
		prevMode = "bridge"
	}
	prevLinks := splitLinks(strMapOf(in.Config["Labels"])[linksLabel])
	plan, err := m.prepareNetwork(ctx, s, false, oldName, prevMode, prevLinks)
	if err != nil {
		return "", err
	}
	s = plan.Spec
	grant, err := m.grantFor(s)
	if err != nil {
		return "", err
	}

	// Fetch a new image before touching the running container. Restoring a
	// snapshot skips this when the pinned image is already present.
	if !(skipPull(ctx) && m.imagePresent(ctx, s.Image)) {
		if s.Image != strOf(in.Config["Image"]) || !m.imagePresent(ctx, s.Image) {
			if err := m.Pull(ctx, s.Image); err != nil {
				return "", err
			}
		}
	}
	body := recreateBody(in, m.imageConfig(ctx, in.Image), s, grant)

	wasRunning := in.State.Running
	parked := oldName + oldSuffix

	if err := m.rename(ctx, in.ID, parked); err != nil {
		return "", &OpError{fmt.Sprintf("Could not set the current container aside: %v", err)}
	}
	// restore puts the original container back; used on any failure below.
	restore := func() {
		if err := m.rename(ctx, in.ID, oldName); err != nil {
			m.log.Error("could not restore container name after failed edit", "name", oldName, "err", err)
			return
		}
		if wasRunning {
			if err := m.Start(ctx, in.ID); err != nil {
				m.log.Error("could not restart container after failed edit", "name", oldName, "err", err)
			}
		}
	}

	if wasRunning {
		// The old container must release its ports before the new one starts.
		if err := m.Stop(ctx, in.ID); err != nil {
			restore()
			return "", &OpError{fmt.Sprintf("Could not stop the container: %v", err)}
		}
	}

	newID, err := m.createContainer(ctx, s.Name, body)
	if err != nil {
		restore()
		return "", &OpError{fmt.Sprintf("Docker rejected the new settings, so nothing was changed: %v", err)}
	}

	if wasRunning || s.Autostart {
		if err := m.Start(ctx, newID); err != nil {
			_ = m.c.do2(ctx, "DELETE", "/containers/"+esc(newID), url.Values{"force": {"1"}})
			restore()
			return "", &OpError{fmt.Sprintf("The container would not start with the new settings, so the previous one was restored: %v", err)}
		}
	}

	// The replacement is up: the original is no longer needed.
	if err := m.c.do2(ctx, "DELETE", "/containers/"+esc(in.ID), url.Values{"force": {"1"}}); err != nil {
		m.log.Warn("could not remove the previous container", "name", parked, "err", err)
	}

	if m.meta != nil {
		if s.Name != oldName {
			_ = m.meta.DeleteContainerMeta(oldName)
		}
		if err := m.meta.SetContainerMeta(s.Name, metaOf(s)); err != nil {
			m.log.Warn("could not save container setting names", "name", s.Name, "err", err)
		}
	}
	if err := m.applyLinks(ctx, s.Name, plan, prevLinks); err != nil {
		return shortID(newID), err
	}
	m.log.Info("container recreated", "name", s.Name, "image", s.Image, "id", shortID(newID))
	return shortID(newID), nil
}

func (m *Manager) rename(ctx context.Context, id, name string) error {
	return m.c.do2(ctx, "POST", "/containers/"+esc(id)+"/rename", url.Values{"name": {name}})
}

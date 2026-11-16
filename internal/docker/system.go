package docker

import (
	"archive/tar"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// Containers FELDCORE runs for itself. They are labelled so the UI can tell
// them from apps the user installed.
const (
	RoleLabel        = "feldcore.role"
	TunnelContainer  = "feldcore-cloudflared"
	ProxyContainer   = "feldcore-caddy"
	ProxyNetwork     = "feldcore"
	CaddyImage       = "caddy:2"
	CaddyPluginImage = "feldcore/caddy:2"
	CloudflaredImage = "cloudflare/cloudflared:latest"
	configLabel      = "feldcore.config"
)

// SystemPort is a host port published by a FELDCORE-owned container.
type SystemPort struct {
	Host      int
	Container int
}

// SystemSpec is a container FELDCORE owns (the tunnel connector or the proxy).
// Env may hold a credential. Callers must not log a SystemSpec.
type SystemSpec struct {
	Name       string
	Image      string
	Entrypoint []string
	Command    []string
	Binds      []string
	Env        []string
	Ports      []SystemPort
	Network    string
	ExtraHosts []string
	Role       string
	Hash       string
}

// PublishedPort is a host port a container is publishing.
type PublishedPort struct {
	Port      int
	Proto     string
	Container string
	Role      string
}

// Ready reports whether the Engine API answers.
func (m *Manager) Ready(ctx context.Context) bool {
	var v struct{ Version string }
	return m.c.getJSON(ctx, "/version", nil, &v) == nil
}

// ApplySystem creates or replaces a FELDCORE-owned container so it matches spec.
// A container already running with the same config hash is left alone.
func (m *Manager) ApplySystem(ctx context.Context, spec SystemSpec) error {
	ctx = context.WithoutCancel(ctx)
	if spec.Name == "" || spec.Image == "" {
		return invalid("A system container needs a name and an image.")
	}
	if spec.Network != "" && spec.Network != "host" && spec.Network != "bridge" && spec.Network != "none" {
		if err := m.EnsureNetwork(ctx, spec.Network); err != nil {
			return err
		}
	}
	in, err := m.inspect(ctx, spec.Name)
	if err == nil {
		labels := strMapOf(in.Config["Labels"])
		if spec.Hash != "" && labels[configLabel] == spec.Hash {
			if in.State.Running {
				return nil
			}
			return m.Start(ctx, in.ID)
		}
		if err := m.c.do2(ctx, "DELETE", "/containers/"+esc(in.ID), url.Values{"force": {"1"}}); err != nil {
			return &OpError{fmt.Sprintf("Could not replace %s: %v", spec.Name, err)}
		}
	} else if !NotFound(err) {
		return err
	}
	if !m.imagePresent(ctx, spec.Image) {
		if err := m.Pull(ctx, spec.Image); err != nil {
			return err
		}
	}
	id, err := m.createContainer(ctx, spec.Name, systemBody(spec))
	if err != nil {
		return &OpError{fmt.Sprintf("Could not create %s: %v", spec.Name, err)}
	}
	if err := m.Start(ctx, id); err != nil {
		return &OpError{fmt.Sprintf("%s was created but did not start: %v", spec.Name, err)}
	}
	// Name, image and role only. Env is not logged: it can hold a token.
	if m.log != nil {
		m.log.Info("system container applied", "name", spec.Name, "image", spec.Image, "role", spec.Role)
	}
	return nil
}

// StopSystem stops and removes a FELDCORE-owned container. A missing container
// is not an error.
func (m *Manager) StopSystem(ctx context.Context, name string) error {
	in, err := m.inspect(ctx, name)
	if NotFound(err) {
		return nil
	}
	if err != nil {
		return err
	}
	_ = m.Stop(ctx, in.ID)
	if err := m.c.do2(ctx, "DELETE", "/containers/"+esc(in.ID), url.Values{"force": {"1"}}); err != nil && !NotFound(err) {
		return err
	}
	return nil
}

// SystemState returns "running", "stopped" or "missing".
func (m *Manager) SystemState(ctx context.Context, name string) (string, error) {
	in, err := m.inspect(ctx, name)
	if NotFound(err) {
		return "missing", nil
	}
	if err != nil {
		return "", err
	}
	if in.State.Running {
		return "running", nil
	}
	return "stopped", nil
}

// EnsureNetwork creates a bridge network if it does not already exist.
func (m *Manager) EnsureNetwork(ctx context.Context, name string) error {
	err := m.c.postJSON(ctx, "/networks/create", nil, map[string]any{
		"Name": name, "Driver": "bridge", "CheckDuplicate": true,
	}, nil)
	if err == nil || alreadyExists(err) {
		return nil
	}
	return err
}

// ConnectNetwork attaches a container to a network. Already attached is fine.
// A missing container is reported to the caller.
func (m *Manager) ConnectNetwork(ctx context.Context, network, container string) error {
	err := m.c.postJSON(ctx, "/networks/"+esc(network)+"/connect", nil, map[string]string{
		"Container": container,
	}, nil)
	if err == nil || alreadyExists(err) {
		return nil
	}
	return err
}

// NetworkMode returns a container's network mode ("bridge", "host", "none", …).
func (m *Manager) NetworkMode(ctx context.Context, name string) (string, error) {
	in, err := m.inspect(ctx, name)
	if err != nil {
		return "", err
	}
	mode := strOf(in.HostConfig["NetworkMode"])
	if mode == "" || mode == "default" {
		return "bridge", nil
	}
	return mode, nil
}

// PublishedPorts lists host ports published by every container.
func (m *Manager) PublishedPorts(ctx context.Context) ([]PublishedPort, error) {
	var raw []apiContainer
	if err := m.c.getJSON(ctx, "/containers/json", url.Values{"all": {"1"}}, &raw); err != nil {
		return nil, err
	}
	var out []PublishedPort
	for _, c := range raw {
		for _, p := range c.Ports {
			if p.PublicPort == 0 {
				continue
			}
			out = append(out, PublishedPort{
				Port: p.PublicPort, Proto: p.Type, Container: cleanName(c.Names), Role: c.Labels[RoleLabel],
			})
		}
	}
	return out, nil
}

// ImagePresent reports whether a local image exists.
func (m *Manager) ImagePresent(ctx context.Context, ref string) bool {
	return m.imagePresent(ctx, ref)
}

// BuildImage builds a local image from a directory that contains a Dockerfile.
func (m *Manager) BuildImage(ctx context.Context, tag, dir string) error {
	ctx = context.WithoutCancel(ctx)
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	err := filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return err
		}
		rel, err := filepath.Rel(dir, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		hdr, err := tar.FileInfoHeader(info, "")
		if err != nil {
			return err
		}
		hdr.Name = rel
		if err := tw.WriteHeader(hdr); err != nil {
			return err
		}
		f, err := os.Open(path)
		if err != nil {
			return err
		}
		_, err = io.Copy(tw, f)
		f.Close()
		return err
	})
	if err != nil {
		return &OpError{fmt.Sprintf("Could not pack the proxy build: %v", err)}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	q := url.Values{"t": {tag}, "dockerfile": {"Dockerfile"}}
	resp, err := m.c.do(ctx, "POST", "/build", q, &buf, "application/x-tar")
	if err != nil {
		return &OpError{fmt.Sprintf("Could not build %s: %v", tag, err)}
	}
	defer drain(resp)
	sc := scanLines(resp.Body)
	for sc.Scan() {
		var line struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(sc.Bytes(), &line) == nil && line.Error != "" {
			return &OpError{fmt.Sprintf("Could not build %s: %s", tag, line.Error)}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	if m.log != nil {
		m.log.Info("image built", "tag", tag)
	}
	return nil
}

func systemBody(spec SystemSpec) map[string]any {
	labels := map[string]string{
		ManagedLabel: "true",
		RoleLabel:    spec.Role,
		configLabel:  spec.Hash,
	}
	host := map[string]any{
		"RestartPolicy": map[string]any{"Name": "unless-stopped"},
	}
	if len(spec.Binds) > 0 {
		host["Binds"] = spec.Binds
	}
	if len(spec.ExtraHosts) > 0 {
		host["ExtraHosts"] = spec.ExtraHosts
	}
	if spec.Network != "" {
		host["NetworkMode"] = spec.Network
	}
	cfg := map[string]any{
		"Image":  spec.Image,
		"Labels": labels,
	}
	if len(spec.Entrypoint) > 0 {
		cfg["Entrypoint"] = spec.Entrypoint
	}
	if len(spec.Command) > 0 {
		cfg["Cmd"] = spec.Command
	}
	if len(spec.Env) > 0 {
		cfg["Env"] = spec.Env
	}
	if len(spec.Ports) > 0 {
		pb := map[string]any{}
		exposed := map[string]any{}
		for _, p := range spec.Ports {
			key := strconv.Itoa(p.Container) + "/tcp"
			pb[key] = []any{map[string]any{"HostPort": strconv.Itoa(p.Host)}}
			exposed[key] = map[string]any{}
		}
		host["PortBindings"] = pb
		cfg["ExposedPorts"] = exposed
	}
	cfg["HostConfig"] = host
	return cfg
}

func alreadyExists(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "already")
}

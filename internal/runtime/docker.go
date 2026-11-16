// Package runtime connects VPN gateways and Proton Mail Bridge to Docker.
package runtime

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/notify"
	"github.com/Herculade0/feldcore/internal/vpn"
)

// Docker drives gateway and Bridge containers through the engine API.
type Docker struct {
	M    *docker.Manager
	HTTP *http.Client
}

func (d *Docker) client() *http.Client {
	if d.HTTP != nil {
		return d.HTTP
	}
	return &http.Client{Timeout: 2 * time.Second}
}

// Apply creates or replaces a VPN gateway. Env on g may hold a private key
// and is not logged.
func (d *Docker) Apply(ctx context.Context, g vpn.Gateway, mode vpn.ApplyMode) (bool, error) {
	if d.M == nil {
		return false, fmt.Errorf("docker is not available")
	}
	spec := gatewaySpec(g)
	found, ok, err := d.find(ctx, g.Name)
	if err != nil {
		return false, err
	}
	if !ok {
		_, err = d.M.Create(ctx, spec)
		return false, err
	}
	if mode == vpn.ApplyEnsure {
		if found.State != "running" && g.Start {
			return false, d.M.Start(ctx, found.ID)
		}
		return false, nil
	}
	_, err = d.M.Update(ctx, found.ID, spec)
	return true, err
}

func (d *Docker) Stop(ctx context.Context, name string) error {
	found, ok, err := d.find(ctx, name)
	if err != nil || !ok || found.State != "running" {
		return err
	}
	return d.M.Stop(ctx, found.ID)
}

func (d *Docker) Remove(ctx context.Context, name string) error {
	found, ok, err := d.find(ctx, name)
	if err != nil || !ok {
		return err
	}
	_, err = d.M.Remove(ctx, found.ID, true, false)
	return err
}

func (d *Docker) Restart(ctx context.Context, name string) error {
	found, ok, err := d.find(ctx, name)
	if err != nil || !ok {
		return err
	}
	return d.M.Restart(ctx, found.ID)
}

func (d *Docker) Probe(ctx context.Context, name string) (vpn.Sample, error) {
	var sample vpn.Sample
	pid, ip, running, err := d.info(ctx, name)
	if err != nil || !running {
		return sample, err
	}
	sample.Running = true
	_ = pid
	if ip == "" {
		return sample, nil
	}
	base := "http://" + ip + ":8000"
	sample.VPNStatus = d.get(ctx, base+"/v1/vpn/status")
	sample.PublicBody = d.get(ctx, base+"/v1/publicip/ip")
	sample.ForwardBody = d.get(ctx, base+"/v1/portforward")
	return sample, nil
}

func (d *Docker) InNamespace(ctx context.Context, name string, fn func() error) error {
	pid, _, running, err := d.info(ctx, name)
	if err != nil {
		return err
	}
	if !running {
		return fmt.Errorf("the VPN gateway is not running")
	}
	return vpn.EnterNetNS(pid, fn)
}

func (d *Docker) SetForwardedPort(ctx context.Context, name string, port int) error {
	_, ip, running, err := d.info(ctx, name)
	if err != nil || !running || ip == "" {
		return err
	}
	body := fmt.Sprintf(`{"ports":[%d]}`, port)
	req, err := http.NewRequestWithContext(ctx, http.MethodPut, "http://"+ip+":8000/v1/portforward", strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	res, err := d.client().Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	io.Copy(io.Discard, res.Body)
	if res.StatusCode >= 300 {
		return fmt.Errorf("gateway control server returned %s", res.Status)
	}
	return nil
}

func (d *Docker) ApplyBridge(ctx context.Context, dataDir string) error {
	if d.M == nil {
		return fmt.Errorf("docker is not available")
	}
	if err := os.MkdirAll(dataDir+"/proton-bridge", 0o700); err != nil {
		return err
	}
	spec := docker.Spec{
		Name: notify.BridgeName, Image: notify.BridgeImage,
		Network: "bridge", RestartPolicy: "unless-stopped", Autostart: true,
		Configs: []docker.Config{
			{Type: docker.TypePort, HostIP: "127.0.0.1", HostPort: notify.BridgePort, ContainerPort: 25, Proto: "tcp"},
			{Type: docker.TypePath, Host: dataDir + "/proton-bridge", Container: "/root", Mode: "rw"},
		},
	}
	found, ok, err := d.find(ctx, notify.BridgeName)
	if err != nil {
		return err
	}
	if !ok {
		_, err = d.M.Create(ctx, spec)
		return err
	}
	if found.State != "running" {
		return d.M.Start(ctx, found.ID)
	}
	return nil
}

func (d *Docker) StopBridge(ctx context.Context, name string) error { return d.Stop(ctx, name) }

func (d *Docker) BridgeRunning(ctx context.Context, name string) (bool, error) {
	found, ok, err := d.find(ctx, name)
	if err != nil || !ok {
		return false, err
	}
	return found.State == "running", nil
}

func (d *Docker) info(ctx context.Context, name string) (int, string, bool, error) {
	found, ok, err := d.find(ctx, name)
	if err != nil || !ok {
		return 0, "", false, err
	}
	return d.M.RuntimeInfo(ctx, found.ID)
}

func (d *Docker) find(ctx context.Context, name string) (docker.Container, bool, error) {
	cs, err := d.M.Containers(ctx)
	if err != nil {
		return docker.Container{}, false, err
	}
	for _, c := range cs {
		if c.Name == name {
			return c, true, nil
		}
	}
	return docker.Container{}, false, nil
}

func (d *Docker) get(ctx context.Context, url string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return ""
	}
	res, err := d.client().Do(req)
	if err != nil {
		return ""
	}
	defer res.Body.Close()
	buf, _ := io.ReadAll(io.LimitReader(res.Body, 1<<16))
	return string(bytes.TrimSpace(buf))
}

// Bridge adapts Docker to the notification service's Bridge container.
type Bridge struct{ D *Docker }

func (b Bridge) Apply(ctx context.Context, dataDir string) error {
	if b.D == nil {
		return fmt.Errorf("docker is not available")
	}
	return b.D.ApplyBridge(ctx, dataDir)
}

func (b Bridge) Stop(ctx context.Context, name string) error {
	if b.D == nil {
		return nil
	}
	return b.D.Stop(ctx, name)
}

func (b Bridge) Running(ctx context.Context, name string) (bool, error) {
	if b.D == nil {
		return false, nil
	}
	return b.D.BridgeRunning(ctx, name)
}

func gatewaySpec(g vpn.Gateway) docker.Spec {
	spec := docker.Spec{
		Name: g.Name, Image: g.Image, Network: "bridge",
		RestartPolicy: "unless-stopped", Autostart: g.Start, Gateway: true,
		ExtraParams: "--cap-add=NET_ADMIN",
	}
	for _, e := range g.Env {
		k, v, _ := strings.Cut(e, "=")
		mask := strings.Contains(k, "PRIVATE") || strings.Contains(k, "PRESHARED") || strings.Contains(k, "PASSWORD")
		spec.Configs = append(spec.Configs, docker.Config{
			Type: docker.TypeVariable, Key: k, Value: v, Mask: mask,
		})
	}
	for _, p := range g.Ports {
		spec.Configs = append(spec.Configs, docker.Config{
			Type: docker.TypePort, HostIP: p.HostIP, HostPort: p.Host,
			ContainerPort: p.Container, Proto: p.Proto,
		})
	}
	for _, dev := range g.Devices {
		spec.Configs = append(spec.Configs, docker.Config{
			Type: docker.TypeDevice, Host: dev, Container: dev, Mode: "rwm",
		})
	}
	return spec
}

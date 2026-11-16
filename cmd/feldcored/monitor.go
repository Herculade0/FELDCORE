package main

import (
	"context"
	"log/slog"
	"path/filepath"
	"strings"

	"github.com/Herculade0/feldcore/internal/config"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/events"
	"github.com/Herculade0/feldcore/internal/health"
	"github.com/Herculade0/feldcore/internal/notify"
	"github.com/Herculade0/feldcore/internal/remote"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/store"
	"github.com/Herculade0/feldcore/internal/vm"
	"github.com/Herculade0/feldcore/internal/vpn"
)

// wireMonitor starts the event bus, the notification dispatcher, and the
// health sampler. Call it before mail.Watch so container-stop events have a
// subscriber.
func wireMonitor(
	ctx context.Context,
	cfg *config.Config,
	st *store.Store,
	box *secrets.Box,
	log *slog.Logger,
	mail *notify.Service,
	dockerMgr *docker.Manager,
	remoteSvc *remote.Service,
	vpnSvc *vpn.Service,
) (*health.Service, *notify.Dispatcher) {
	bus := events.New()
	events.SetDefault(bus)
	if mail != nil {
		mail.Bus = bus
	}
	inbox := notify.OpenInbox(filepath.Join(cfg.DataDir, "inbox.json"))
	disp := notify.NewDispatcher(st, box, mail, inbox, filepath.Join(cfg.DataDir, "notify-dedupe.json"), log)
	bus.Subscribe(disp.Handle)

	hs := health.New(cfg.DataDir, log)
	hs.Store = st
	hs.Bus = bus
	hs.Scrub = func(s string) string { return secrets.Redact(s, box.Values()...) }
	hs.Certs = func(ctx context.Context) ([]health.Cert, error) {
		return health.ReadCerts(ctx, []string{
			filepath.Join(cfg.DataDir, "caddy"),
			filepath.Join(cfg.DataDir, "certs"),
		})
	}
	hs.Containers = func(ctx context.Context) ([]health.ContainerSnap, error) {
		return containerHealth(ctx, dockerMgr)
	}
	hs.VMs = func(ctx context.Context) ([]health.VMSnap, error) {
		list := (vm.Probe{}).List(ctx)
		if !list.Available || list.Message != "" {
			msg := list.Message
			if msg == "" {
				msg = "The VM layer is not installed on this server."
			}
			return nil, health.Unavailable(msg)
		}
		out := make([]health.VMSnap, 0, len(list.VMs))
		for _, d := range list.VMs {
			out = append(out, health.VMSnap{Name: d.Name, State: d.State})
		}
		return out, nil
	}
	hs.Tunnels = func(ctx context.Context) ([]health.TunnelSnap, error) {
		return tunnelHealth(ctx, st, remoteSvc, vpnSvc)
	}
	go hs.Run(ctx)
	return hs, disp
}

func containerHealth(ctx context.Context, dockerMgr *docker.Manager) ([]health.ContainerSnap, error) {
	if dockerMgr == nil {
		return nil, health.Unavailable("Docker is not running, so container health cannot be checked.")
	}
	st := dockerMgr.Status(ctx)
	if !st.Running {
		msg := st.Message
		if msg == "" {
			msg = "Docker is not running, so container health cannot be checked."
		}
		return nil, health.Unavailable(msg)
	}
	rows, err := dockerMgr.RuntimeHealth(ctx)
	if err != nil {
		return nil, health.Unavailable("Docker is not running, so container health cannot be checked.")
	}
	out := make([]health.ContainerSnap, 0, len(rows))
	for _, c := range rows {
		out = append(out, health.ContainerSnap{
			Name: c.Name, State: c.State, Health: c.Health,
			RestartsInWindow: c.RestartCount, OOMKilled: c.OOMKilled,
		})
	}
	return out, nil
}

func tunnelHealth(ctx context.Context, st *store.Store, remoteSvc *remote.Service, vpnSvc *vpn.Service) ([]health.TunnelSnap, error) {
	var out []health.TunnelSnap
	if remoteSvc != nil {
		v := remoteSvc.View(ctx)
		if v.Mode != "" && v.Mode != "off" {
			name := v.TunnelName
			if name == "" {
				name = "Cloudflare"
			}
			out = append(out, health.TunnelSnap{
				Name: name, Kind: "cloudflare",
				State: tunnelState(v.Status.State), Detail: v.Status.Summary,
			})
		}
	}
	if vpnSvc != nil && st != nil {
		snap := st.Snapshot().VPN
		if snap.Enabled {
			for _, t := range snap.Tunnels {
				if !t.Enabled {
					continue
				}
				name := t.Name
				if name == "" {
					name = t.ID
				}
				got, err := vpnSvc.Status(ctx, t.ID)
				if err != nil {
					out = append(out, health.TunnelSnap{
						Name: name, Kind: "vpn", State: "unknown", Detail: err.Error(),
					})
					continue
				}
				out = append(out, health.TunnelSnap{
					Name: name, Kind: "vpn", State: tunnelState(got.State), Detail: got.Message,
				})
			}
		}
	}
	return out, nil
}

func tunnelState(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "healthy", "up":
		return "up"
	case "starting":
		return "starting"
	case "down":
		return "down"
	case "off":
		return "off"
	default:
		return "unknown"
	}
}

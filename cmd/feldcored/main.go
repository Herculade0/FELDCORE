// Command feldcored is the FELDCORE daemon: a single self-contained binary that
// serves the management API and the embedded web UI.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Herculade0/feldcore/internal/api"
	"github.com/Herculade0/feldcore/internal/apps"
	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/backup"
	"github.com/Herculade0/feldcore/internal/citizenid"
	"github.com/Herculade0/feldcore/internal/config"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/notify"
	"github.com/Herculade0/feldcore/internal/proxy"
	"github.com/Herculade0/feldcore/internal/publish"
	"github.com/Herculade0/feldcore/internal/remote"
	"github.com/Herculade0/feldcore/internal/runtime"
	"github.com/Herculade0/feldcore/internal/secrets"
	"github.com/Herculade0/feldcore/internal/setup"
	"github.com/Herculade0/feldcore/internal/shares"
	"github.com/Herculade0/feldcore/internal/snapshots"
	"github.com/Herculade0/feldcore/internal/storage"
	"github.com/Herculade0/feldcore/internal/store"
	"github.com/Herculade0/feldcore/internal/system"
	"github.com/Herculade0/feldcore/internal/version"
	"github.com/Herculade0/feldcore/internal/vm"
	"github.com/Herculade0/feldcore/internal/vpn"
	"github.com/Herculade0/feldcore/web"
)

func main() {
	cfg := config.Load()
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	webRoot, err := web.Dist()
	if err != nil {
		log.Error("failed to open embedded web assets", "err", err)
		os.Exit(1)
	}

	st, err := store.Open(cfg.DataDir)
	if err != nil {
		log.Error("cannot open the data directory; run as root or pass -data-dir",
			"dir", cfg.DataDir, "err", err)
		os.Exit(1)
	}
	if !config.SharesRootForced() {
		if root := strings.TrimSpace(st.Snapshot().SharesRoot); root != "" {
			cfg.SharesRoot = root
		}
	}
	box, err := secrets.Open(cfg.DataDir)
	if err != nil {
		log.Error("cannot open the secrets directory", "dir", cfg.DataDir, "err", err)
		os.Exit(1)
	}
	// Every log line from here on is scrubbed of stored credentials.
	log = slog.New(secrets.NewRedactingHandler(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}), box))
	slog.SetDefault(log)

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// One background loop samples the host every 2s and keeps ~2 minutes.
	collector := system.NewCollector()
	sampler := system.NewSampler(collector, 2*time.Second, 60)
	go sampler.Run(ctx)

	shareMgr := shares.New(st, shares.Paths{
		SharesRoot:   cfg.SharesRoot,
		SambaConf:    cfg.SambaConf,
		SambaInclude: cfg.SambaInclude,
		ExportsFile:  cfg.ExportsFile,
	}, log)
	// The wizard asks which default shares to create. A headless install
	// (-skip-wizard, or an install that already finished setup) still uses
	// -default-shares.
	snap := st.Snapshot()
	wizardOwnsShares := !cfg.SkipWizard && (snap.Admin == nil || (snap.Wizard != nil && !snap.Wizard.Done))
	go func() {
		if cfg.DefaultShares && !wizardOwnsShares {
			shareMgr.EnsureDefaults(ctx)
		} else if wizardOwnsShares {
			log.Info("default shares will be chosen in the setup wizard")
		} else {
			log.Info("default shares disabled")
		}
		shareMgr.Apply(ctx)
	}()

	wiz := setup.New(setup.Deps{
		Store: st, Shares: shareMgr, DataDir: cfg.DataDir,
		ListenPort: listenPort(cfg.ListenAddr), Log: log,
		Skip: cfg.SkipWizard,
	})
	if code, err := wiz.EnsureCode(); err != nil {
		log.Error("could not prepare the setup code", "err", err)
	} else if code != "" {
		fmt.Fprintf(os.Stdout, "FELDCORE setup code: %s\n", setup.FormatCode(code))
		log.Info("enter the setup code above on the first-start screen; it works once")
	}

	if os.Geteuid() != 0 {
		log.Warn("not running as root: shares cannot be applied to the system")
	}

	dockerMgr := docker.NewManager(cfg.DockerSocket, st, log)
	rt := &runtime.Docker{M: dockerMgr}
	vpnSvc := &vpn.Service{Store: st, Secrets: box, RT: rt, Log: log}
	dockerMgr.SetRouteGuard(vpnSvc.AllowStart)
	backups := &backup.Service{Store: st, Secrets: box, DataDir: cfg.DataDir, SharesRoot: cfg.SharesRoot, Log: log}
	mail := &notify.Service{
		Store: st, Secrets: box, Bridge: runtime.Bridge{D: rt},
		DataDir: cfg.DataDir, Version: version.Current, Log: log,
	}
	authSvc := auth.New(st)
	authSvc.UseSecrets(box)
	wiz.UseAccounts(authSvc)
	wiz.UseAudit(authSvc)
	wiz.UseSecrets(box)
	wiz.UseMail(mail)
	wiz.UseDrive(backups)
	pubPort := publishListenPort()
	remoteSvc := &remote.Service{
		Store: st, Secrets: box, Engine: dockerMgr,
		Binary:      &remote.HostCloudflared{Dir: filepath.Join(cfg.DataDir, "bin")},
		DataDir:     cfg.DataDir,
		ListenPort:  listenPort(cfg.ListenAddr),
		GatewayPort: pubPort,
		Log:         log,
	}
	wiz.UseTunnel(remoteSvc)
	wiz.UseVPN(vpnSvc)
	proxySvc := &proxy.Service{
		Store: st, Secrets: box, Engine: dockerMgr,
		DataDir: cfg.DataDir, ListenPort: listenPort(cfg.ListenAddr), GatewayPort: pubPort, Log: log,
	}
	pubSvc := &publish.Service{
		Store: st, Secrets: box, Proxy: proxySvc, Remote: remoteSvc,
		Resolve: dockerMgr, Trust: dockerMgr.TrustedPeer,
		DataDir: cfg.DataDir, GatewayPort: pubPort, Log: log, Audit: authSvc,
	}
	pubSvc.OpenGeo()
	snapSvc := snapshots.Open(st, dockerMgr, cfg.DataDir, func() string {
		return filepath.Join(cfg.SharesRoot, "system", "snapshots")
	})
	snapSvc.Log = log
	go vpnSvc.Restore(ctx)
	go vpnSvc.KeepForwarded(ctx)
	go backups.Loop(ctx)
	healthSvc, alerts := wireMonitor(ctx, cfg, st, box, log, mail, dockerMgr, remoteSvc, vpnSvc)
	go mail.Watch(ctx, func(ctx context.Context) ([]notify.Container, error) {
		cs, err := dockerMgr.Containers(ctx)
		if err != nil {
			return nil, err
		}
		out := make([]notify.Container, 0, len(cs))
		for _, c := range cs {
			out = append(out, notify.Container{Name: c.Name, Running: c.State == "running"})
		}
		return out, nil
	}, wiz.LatestRelease, func() bool {
		snap := st.Snapshot()
		if snap.Prefs.Touched {
			return snap.Prefs.UpdateCheck
		}
		if snap.Wizard != nil {
			return snap.Wizard.UpdateCheck
		}
		return false
	})
	go func() {
		if err := remoteSvc.Sync(ctx); err != nil {
			log.Warn("remote access was not applied", "err", err)
		}
		if err := proxySvc.Apply(ctx); err != nil {
			log.Warn("web proxy was not applied", "err", err)
		}
	}()

	diskSvc, err := storage.Open(storage.Deps{
		DataDir: cfg.DataDir, SharesRoot: shareMgr.Root,
		PermitHostDisks: true, ApplySystem: os.Geteuid() == 0, Log: log,
		SetAppdata: st.SetAppdata,
		ShareNames: func() []string {
			folders := shareMgr.Folders()
			names := make([]string, 0, len(folders))
			for _, f := range folders {
				names = append(names, f.Name)
			}
			return names
		},
	})
	if err != nil {
		log.Error("cannot open storage", "err", err)
		os.Exit(1)
	}
	// The wizard's storage step and the Storage page share this controller.
	wiz.UseVolume(diskSvc)

	cid := citizenid.Open(citizenid.Deps{Store: st, Secrets: box, Audit: authSvc, Log: log, Trust: dockerMgr.TrustedPeer})
	authSvc.SetSecondFactor(cid.AdminFactor)
	pubSvc.SetAuth(publish.CitizenAuth(cid))
	srv := api.New(api.Deps{
		System:  collector,
		Sampler: sampler,
		Auth:    authSvc,
		Citizen: cid,
		Shares:  shareMgr,
		Docker:  dockerMgr,
		Apps: apps.New(apps.Deps{
			DataDir:    cfg.DataDir,
			SharesRoot: cfg.SharesRoot,
			FeedURL:    st.Snapshot().Prefs.FeedURL,
			Log:        log,
			SharePaths: func() map[string]string {
				out := map[string]string{}
				for _, f := range shareMgr.Folders() {
					out[strings.ToLower(f.Name)] = f.Path
				}
				return out
			},
		}),
		VMs: vm.New(vm.Deps{
			Store: st, SharesRoot: cfg.SharesRoot, Log: log,
			Shares: func() []vm.ShareInfo {
				var out []vm.ShareInfo
				for _, f := range shareMgr.Folders() {
					out = append(out, vm.ShareInfo{Name: f.Name, Path: f.Path})
				}
				return out
			},
		}),
		Setup:      wiz,
		Remote:     remoteSvc,
		Proxy:      proxySvc,
		Snaps:      snapSvc,
		SkipWizard: cfg.SkipWizard,
		WebRoot:    webRoot,
		Log:        log,
		VPN:        vpnSvc,
		Backups:    backups,
		Mail:       mail,
		Publish:    pubSvc,
		Storage:    diskSvc,
		Health:     healthSvc,
		Alerts:     alerts,
	})
	go func() {
		if err := diskSvc.Restore(ctx); err != nil {
			log.Warn("a storage pool was not mounted", "err", err)
		}
		diskSvc.Loop(ctx)
	}()
	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	pubHTTP := &http.Server{
		Addr:              publishListenAddr(),
		Handler:           pubSvc.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}
	go func() {
		log.Info("feldcored listening", "addr", cfg.ListenAddr, "data", cfg.DataDir)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "err", err)
			stop()
		}
	}()
	go func() {
		log.Info("publish proxy listening", "addr", pubHTTP.Addr)
		if err := pubHTTP.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("publish proxy error", "err", err)
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
	if err := pubHTTP.Shutdown(shutdownCtx); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Error("publish proxy shutdown failed", "err", err)
	}
}

// publishListenAddr is the listener Caddy and cloudflared dial. It is not
// only loopback: the containers reach the host through the Docker bridge.
// FELDCORE_PUBLISH_PORT overrides the port. Nothing is published until a
// container's Publish switch is turned on.
func publishListenAddr() string {
	return fmt.Sprintf(":%d", publishListenPort())
}

func publishListenPort() int {
	p := strings.TrimSpace(os.Getenv("FELDCORE_PUBLISH_PORT"))
	if p == "" {
		return 8791
	}
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 {
		return 8791
	}
	return n
}

func listenPort(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 8787
	}
	n, err := strconv.Atoi(p)
	if err != nil || n == 0 {
		return 8787
	}
	return n
}

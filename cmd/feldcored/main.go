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
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/Herculade0/feldcore/internal/api"
	"github.com/Herculade0/feldcore/internal/apps"
	"github.com/Herculade0/feldcore/internal/auth"
	"github.com/Herculade0/feldcore/internal/config"
	"github.com/Herculade0/feldcore/internal/docker"
	"github.com/Herculade0/feldcore/internal/setup"
	"github.com/Herculade0/feldcore/internal/shares"
	"github.com/Herculade0/feldcore/internal/store"
	"github.com/Herculade0/feldcore/internal/system"
	"github.com/Herculade0/feldcore/internal/vm"
	"github.com/Herculade0/feldcore/web"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))
	slog.SetDefault(log)

	cfg := config.Load()

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

	srv := api.New(api.Deps{
		System:  collector,
		Sampler: sampler,
		Auth:    auth.New(st),
		Shares:  shareMgr,
		Docker:  docker.NewManager(cfg.DockerSocket, st, log),
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
		SkipWizard: cfg.SkipWizard,
		WebRoot:    webRoot,
		Log:        log,
	})
	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("feldcored listening", "addr", cfg.ListenAddr, "data", cfg.DataDir)
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Error("http server error", "err", err)
			stop()
		}
	}()

	<-ctx.Done()
	log.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := httpSrv.Shutdown(shutdownCtx); err != nil {
		log.Error("graceful shutdown failed", "err", err)
	}
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

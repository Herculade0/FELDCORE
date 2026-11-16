// Command feldcored is the FELDCORE daemon: a single self-contained binary that
// serves the management API and the embedded web UI.
package main

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Herculade0/feldcore/internal/api"
	"github.com/Herculade0/feldcore/internal/config"
	"github.com/Herculade0/feldcore/internal/system"
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

	// Graceful shutdown on SIGINT/SIGTERM.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// One background loop samples the host every 2s and keeps ~2 minutes.
	collector := system.NewCollector()
	sampler := system.NewSampler(collector, 2*time.Second, 60)
	go sampler.Run(ctx)

	srv := api.New(collector, sampler, webRoot)
	httpSrv := &http.Server{
		Addr:              cfg.ListenAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("feldcored listening", "addr", cfg.ListenAddr)
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

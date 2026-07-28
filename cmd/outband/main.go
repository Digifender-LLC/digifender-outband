package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"outband/internal/config"
	"outband/internal/hosts"
	"outband/internal/httpapi"
	"outband/internal/provider"
	"outband/internal/telemetry"

	// Register BMC providers (ipmi, amt, ilo, idrac).
	_ "outband/internal/amt"
	_ "outband/internal/idrac"
	_ "outband/internal/ilo"
	_ "outband/internal/ipmi"
)

func main() {
	log := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: slog.LevelInfo}))

	cfg, err := config.Load(os.Args[1:])
	if err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}
	if err := config.ValidateProviders(cfg, provider.Known); err != nil {
		log.Error("config", "err", err)
		os.Exit(2)
	}

	registry, err := hosts.Open(cfg.Hosts, cfg.DefaultHost, log)
	if err != nil {
		log.Error("hosts", "err", err)
		os.Exit(1)
	}
	defer registry.Close()

	store, err := telemetry.Open(cfg.DataDir)
	if err != nil {
		log.Error("telemetry store", "err", err)
		os.Exit(1)
	}
	defer store.Close()

	gate, err := httpapi.NewGate(cfg.UIPass)
	if err != nil {
		log.Error("auth gate", "err", err)
		os.Exit(1)
	}

	srv, err := httpapi.New(registry, gate, store, log, cfg.OIDC)
	if err != nil {
		log.Error("http server", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	intervals := telemetry.PollIntervals{
		Sensors: cfg.PollSensors,
		Power:   cfg.PollPower,
		SEL:     cfg.PollSEL,
		MCInfo:  cfg.PollMCInfo,
	}
	retention := time.Duration(cfg.RetentionDays) * 24 * time.Hour
	for _, h := range registry.All() {
		host := h
		feats := host.Features()
		collector := &telemetry.Collector{
			HostID:       host.ID,
			Client:       host.Client,
			Store:        store,
			Intervals:    intervals,
			Retention:    retention,
			Log:          log,
			RenameSensor: host.RenameSensor,
			Features:     &feats,
		}
		go collector.Run(ctx)
	}

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	def := registry.Default()
	go func() {
		log.Info("Outband listening",
			"addr", cfg.Listen,
			"default_host", def.ID,
			"provider", def.Provider,
			"bmc", def.Address,
			"hosts", len(registry.All()),
			"data", cfg.DataDir,
		)
		if err := httpServer.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			log.Error("listen", "err", err)
			os.Exit(1)
		}
	}()

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	<-sigCh

	cancel()
	shutdownCtx, shutdownCancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer shutdownCancel()
	_ = httpServer.Shutdown(shutdownCtx)
	log.Info("shutdown complete")
}

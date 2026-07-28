package main

import (
	"context"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"mipmi/internal/config"
	"mipmi/internal/hosts"
	"mipmi/internal/httpapi"
	"mipmi/internal/provider"
	"mipmi/internal/telemetry"

	// Register BMC providers (ipmi + stubs in provider).
	_ "mipmi/internal/ipmi"
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

	active := registry.Default()

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

	srv, err := httpapi.New(active, gate, store, log)
	if err != nil {
		log.Error("http server", "err", err)
		os.Exit(1)
	}

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	collector := &telemetry.Collector{
		HostID: active.ID,
		Client: active.Client,
		Store:  store,
		Intervals: telemetry.PollIntervals{
			Sensors: cfg.PollSensors,
			Power:   cfg.PollPower,
			SEL:     cfg.PollSEL,
			MCInfo:  cfg.PollMCInfo,
		},
		Retention: time.Duration(cfg.RetentionDays) * 24 * time.Hour,
		Log:       log,
	}
	go collector.Run(ctx)

	httpServer := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Handler(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	go func() {
		log.Info("mIPMI listening",
			"addr", cfg.Listen,
			"host", active.ID,
			"provider", active.Provider,
			"bmc", active.Address,
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

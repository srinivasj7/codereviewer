// admin-ui serves the web interface for configuring the deployment,
// viewing dashboard counts, and exporting/importing config + selective
// data backups. Auth: single admin password (and optionally GitHub
// OAuth) verified by the SecretsProvider.
//
// Bootstrap config still lives in TOML (postgres URL, secrets provider,
// bus URL, listen addr). Runtime-tunable values overlay from app_settings.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"codereviewer/internal/admin"
	"codereviewer/internal/boot"
	"codereviewer/internal/config"
	"codereviewer/internal/ports"
	"codereviewer/internal/schemas"
)

func main() {
	cfgPath := flag.String("config", "config.toml", "path to TOML config file")
	flag.Parse()
	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "admin-ui:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
	}

	secrets, err := boot.PickSecrets(cfg.Secrets)
	if err != nil {
		return fmt.Errorf("secrets: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	stores, err := boot.PickStores(ctx, cfg.Store, ports.Obs{})
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if stores.Close != nil {
		defer stores.Close()
	}
	if err := config.ApplyOverlay(ctx, cfg, stores.Settings); err != nil {
		return fmt.Errorf("apply settings overlay: %w", err)
	}

	obs, shutdownObs := boot.PickObservability(ctx, cfg.Observability)
	defer flushObs(shutdownObs)

	reloader, err := boot.NewReloader(*cfg, stores.Settings, 30*time.Second)
	if err != nil {
		return fmt.Errorf("settings reloader: %w", err)
	}
	go reloader.Run(ctx, obs.Logger)

	bus, err := boot.PickBus(ctx, cfg.MessageBus, obs)
	if err != nil {
		return fmt.Errorf("bus: %w", err)
	}

	password, err := secrets.Get(ctx, "ADMIN_PASSWORD")
	if err != nil || password == "" {
		// Fall back to TOML (`admin.password`); useful for dev where
		// the operator hard-codes a value via env expansion.
		password = cfg.Admin.Password
	}
	sessionSecret, err := secrets.Get(ctx, "ADMIN_SESSION_SECRET")
	if err != nil || sessionSecret == "" {
		sessionSecret = cfg.Admin.SessionSecret
	}

	srv, err := admin.New(admin.Deps{
		Cfg:      cfg,
		Settings: stores.Settings,
		Comments: stores.Comments,
		Rules:    stores.Rules,
		PrRuns:   stores.PrRuns,
		Repos:    stores.Repos,
		Context:  stores.Context,
		Bus:      bus,
		Pool:     stores.RawHandle,
		Obs:      obs,
	}, password, sessionSecret, false /* secure cookie: production sets via reverse proxy */)
	if err != nil {
		return fmt.Errorf("admin server: %w", err)
	}

	httpSrv := &http.Server{
		Addr:              cfg.Admin.ListenAddr,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       60 * time.Second,
		WriteTimeout:      60 * time.Second,
	}

	if cfg.Admin.AutoExportEnabled {
		exporter := admin.NewAutoExporter(srv, cfg.Admin.ExportDir,
			time.Duration(cfg.Admin.AutoExportHours)*time.Hour)
		go exporter.Run(ctx)
	}

	if cfg.Retention.JanitorEnabled {
		j := &admin.Janitor{
			PrRuns:         stores.PrRuns,
			Feedback:       stores.Feedback,
			Context:        stores.Context,
			EmbeddingCache: stores.EmbeddingCache,
			ExportDir:      cfg.Admin.ExportDir,
			Live:           func() schemas.RetentionConfig { return reloader.Current().Retention },
			Interval:       time.Duration(cfg.Retention.JanitorIntervalHours) * time.Hour,
			Obs:            obs.Logger,
		}
		go j.Run(ctx)
	}

	go func() {
		obs.Logger.Info("admin-ui listening", "addr", httpSrv.Addr)
		if err := httpSrv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			obs.Logger.Error("listen failed", "err", err.Error())
		}
	}()

	<-ctx.Done()
	obs.Logger.Info("admin-ui shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	return httpSrv.Shutdown(shutdownCtx)
}


// flushObs gives the OTel exporters a small window to drain.
func flushObs(shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

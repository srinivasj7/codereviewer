// rules-sync clones the configured rules repo, parses each rule file,
// embeds, and upserts via RuleStore. Slice 5 will fire it from a
// webhook on the rules repo; for now it runs as a one-shot CLI or a
// scheduled job.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"codereviewer/internal/adapters/llmlitellm"
	"codereviewer/internal/adapters/rulessourcegit"
	"codereviewer/internal/boot"
	"codereviewer/internal/config"
	"codereviewer/internal/core/pipelines/rulessync"
	"codereviewer/internal/ports"
)

func main() {
	cfgPath := flag.String("config", "config.toml", "path to TOML config file")
	tenantOverride := flag.String("tenant-id", "", "override the configured tenant id")
	pattern := flag.String("pattern", "rules/**/*.md", "glob for rule files (supports prefix/**/*.ext)")
	flag.Parse()

	if err := run(*cfgPath, *tenantOverride, *pattern); err != nil {
		fmt.Fprintln(os.Stderr, "rules-sync:", err)
		os.Exit(1)
	}
}

func run(cfgPath, tenantOverride, pattern string) error {
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

	// Stores boot from TOML-only config so the runtime overlay table is
	// reachable; everything else then sees the overlayed values.
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

	if cfg.Rules.GitURL == "" {
		return fmt.Errorf("rules.git_url is empty (set via TOML or admin UI settings)")
	}
	tenantId := ports.TenantId(cfg.Tenant.Id)
	if tenantOverride != "" {
		tenantId = ports.TenantId(tenantOverride)
	}

	obs, shutdownObs := boot.PickObservability(ctx, cfg.Observability)
	defer flushObs(shutdownObs)

	llm, err := boot.PickLlm(cfg.Llm, secrets, obs, llmlitellm.ModelURLs{})
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}

	source := rulessourcegit.New(pattern)
	pipeline := rulessync.NewPipeline(rulessync.Deps{
		Source:         source,
		Llm:            llm,
		Obs:            obs,
		Rules:          stores.Rules,
		EmbeddingCache: stores.EmbeddingCache,
		EmbeddingModel: cfg.Llm.EmbeddingsURL,
		GitUrl:         cfg.Rules.GitURL,
		Branch:         cfg.Rules.Branch,
	})

	start := time.Now()
	n, err := pipeline.Run(ctx, rulessync.Args{TenantId: tenantId})
	if err != nil {
		return err
	}
	obs.Logger.Info("rules-sync completed",
		"git_url", cfg.Rules.GitURL,
		"rules_synced", n,
		"duration_ms", time.Since(start).Milliseconds(),
	)
	return nil
}

// flushObs gives the OTel exporters a small window to drain. Errors are
// dropped — at shutdown time there's no actionable handler.
func flushObs(shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

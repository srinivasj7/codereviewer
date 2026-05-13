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
	if cfg.Rules.GitURL == "" {
		return fmt.Errorf("rules.git_url is empty in %s", cfgPath)
	}

	tenantId := ports.TenantId(cfg.Tenant.Id)
	if tenantOverride != "" {
		tenantId = ports.TenantId(tenantOverride)
	}

	secrets, err := boot.PickSecrets(cfg.Secrets)
	if err != nil {
		return fmt.Errorf("secrets: %w", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	obs, shutdownObs := boot.PickObservability(ctx, cfg.Observability)
	defer flushObs(shutdownObs)

	llm, err := boot.PickLlm(cfg.Llm, secrets, obs)
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}
	stores, err := boot.PickStores(ctx, cfg.Store, obs)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if stores.Close != nil {
		defer stores.Close()
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

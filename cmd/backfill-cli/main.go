// backfill-cli runs the backfill pipeline once for a given (repo,
// window). It exits non-zero on configuration errors; transient API
// failures inside the run are logged and skipped so one bad PR
// doesn't abort the entire backfill.
//
//	backfill-cli --config dev.toml --repo owner/name --window-days 270
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
	"codereviewer/internal/boot"
	"codereviewer/internal/config"
	"codereviewer/internal/core/pipelines/backfill"
	"codereviewer/internal/ports"
)

func main() {
	cfgPath := flag.String("config", "config.toml", "path to TOML config file")
	repoFlag := flag.String("repo", "", "repository as owner/name (required)")
	windowDays := flag.Int("window-days", 270, "history window in days (default: 9 months)")
	tenantOverride := flag.String("tenant-id", "", "override the configured tenant id")
	flag.Parse()

	if *repoFlag == "" {
		fmt.Fprintln(os.Stderr, "backfill-cli: --repo owner/name is required")
		flag.Usage()
		os.Exit(2)
	}

	if err := run(*cfgPath, *repoFlag, *windowDays, *tenantOverride); err != nil {
		fmt.Fprintln(os.Stderr, "backfill-cli:", err)
		os.Exit(1)
	}
}

func run(cfgPath, repo string, windowDays int, tenantOverride string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
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

	obs, shutdownObs := boot.PickObservability(ctx, cfg.Observability)
	defer flushObs(shutdownObs)

	vcs, err := boot.PickVcs(cfg.Vcs, secrets)
	if err != nil {
		return fmt.Errorf("vcs: %w", err)
	}
	llm, err := boot.PickLlm(cfg.Llm, secrets, obs, llmlitellm.ModelURLs{})
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}

	// Ensure the repo is registered before we start writing comments
	// against it; the gateway normally does this on first webhook but
	// a fresh deploy may have never received one yet.
	if stores.Repos != nil {
		_ = stores.Repos.EnsureExists(ctx, ports.RepoRef{
			TenantId: tenantId,
			RepoId:   ports.RepoId(repo),
			Owner:    splitOwner(repo),
			Name:     splitName(repo),
		})
	}

	pipeline := backfill.NewPipeline(backfill.Deps{
		Vcs:            vcs,
		Llm:            llm,
		Obs:            obs,
		Comments:       stores.Comments,
		EmbeddingCache: stores.EmbeddingCache,
		EmbeddingModel: cfg.Llm.EmbeddingsURL,
	})

	start := time.Now()
	n, err := pipeline.Run(ctx, backfill.Args{
		TenantId:   tenantId,
		RepoId:     ports.RepoId(repo),
		WindowDays: windowDays,
		Now:        start,
	})
	if err != nil {
		return err
	}
	obs.Logger.Info("backfill-cli finished",
		"repo", repo,
		"window_days", windowDays,
		"comments_upserted", n,
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

func splitOwner(repo string) string {
	for i := 0; i < len(repo); i++ {
		if repo[i] == '/' {
			return repo[:i]
		}
	}
	return ""
}

func splitName(repo string) string {
	for i := 0; i < len(repo); i++ {
		if repo[i] == '/' {
			return repo[i+1:]
		}
	}
	return ""
}

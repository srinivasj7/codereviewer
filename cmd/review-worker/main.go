// review-worker consumes ReviewJob messages from the bus and runs the
// review pipeline for each. Composition root: loads config, picks
// adapter implementations, wires the pipeline, and starts consuming.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"codereviewer/internal/adapters/contextadhoc"
	"codereviewer/internal/adapters/contextgithubissues"
	"codereviewer/internal/adapters/contextjira"
	"codereviewer/internal/adapters/contextlinear"
	"codereviewer/internal/adapters/contextrepoinstructions"
	"codereviewer/internal/adapters/vcsgithub"
	"codereviewer/internal/boot"
	"codereviewer/internal/config"
	"codereviewer/internal/core/pipelines/review"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

func main() {
	cfgPath := flag.String("config", "config.toml", "path to TOML config file")
	flag.Parse()

	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "review-worker:", err)
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
	clock := boot.PickClock()

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

	bus, err := boot.PickBus(ctx, cfg.MessageBus, obs)
	if err != nil {
		return fmt.Errorf("bus: %w", err)
	}

	vcs, err := boot.PickVcs(cfg.Vcs, secrets)
	if err != nil {
		return fmt.Errorf("vcs: %w", err)
	}

	llm, err := boot.PickLlm(cfg.Llm, secrets, obs)
	if err != nil {
		return fmt.Errorf("llm: %w", err)
	}

	providers := pickContextProviders(cfg, vcs, stores.Context, obs)
	pipeline := review.NewPipeline(review.Deps{
		Vcs:              vcs,
		Llm:              llm,
		Clock:            clock,
		Obs:              obs,
		CodeChunks:       stores.CodeChunks,
		Comments:         stores.Comments,
		Rules:            stores.Rules,
		PrRuns:           stores.PrRuns,
		CostCaps:         stores.CostCaps,
		EmbeddingCache:   stores.EmbeddingCache,
		ContextProviders: providers,
		TokenCap:         cfg.Llm.PerPrTokenCap,
	})

	sub, err := bus.Consume(ctx, ports.QueueReview, pipeline.Handle)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}
	defer func() { _ = sub.Stop() }()

	obs.Logger.Info("review-worker started; awaiting jobs")
	<-ctx.Done()
	obs.Logger.Info("review-worker shutting down")
	return nil
}

// flushObs gives the OTel exporters a small window to drain. Errors are
// dropped — at shutdown time there's no actionable handler.
func flushObs(shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

// pickContextProviders constructs the enabled context providers based
// on cfg. Always includes repo-instructions and ad-hoc (cheap and
// always-relevant). The three issue trackers light up only when their
// config block is populated. The github-issues provider type-asserts
// the VcsSource for .Client() so it shares the App's auth; if the VCS
// adapter isn't vcsgithub.Source, the provider is skipped.
func pickContextProviders(
	cfg *schemas.Config,
	vcs ports.VcsSource,
	ctxStore store.ContextStore,
	obs ports.Obs,
) []ports.ContextProvider {
	providers := []ports.ContextProvider{
		contextrepoinstructions.New(vcs, ctxStore, obs),
		contextadhoc.New(ctxStore, cfg.Context.MaxItemsPerPr, obs),
	}
	if cfg.Context.Jira.BaseURL != "" {
		providers = append(providers,
			contextjira.New(cfg.Context.Jira.BaseURL, cfg.Context.Jira.Email,
				cfg.Context.Jira.APIToken, vcs, obs))
	}
	if cfg.Context.GithubIssues.Enabled {
		if gh, ok := vcs.(*vcsgithub.Source); ok {
			providers = append(providers, contextgithubissues.New(gh.Client(), vcs, obs))
		} else {
			obs.Logger.Warn("github-issues context provider enabled but VcsSource is not vcsgithub; skipping")
		}
	}
	if cfg.Context.Linear.APIKey != "" {
		providers = append(providers,
			contextlinear.New(cfg.Context.Linear.APIKey, cfg.Context.Linear.TeamPrefixes, vcs, obs))
	}
	return providers
}

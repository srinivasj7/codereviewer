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

	"codereviewer/internal/boot"
	"codereviewer/internal/config"
	"codereviewer/internal/core/pipelines/review"
	"codereviewer/internal/ports"
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

	stores, err := boot.PickStores(ctx, cfg.Store, obs)
	if err != nil {
		return fmt.Errorf("store: %w", err)
	}
	if stores.Close != nil {
		defer stores.Close()
	}

	pipeline := review.NewPipeline(review.Deps{
		Vcs:            vcs,
		Llm:            llm,
		Clock:          clock,
		Obs:            obs,
		CodeChunks:     stores.CodeChunks,
		Comments:       stores.Comments,
		Rules:          stores.Rules,
		PrRuns:         stores.PrRuns,
		CostCaps:       stores.CostCaps,
		EmbeddingCache: stores.EmbeddingCache,
		TokenCap:       cfg.Llm.PerPrTokenCap,
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

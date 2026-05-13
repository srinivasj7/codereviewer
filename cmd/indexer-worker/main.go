// indexer-worker consumes IndexJob messages and indexes default-branch
// pushes into code_chunks (design §6.2). Composition root.
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
	"codereviewer/internal/core/pipelines/indexer"
	"codereviewer/internal/ports"
)

func main() {
	cfgPath := flag.String("config", "config.toml", "path to TOML config file")
	flag.Parse()

	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "indexer-worker:", err)
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

	parser := boot.PickParser()

	pipeline := indexer.NewPipeline(indexer.Deps{
		Vcs:            vcs,
		Llm:            llm,
		Parser:         parser,
		Obs:            obs,
		CodeChunks:     stores.CodeChunks,
		EmbeddingCache: stores.EmbeddingCache,
		EmbeddingModel: cfg.Llm.EmbeddingsURL,
	})

	sub, err := bus.Consume(ctx, ports.QueueIndex, pipeline.Handle)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}
	defer func() { _ = sub.Stop() }()

	obs.Logger.Info("indexer-worker started; awaiting jobs")
	<-ctx.Done()
	obs.Logger.Info("indexer-worker shutting down")
	return nil
}

// flushObs gives the OTel exporters a small window to drain. Errors are
// dropped — at shutdown time there's no actionable handler.
func flushObs(shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

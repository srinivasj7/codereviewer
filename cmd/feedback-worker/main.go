// feedback-worker consumes FeedbackJob messages from the bus and
// records the implied outcome on the targeted bot comment.
//
// Composition root: loads config, picks adapter implementations,
// wires the pipeline, and starts consuming.
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
	"codereviewer/internal/core/pipelines/feedback"
	"codereviewer/internal/ports"
	"codereviewer/internal/schemas"
)

func main() {
	cfgPath := flag.String("config", "config.toml", "path to TOML config file")
	flag.Parse()

	if err := run(*cfgPath); err != nil {
		fmt.Fprintln(os.Stderr, "feedback-worker:", err)
		os.Exit(1)
	}
}

func run(cfgPath string) error {
	cfg, err := config.Load(cfgPath)
	if err != nil {
		return err
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

	reloader, err := boot.NewReloader(*cfg, stores.Settings, 30*time.Second)
	if err != nil {
		return fmt.Errorf("settings reloader: %w", err)
	}
	go reloader.Run(ctx, obs.Logger)

	bus, err := boot.PickBus(ctx, cfg.MessageBus, obs)
	if err != nil {
		return fmt.Errorf("bus: %w", err)
	}

	pipeline := feedback.NewPipeline(feedback.Deps{
		Comments: stores.Comments,
		Feedback: stores.Feedback,
		Clock:    clock,
		Obs:      obs,
	})

	// Slice 8 — conversation mode. Wired only when [conversation].enabled
	// is true on boot; the closures still read live overlay values so the
	// admin UI can flip triggers/cap at runtime.
	if cfg.Conversation.Enabled {
		secrets, err := boot.PickSecrets(cfg.Secrets)
		if err != nil {
			return fmt.Errorf("secrets: %w", err)
		}
		vcs, err := boot.PickVcs(cfg.Vcs, secrets)
		if err != nil {
			return fmt.Errorf("vcs: %w", err)
		}
		llm, err := boot.PickLlm(cfg.Llm, secrets, obs, llmlitellm.ModelURLs{
			Primary:    func() string { return reloader.Current().Llm.PrimaryModelURL },
			Fallback:   func() string { return reloader.Current().Llm.FallbackModelURL },
			Embeddings: func() string { return reloader.Current().Llm.EmbeddingsURL },
		})
		if err != nil {
			return fmt.Errorf("llm: %w", err)
		}
		pipeline.SetConversationDeps(vcs, llm, stores.CostCaps,
			func() schemas.ConversationConfig { return reloader.Current().Conversation })
		obs.Logger.Info("conversation mode enabled",
			"max_replies_per_pr", cfg.Conversation.MaxRepliesPerPr,
			"trigger_suffixes", cfg.Conversation.TriggerSuffixes,
			"trigger_prefixes", cfg.Conversation.TriggerPrefixes,
		)
	}

	sub, err := bus.Consume(ctx, ports.QueueFeedback, pipeline.Handle)
	if err != nil {
		return fmt.Errorf("consume: %w", err)
	}
	defer func() { _ = sub.Stop() }()

	obs.Logger.Info("feedback-worker started; awaiting jobs")
	<-ctx.Done()
	obs.Logger.Info("feedback-worker shutting down")
	return nil
}

// flushObs gives the OTel exporters a small window to drain. Errors are
// dropped — at shutdown time there's no actionable handler.
func flushObs(shutdown func(context.Context) error) {
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = shutdown(ctx)
}

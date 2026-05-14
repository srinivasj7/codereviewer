// Package boot picks adapter implementations based on the loaded config
// and wires them into use cases. It is the only package that imports
// from internal/adapters, keeping the dependency direction clean.
package boot

import (
	"context"
	"fmt"

	"codereviewer/internal/adapters/busmem"
	"codereviewer/internal/adapters/busnats"
	"codereviewer/internal/adapters/clocksystem"
	"codereviewer/internal/adapters/llmlitellm"
	"codereviewer/internal/adapters/obsotel"
	"codereviewer/internal/adapters/obsstdout"
	"codereviewer/internal/adapters/parsertreesitter"
	"codereviewer/internal/adapters/secretsenv"
	"codereviewer/internal/adapters/storepostgres"
	"codereviewer/internal/adapters/vcsgithub"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

// Stores bundles the ten store sub-ports. Adapters that back all of
// them with one connection (storepostgres) return all ten at once.
//
// RawHandle is the adapter's underlying connection (e.g. *pgxpool.Pool
// for storepostgres). It's `any` to keep the bus of dependencies above
// the adapter packages; consumers that need raw SQL access (the admin
// UI for export/import) type-assert internally.
type Stores struct {
	Repos          store.RepoStore
	CodeChunks     store.CodeChunkStore
	Comments       store.CommentStore
	Rules          store.RuleStore
	PrRuns         store.PrRunStore
	Feedback       store.FeedbackStore
	CostCaps       store.CostCapStore
	EmbeddingCache store.EmbeddingCache
	Settings       store.SettingsStore
	Context        store.ContextStore
	RawHandle      any
	Close          func()
}

// PickBus selects a MessageBus implementation.
func PickBus(ctx context.Context, cfg schemas.MessageBusConfig, _ ports.Obs) (ports.MessageBus, error) {
	switch cfg.Type {
	case "memory":
		return busmem.New(), nil
	case "nats":
		if cfg.ReviewQueueURL == "" {
			return nil, fmt.Errorf("message_bus.review_queue_url must be set for nats (used as NATS URL)")
		}
		return busnats.New(ctx, cfg.ReviewQueueURL)
	case "sqs":
		return nil, fmt.Errorf("bussqs adapter not yet implemented (slice 5)")
	}
	return nil, fmt.Errorf("unknown message_bus.type: %q", cfg.Type)
}

// PickSecrets selects a SecretsProvider.
func PickSecrets(cfg schemas.SecretsConfig) (ports.SecretsProvider, error) {
	switch cfg.Provider {
	case "env":
		return secretsenv.New(), nil
	case "aws":
		return nil, fmt.Errorf("secretsaws adapter not yet implemented (slice 5)")
	case "vault":
		return nil, fmt.Errorf("secretsvault adapter not yet implemented")
	}
	return nil, fmt.Errorf("unknown secrets.provider: %q", cfg.Provider)
}

// PickObservability selects an Obs bundle. Sink "stdout" returns
// obsstdout (no shutdown needed); "otel" returns the OTLP-HTTP-backed
// obsotel adapter and a shutdown function that flushes both providers.
// On "otel" exporter setup failure, falls back to stdout so the process
// stays runnable — telemetry is best-effort.
func PickObservability(ctx context.Context, cfg schemas.ObservabilityConfig) (ports.Obs, func(context.Context) error) {
	noShutdown := func(context.Context) error { return nil }
	switch cfg.Sink {
	case "otel":
		if cfg.OtlpEndpoint == "" {
			return obsstdout.New(cfg.ServiceName), noShutdown
		}
		obs, shutdown, err := obsotel.New(ctx, cfg.ServiceName, cfg.OtlpEndpoint)
		if err != nil {
			fallback := obsstdout.New(cfg.ServiceName)
			fallback.Logger.Warn("obsotel init failed; falling back to stdout",
				"endpoint", cfg.OtlpEndpoint, "err", err.Error())
			return fallback, noShutdown
		}
		return obs, shutdown
	}
	return obsstdout.New(cfg.ServiceName), noShutdown
}

// PickClock returns the system clock.
func PickClock() ports.Clock {
	return clocksystem.New()
}

// PickVcs selects a VcsSource.
func PickVcs(cfg schemas.VcsConfig, _ ports.SecretsProvider) (ports.VcsSource, error) {
	switch cfg.Provider {
	case "github":
		return vcsgithub.New(cfg)
	case "memory":
		return nil, fmt.Errorf("the memory vcs lives in internal/testing/fakes; use the harness for tests")
	}
	return nil, fmt.Errorf("unknown vcs.provider: %q", cfg.Provider)
}

// PickLlm selects an LlmGateway.
func PickLlm(cfg schemas.LlmConfig, _ ports.SecretsProvider, _ ports.Obs) (ports.LlmGateway, error) {
	switch cfg.Provider {
	case "litellm":
		return llmlitellm.New(cfg)
	case "fake":
		return nil, fmt.Errorf("the fake LLM lives in internal/testing/fakes; use the harness for tests")
	}
	return nil, fmt.Errorf("unknown llm.provider: %q", cfg.Provider)
}

// PickParser returns the configured parser registry. Only one
// implementation today.
func PickParser() ports.ParserRegistry {
	return parsertreesitter.New()
}

// PickStores selects the seven store sub-ports.
func PickStores(ctx context.Context, cfg schemas.StoreConfig, _ ports.Obs) (Stores, error) {
	switch cfg.Type {
	case "postgres":
		if cfg.PostgresURL == "" {
			return Stores{}, fmt.Errorf("store.postgres_url is required")
		}
		pool, err := storepostgres.NewPool(ctx, cfg.PostgresURL)
		if err != nil {
			return Stores{}, err
		}
		s := storepostgres.NewStores(pool)
		return Stores{
			Repos:          s.Repos,
			CodeChunks:     s.CodeChunks,
			Comments:       s.Comments,
			Rules:          s.Rules,
			PrRuns:         s.PrRuns,
			Feedback:       s.Feedback,
			CostCaps:       s.CostCaps,
			EmbeddingCache: s.EmbeddingCache,
			Settings:       s.Settings,
			Context:        s.Context,
			RawHandle:      pool,
			Close:          s.Close,
		}, nil
	case "memory":
		return Stores{}, fmt.Errorf("memory stores live in internal/testing/fakes; use the harness for tests")
	}
	return Stores{}, fmt.Errorf("unknown store.type: %q", cfg.Type)
}

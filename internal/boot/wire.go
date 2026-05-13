// Package boot picks adapter implementations based on the loaded config
// and wires them into use cases. It is the only package that imports
// from internal/adapters, keeping the dependency direction clean.
package boot

import (
	"context"
	"fmt"

	"codereviewer/internal/adapters/busmem"
	"codereviewer/internal/adapters/clocksystem"
	"codereviewer/internal/adapters/obsstdout"
	"codereviewer/internal/adapters/secretsenv"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

// Stores bundles the seven store sub-ports. Adapters that back all of
// them with one connection (e.g. storepostgres) return all seven at once.
type Stores struct {
	CodeChunks     store.CodeChunkStore
	Comments       store.CommentStore
	Rules          store.RuleStore
	PrRuns         store.PrRunStore
	Feedback       store.FeedbackStore
	CostCaps       store.CostCapStore
	EmbeddingCache store.EmbeddingCache
}

// PickBus selects a MessageBus implementation.
func PickBus(cfg schemas.MessageBusConfig, _ ports.Obs) (ports.MessageBus, error) {
	switch cfg.Type {
	case "memory":
		return busmem.New(), nil
	case "sqs":
		return nil, fmt.Errorf("bussqs adapter not yet implemented (slice 1)")
	case "nats":
		return nil, fmt.Errorf("busnats adapter not yet implemented (slice 1)")
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

// PickObservability selects an Obs bundle. Slice 0 always returns
// obsstdout; the otel sink wiring lands in slice 4.
func PickObservability(cfg schemas.ObservabilityConfig) ports.Obs {
	return obsstdout.New(cfg.ServiceName)
}

// PickClock returns the system clock. Tests construct a fake directly.
func PickClock() ports.Clock {
	return clocksystem.New()
}

// PickVcs selects a VcsSource. Slice 0 only knows the testing fake,
// which is constructed inside the harness, not here.
func PickVcs(cfg schemas.VcsConfig, _ ports.SecretsProvider) (ports.VcsSource, error) {
	switch cfg.Provider {
	case "github":
		return nil, fmt.Errorf("vcsgithub adapter not yet implemented (slice 1)")
	case "memory":
		return nil, fmt.Errorf("the memory vcs lives in internal/testing/fakes; use the harness for tests")
	}
	return nil, fmt.Errorf("unknown vcs.provider: %q", cfg.Provider)
}

// PickLlm selects an LlmGateway. Slice 0 only knows the testing fake,
// constructed inside the harness.
func PickLlm(cfg schemas.LlmConfig, _ ports.SecretsProvider, _ ports.Obs) (ports.LlmGateway, error) {
	switch cfg.Provider {
	case "litellm":
		return nil, fmt.Errorf("llmlitellm adapter not yet implemented (slice 1)")
	case "fake":
		return nil, fmt.Errorf("the fake LLM lives in internal/testing/fakes; use the harness for tests")
	}
	return nil, fmt.Errorf("unknown llm.provider: %q", cfg.Provider)
}

// PickStores selects the seven store sub-ports. Slice 0 has none of the
// production adapters yet; tests use the in-memory fakes via the harness.
func PickStores(_ context.Context, cfg schemas.StoreConfig, _ ports.Obs) (Stores, error) {
	switch cfg.Type {
	case "postgres":
		return Stores{}, fmt.Errorf("storepostgres adapter not yet implemented (slice 1)")
	case "memory":
		return Stores{}, fmt.Errorf("memory stores live in internal/testing/fakes; use the harness for tests")
	}
	return Stores{}, fmt.Errorf("unknown store.type: %q", cfg.Type)
}

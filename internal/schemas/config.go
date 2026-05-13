// Package schemas defines wire-format types: configuration, bus
// messages, LLM output, and webhook payloads. These are the types that
// cross trust boundaries — anything coming from the outside world or
// going to durable storage gets validated here.
package schemas

import (
	"fmt"
	"strings"
)

// Config is the top-level TOML structure. Environment variables of the
// form ${NAME} in string values are expanded by the config loader.
type Config struct {
	Vcs           VcsConfig           `toml:"vcs"`
	MessageBus    MessageBusConfig    `toml:"message_bus"`
	Store         StoreConfig         `toml:"store"`
	Llm           LlmConfig           `toml:"llm"`
	Secrets       SecretsConfig       `toml:"secrets"`
	Observability ObservabilityConfig `toml:"observability"`
	Cost          CostConfig          `toml:"cost"`
	Rules         RulesConfig         `toml:"rules"`
}

// VcsConfig configures the version-control adapter.
type VcsConfig struct {
	Provider      string `toml:"provider"` // github | memory
	AppId         string `toml:"app_id"`
	PrivateKey    string `toml:"private_key"`
	WebhookSecret string `toml:"webhook_secret"`
}

// MessageBusConfig configures the message bus adapter.
type MessageBusConfig struct {
	Type             string `toml:"type"` // sqs | nats | memory
	ReviewQueueURL   string `toml:"review_queue_url"`
	IndexQueueURL    string `toml:"index_queue_url"`
	FeedbackQueueURL string `toml:"feedback_queue_url"`
	BackfillQueueURL string `toml:"backfill_queue_url"`
}

// StoreConfig configures persistence. Memory is for tests; Postgres in production.
type StoreConfig struct {
	Type        string `toml:"type"` // postgres | memory
	PostgresURL string `toml:"postgres_url"`
}

// LlmConfig configures the LLM gateway adapter. The pilot adapter targets
// LiteLLM at GatewayURL; PrimaryModelURL/FallbackModelURL are passed through
// to LiteLLM's routing config.
type LlmConfig struct {
	Provider         string `toml:"provider"` // litellm | fake
	GatewayURL       string `toml:"gateway_url"`
	PrimaryModelURL  string `toml:"primary_model_url"`
	FallbackModelURL string `toml:"fallback_model_url"`
	EmbeddingsURL    string `toml:"embeddings_url"`
	APIKey           string `toml:"api_key"`
	PerPrTokenCap    int    `toml:"per_pr_token_cap"`
}

// SecretsConfig selects the secrets provider.
type SecretsConfig struct {
	Provider string `toml:"provider"` // env | aws | vault
}

// ObservabilityConfig configures the OTel exporter.
type ObservabilityConfig struct {
	Sink         string `toml:"sink"` // stdout | otel
	OtlpEndpoint string `toml:"otlp_endpoint"`
	ServiceName  string `toml:"service_name"`
}

// CostConfig holds system-wide cost defaults. Per-repo overrides live in
// the cost_caps DB table.
type CostConfig struct {
	DailyUsdCapDefault float64 `toml:"daily_usd_cap_default"`
}

// RulesConfig points the rules-sync pipeline at an external git repo.
type RulesConfig struct {
	GitURL string `toml:"git_url"`
	Branch string `toml:"branch"`
}

// Validate checks that selected providers are known and required fields
// are populated for the chosen provider. It does NOT verify external
// reachability — that's the adapter's job at boot.
func (c *Config) Validate() error {
	if err := validateOneOf("vcs.provider", c.Vcs.Provider, "github", "memory"); err != nil {
		return err
	}
	if err := validateOneOf("message_bus.type", c.MessageBus.Type, "sqs", "nats", "memory"); err != nil {
		return err
	}
	if err := validateOneOf("store.type", c.Store.Type, "postgres", "memory"); err != nil {
		return err
	}
	if err := validateOneOf("llm.provider", c.Llm.Provider, "litellm", "fake"); err != nil {
		return err
	}
	if err := validateOneOf("secrets.provider", c.Secrets.Provider, "env", "aws", "vault"); err != nil {
		return err
	}
	if err := validateOneOf("observability.sink", c.Observability.Sink, "stdout", "otel"); err != nil {
		return err
	}
	if c.Llm.PerPrTokenCap <= 0 {
		c.Llm.PerPrTokenCap = 30000
	}
	if c.Cost.DailyUsdCapDefault <= 0 {
		c.Cost.DailyUsdCapDefault = 5.00
	}
	return nil
}

func validateOneOf(field, value string, allowed ...string) error {
	for _, a := range allowed {
		if value == a {
			return nil
		}
	}
	return fmt.Errorf("config: %s=%q is not one of [%s]", field, value, strings.Join(allowed, ", "))
}

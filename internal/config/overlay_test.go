package config_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/require"

	"codereviewer/internal/config"
	"codereviewer/internal/schemas"
	"codereviewer/internal/testing/fakes"
)

func baseConfig() *schemas.Config {
	c := &schemas.Config{
		Vcs:        schemas.VcsConfig{Provider: "github"},
		MessageBus: schemas.MessageBusConfig{Type: "nats", ReviewQueueURL: "nats://localhost:4222"},
		Store:      schemas.StoreConfig{Type: "postgres", PostgresURL: "postgres://x"},
		Llm: schemas.LlmConfig{
			Provider:      "litellm",
			GatewayURL:    "http://litellm:4000",
			PerPrTokenCap: 30000,
		},
		Secrets:       schemas.SecretsConfig{Provider: "env"},
		Observability: schemas.ObservabilityConfig{Sink: "stdout", ServiceName: "test"},
		Cost:          schemas.CostConfig{DailyUsdCapDefault: 5.0},
		Rules:         schemas.RulesConfig{GitURL: "https://toml-rules", Branch: "main"},
		Gateway:       schemas.GatewayConfig{ListenAddr: ":8080"},
		Tenant:        schemas.TenantConfig{Id: "default-tenant", Name: "default"},
	}
	require.NoError(nil, c.Validate())
	return c
}

func TestApplyOverlay_OverridesAndPreserves(t *testing.T) {
	cfg := baseConfig()
	settings := fakes.NewSettingsStore()
	ctx := context.Background()

	// Override rules.git_url and one numeric.
	require.NoError(t, settings.Set(ctx, "rules.git_url", "https://overridden", "admin"))
	require.NoError(t, settings.Set(ctx, "cost.daily_usd_cap_default", "12.5", "admin"))
	// A non-overlay key MUST be ignored (gateway.listen_addr is not in the list).
	require.NoError(t, settings.Set(ctx, "gateway.listen_addr", ":9999", "admin"))

	require.NoError(t, config.ApplyOverlay(ctx, cfg, settings))
	require.Equal(t, "https://overridden", cfg.Rules.GitURL)
	require.Equal(t, 12.5, cfg.Cost.DailyUsdCapDefault)
	require.Equal(t, ":8080", cfg.Gateway.ListenAddr, "non-overlay key must not affect cfg")
	// Untouched value stays put.
	require.Equal(t, "main", cfg.Rules.Branch)
	require.Equal(t, 30000, cfg.Llm.PerPrTokenCap)
}

func TestApplyOverlay_BadNumberFails(t *testing.T) {
	cfg := baseConfig()
	settings := fakes.NewSettingsStore()
	require.NoError(t, settings.Set(context.Background(),
		"llm.per_pr_token_cap", "not-an-int", "admin"))
	err := config.ApplyOverlay(context.Background(), cfg, settings)
	require.Error(t, err)
}

func TestApplyOverlay_ValidatesAfter(t *testing.T) {
	cfg := baseConfig()
	settings := fakes.NewSettingsStore()
	require.NoError(t, settings.Set(context.Background(),
		"observability.sink", "telepathy", "admin"))
	err := config.ApplyOverlay(context.Background(), cfg, settings)
	require.Error(t, err, "validate must reject unknown sink")
}

func TestApplyOverlay_EnvExpansion(t *testing.T) {
	cfg := baseConfig()
	settings := fakes.NewSettingsStore()
	ctx := context.Background()

	// Set OTLP endpoint as an env-var reference; the actual value comes
	// from the process env at apply time.
	t.Setenv("TEST_OTEL_ENDPOINT", "otel.prod.example:4318")
	require.NoError(t, settings.Set(ctx, "observability.otlp_endpoint", "${TEST_OTEL_ENDPOINT}", "admin"))
	require.NoError(t, settings.Set(ctx, "rules.git_url", "${TEST_RULES_URL}", "admin"))
	// TEST_RULES_URL is not set — should expand to empty, leaving the field empty.

	require.NoError(t, config.ApplyOverlay(ctx, cfg, settings))
	require.Equal(t, "otel.prod.example:4318", cfg.Observability.OtlpEndpoint)
	require.Equal(t, "", cfg.Rules.GitURL)
}

func TestApplyOverlay_NilStoreIsNoOp(t *testing.T) {
	cfg := baseConfig()
	orig := cfg.Rules.GitURL
	require.NoError(t, config.ApplyOverlay(context.Background(), cfg, nil))
	require.Equal(t, orig, cfg.Rules.GitURL)
}

func TestReadCurrent_KnownKeys(t *testing.T) {
	cfg := baseConfig()
	require.Equal(t, "https://toml-rules", config.ReadCurrent(cfg, "rules.git_url"))
	require.Equal(t, "30000", config.ReadCurrent(cfg, "llm.per_pr_token_cap"))
	require.Equal(t, "5", config.ReadCurrent(cfg, "cost.daily_usd_cap_default"))
	require.Equal(t, "", config.ReadCurrent(cfg, "not-a-real-key"))
}

func TestOverlayKeys_CoveredByApply(t *testing.T) {
	// Every key in OverlayKeys must round-trip through applyOne — guard
	// against adding a key to the list but forgetting the switch arm.
	cfg := baseConfig()
	settings := fakes.NewSettingsStore()
	ctx := context.Background()
	for _, k := range config.OverlayKeys {
		v := "x"
		// Numeric keys need parseable values.
		switch k {
		case "llm.per_pr_token_cap":
			v = "42"
		case "cost.daily_usd_cap_default":
			v = "1.5"
		case "observability.sink":
			v = "stdout"
		}
		require.NoError(t, settings.Set(ctx, k, v, "test"))
	}
	require.NoError(t, config.ApplyOverlay(ctx, cfg, settings))
}

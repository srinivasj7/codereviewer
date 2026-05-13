package config

import (
	"context"
	"fmt"
	"strconv"

	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

// OverlayKeys lists the settings keys that the admin UI may edit at
// runtime. They are dotted to match the TOML structure they shadow.
// Anything not in this list is TOML-only (bootstrap concerns: DB URL,
// secrets provider, bus URLs, listen addr, vcs credentials).
//
// Adding a key: list it here, add a case in ApplyOverlay, and surface
// it in the admin UI's settings form.
var OverlayKeys = []string{
	"rules.git_url",
	"rules.branch",
	"tenant.id",
	"tenant.name",
	"cost.daily_usd_cap_default",
	"llm.primary_model_url",
	"llm.fallback_model_url",
	"llm.embeddings_url",
	"llm.per_pr_token_cap",
	"observability.sink",
	"observability.otlp_endpoint",
	"observability.service_name",
}

// IsOverlayKey reports whether key is in the runtime-tunable set.
func IsOverlayKey(key string) bool {
	for _, k := range OverlayKeys {
		if k == key {
			return true
		}
	}
	return false
}

// ApplyOverlay reads every OverlayKey from the SettingsStore and writes
// each present value into cfg. Empty / absent settings leave the TOML
// default in place. Returns the same config (mutated) for chaining.
//
// String values are passed through ExpandEnv so an overlay value of
// `${OTEL_ENDPOINT}` resolves per-environment — useful when the same
// export TOML moves between docker-compose (where `otel-collector` is
// a service hostname) and EC2 (where it isn't).
//
// Parse failures on numeric/boolean overlays log nothing here — they
// surface as the original TOML value plus an error returned to the
// caller, so admins fixing a typo see it immediately.
func ApplyOverlay(ctx context.Context, cfg *schemas.Config, settings store.SettingsStore) error {
	if settings == nil {
		return nil
	}
	all, err := settings.GetAll(ctx)
	if err != nil {
		return fmt.Errorf("read overlay: %w", err)
	}
	for _, s := range all {
		if !IsOverlayKey(s.Key) {
			continue
		}
		v := ExpandEnv(s.Value)
		if err := applyOne(cfg, s.Key, v); err != nil {
			return fmt.Errorf("apply overlay %s=%q: %w", s.Key, v, err)
		}
	}
	return cfg.Validate()
}

func applyOne(cfg *schemas.Config, key, value string) error {
	switch key {
	case "rules.git_url":
		cfg.Rules.GitURL = value
	case "rules.branch":
		cfg.Rules.Branch = value
	case "tenant.id":
		cfg.Tenant.Id = value
	case "tenant.name":
		cfg.Tenant.Name = value
	case "cost.daily_usd_cap_default":
		v, err := strconv.ParseFloat(value, 64)
		if err != nil {
			return fmt.Errorf("not a number: %w", err)
		}
		cfg.Cost.DailyUsdCapDefault = v
	case "llm.primary_model_url":
		cfg.Llm.PrimaryModelURL = value
	case "llm.fallback_model_url":
		cfg.Llm.FallbackModelURL = value
	case "llm.embeddings_url":
		cfg.Llm.EmbeddingsURL = value
	case "llm.per_pr_token_cap":
		v, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("not an int: %w", err)
		}
		cfg.Llm.PerPrTokenCap = v
	case "observability.sink":
		cfg.Observability.Sink = value
	case "observability.otlp_endpoint":
		cfg.Observability.OtlpEndpoint = value
	case "observability.service_name":
		cfg.Observability.ServiceName = value
	default:
		// Listed in OverlayKeys but not handled here — programmer error.
		return fmt.Errorf("unhandled overlay key")
	}
	return nil
}

// ReadCurrent returns the current effective value for an overlay key,
// reading from cfg (which already has overlays applied). The admin UI
// uses this to render the settings form with current values.
func ReadCurrent(cfg *schemas.Config, key string) string {
	switch key {
	case "rules.git_url":
		return cfg.Rules.GitURL
	case "rules.branch":
		return cfg.Rules.Branch
	case "tenant.id":
		return cfg.Tenant.Id
	case "tenant.name":
		return cfg.Tenant.Name
	case "cost.daily_usd_cap_default":
		return strconv.FormatFloat(cfg.Cost.DailyUsdCapDefault, 'f', -1, 64)
	case "llm.primary_model_url":
		return cfg.Llm.PrimaryModelURL
	case "llm.fallback_model_url":
		return cfg.Llm.FallbackModelURL
	case "llm.embeddings_url":
		return cfg.Llm.EmbeddingsURL
	case "llm.per_pr_token_cap":
		return strconv.Itoa(cfg.Llm.PerPrTokenCap)
	case "observability.sink":
		return cfg.Observability.Sink
	case "observability.otlp_endpoint":
		return cfg.Observability.OtlpEndpoint
	case "observability.service_name":
		return cfg.Observability.ServiceName
	}
	return ""
}

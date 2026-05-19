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
	"retention.pr_runs_days",
	"retention.feedback_events_days",
	"retention.pr_context_items_days",
	"retention.embedding_cache_max_rows",
	"retention.auto_export_max_files",
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

// RestartRequiredKeys are overlay keys whose consumers capture them at
// boot, so a worker restart is still required after a UI save before
// the new value takes effect. Everything else in OverlayKeys is
// hot-reloaded by boot.Reloader.
//
// The boundary is honest, not aspirational: live-reloading the LLM
// model URL would require recreating the openai client; live-reloading
// the OTel sink would require tearing down the exporter pipeline.
// Both are non-trivial and rare-operation changes — restart is fine.
var RestartRequiredKeys = []string{
	"rules.git_url",
	"rules.branch",
	"tenant.id",
	"tenant.name",
	"observability.sink",
	"observability.otlp_endpoint",
	"observability.service_name",
}

// IsRestartRequired reports whether changing key needs a worker restart.
func IsRestartRequired(key string) bool {
	for _, k := range RestartRequiredKeys {
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
	case "retention.pr_runs_days":
		return setIntOverlay(value, &cfg.Retention.PrRunsDays)
	case "retention.feedback_events_days":
		return setIntOverlay(value, &cfg.Retention.FeedbackEventsDays)
	case "retention.pr_context_items_days":
		return setIntOverlay(value, &cfg.Retention.PrContextItemsDays)
	case "retention.embedding_cache_max_rows":
		return setIntOverlay(value, &cfg.Retention.EmbeddingCacheMaxRows)
	case "retention.auto_export_max_files":
		return setIntOverlay(value, &cfg.Retention.AutoExportMaxFiles)
	default:
		// Listed in OverlayKeys but not handled here — programmer error.
		return fmt.Errorf("unhandled overlay key")
	}
	return nil
}

func setIntOverlay(value string, dest *int) error {
	v, err := strconv.Atoi(value)
	if err != nil {
		return fmt.Errorf("not an int: %w", err)
	}
	*dest = v
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
	case "retention.pr_runs_days":
		return strconv.Itoa(cfg.Retention.PrRunsDays)
	case "retention.feedback_events_days":
		return strconv.Itoa(cfg.Retention.FeedbackEventsDays)
	case "retention.pr_context_items_days":
		return strconv.Itoa(cfg.Retention.PrContextItemsDays)
	case "retention.embedding_cache_max_rows":
		return strconv.Itoa(cfg.Retention.EmbeddingCacheMaxRows)
	case "retention.auto_export_max_files":
		return strconv.Itoa(cfg.Retention.AutoExportMaxFiles)
	}
	return ""
}

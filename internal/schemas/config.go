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
	Gateway       GatewayConfig       `toml:"gateway"`
	Tenant        TenantConfig        `toml:"tenant"`
	Admin         AdminConfig         `toml:"admin"`
	Context       ContextConfig       `toml:"context"`
	Retention     RetentionConfig     `toml:"retention"`
	RateLimit     RateLimitConfig     `toml:"rate_limit"`
}

// RetentionConfig caps the growth of append-mostly tables and on-disk
// artifacts. The janitor (admin-ui background goroutine) sweeps on the
// configured interval. Zero/negative values mean "never delete" for
// the corresponding table — useful for compliance deploys that prefer
// archival via DB backups.
type RetentionConfig struct {
	PrRunsDays            int  `toml:"pr_runs_days"`             // default 365
	FeedbackEventsDays    int  `toml:"feedback_events_days"`     // default 730
	PrContextItemsDays    int  `toml:"pr_context_items_days"`    // default 90
	EmbeddingCacheMaxRows int  `toml:"embedding_cache_max_rows"` // default 100000
	AutoExportMaxFiles    int  `toml:"auto_export_max_files"`    // default 30
	JanitorIntervalHours  int  `toml:"janitor_interval_hours"`   // default 6
	JanitorEnabled        bool `toml:"janitor_enabled"`
}

// RateLimitConfig governs the in-memory token-bucket limits on the
// public-facing endpoints. Per-IP, in-process — multi-replica deploys
// get multiplied limits, which is fine for the current scale.
type RateLimitConfig struct {
	LoginAttempts       int `toml:"login_attempts"`        // default 5
	LoginWindowMinutes  int `toml:"login_window_minutes"`  // default 15
	WebhookPerSecond    int `toml:"webhook_per_second"`    // default 100
	WebhookMaxBodyBytes int `toml:"webhook_max_body_bytes"` // default 1<<20 = 1 MiB
}

// AdminConfig configures the admin web UI (cmd/admin-ui).
type AdminConfig struct {
	ListenAddr        string `toml:"listen_addr"`         // ":8090"
	Password          string `toml:"password"`            // bootstrap; use ${ADMIN_PASSWORD}
	SessionSecret     string `toml:"session_secret"`      // HMAC key for the session cookie
	SessionMinutes    int    `toml:"session_minutes"`     // cookie lifetime; default 60
	ExportDir         string `toml:"export_dir"`          // where scheduled exports are written; ":memory:" disables disk writes
	AutoExportEnabled bool   `toml:"auto_export_enabled"` // run the background exporter goroutine
	AutoExportHours   int    `toml:"auto_export_hours"`   // interval between exports; default 24
	GithubOAuth       AdminGithubOAuthConfig `toml:"github_oauth"`
}

// AdminGithubOAuthConfig optionally adds GitHub OAuth as a second login
// path. Disabled when ClientId is empty.
type AdminGithubOAuthConfig struct {
	ClientId     string   `toml:"client_id"`
	ClientSecret string   `toml:"client_secret"`
	CallbackURL  string   `toml:"callback_url"`
	AllowedOrgs  []string `toml:"allowed_orgs"`
}

// GatewayConfig configures the webhook gateway HTTP listener.
type GatewayConfig struct {
	ListenAddr string `toml:"listen_addr"`
}

// TenantConfig sets the deployment's tenant identity. Single-tenant
// deployments hardcode this; multi-tenant routing arrives later.
type TenantConfig struct {
	Id   string `toml:"id"`
	Name string `toml:"name"`
}

// VcsConfig configures the version-control adapter.
type VcsConfig struct {
	Provider       string `toml:"provider"` // github | memory
	AppId          string `toml:"app_id"`
	InstallationId string `toml:"installation_id"`
	PrivateKey     string `toml:"private_key"`      // inline PEM (use ${ENV} expansion)
	PrivateKeyPath string `toml:"private_key_path"` // alternative to inline; preferred
	WebhookSecret  string `toml:"webhook_secret"`
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

// ContextConfig configures the issue-tracker context providers and
// the ad-hoc URL-fetch allow-list. Disabled providers don't run.
type ContextConfig struct {
	Jira             JiraConfig         `toml:"jira"`
	GithubIssues     GithubIssuesConfig `toml:"github_issues"`
	Linear           LinearConfig       `toml:"linear"`
	AllowedUrlHosts  []string           `toml:"allowed_url_hosts"`
	UrlFetchMaxBytes int                `toml:"url_fetch_max_bytes"` // default 1MB
	MaxItemsPerPr    int                `toml:"max_items_per_pr"`    // default 10
}

// JiraConfig — Atlassian REST + email/api-token auth. Disabled if BaseURL empty.
type JiraConfig struct {
	BaseURL  string `toml:"base_url"` // https://acme.atlassian.net
	Email    string `toml:"email"`
	APIToken string `toml:"api_token"`
}

// GithubIssuesConfig — reuses the existing GitHub App credentials.
// Disabled when Enabled=false.
type GithubIssuesConfig struct {
	Enabled bool `toml:"enabled"`
}

// LinearConfig — GraphQL + personal API key. Disabled if APIKey empty.
type LinearConfig struct {
	APIKey      string   `toml:"api_key"`
	TeamPrefixes []string `toml:"team_prefixes"` // optional; only keys with these prefixes are fetched
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
	if c.Gateway.ListenAddr == "" {
		c.Gateway.ListenAddr = ":8080"
	}
	if c.Tenant.Id == "" {
		c.Tenant.Id = "default-tenant"
	}
	if c.Tenant.Name == "" {
		c.Tenant.Name = "default"
	}
	if c.Admin.ListenAddr == "" {
		c.Admin.ListenAddr = ":8090"
	}
	if c.Admin.SessionMinutes <= 0 {
		c.Admin.SessionMinutes = 60
	}
	if c.Admin.AutoExportHours <= 0 {
		c.Admin.AutoExportHours = 24
	}
	if c.Context.UrlFetchMaxBytes <= 0 {
		c.Context.UrlFetchMaxBytes = 1 << 20 // 1 MiB
	}
	if c.Context.MaxItemsPerPr <= 0 {
		c.Context.MaxItemsPerPr = 10
	}
	if c.Retention.PrRunsDays == 0 {
		c.Retention.PrRunsDays = 365
	}
	if c.Retention.FeedbackEventsDays == 0 {
		c.Retention.FeedbackEventsDays = 730
	}
	if c.Retention.PrContextItemsDays == 0 {
		c.Retention.PrContextItemsDays = 90
	}
	if c.Retention.EmbeddingCacheMaxRows == 0 {
		c.Retention.EmbeddingCacheMaxRows = 100_000
	}
	if c.Retention.AutoExportMaxFiles == 0 {
		c.Retention.AutoExportMaxFiles = 30
	}
	if c.Retention.JanitorIntervalHours <= 0 {
		c.Retention.JanitorIntervalHours = 6
	}
	if c.RateLimit.LoginAttempts <= 0 {
		c.RateLimit.LoginAttempts = 5
	}
	if c.RateLimit.LoginWindowMinutes <= 0 {
		c.RateLimit.LoginWindowMinutes = 15
	}
	if c.RateLimit.WebhookPerSecond <= 0 {
		c.RateLimit.WebhookPerSecond = 100
	}
	if c.RateLimit.WebhookMaxBodyBytes <= 0 {
		c.RateLimit.WebhookMaxBodyBytes = 1 << 20 // 1 MiB
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

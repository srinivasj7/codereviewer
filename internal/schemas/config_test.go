package schemas

import (
	"strings"
	"testing"

	"github.com/pelletier/go-toml/v2"
)

func TestVcsConfig_ActiveProviders(t *testing.T) {
	cases := []struct {
		name string
		cfg  VcsConfig
		want []string
	}{
		{
			name: "plural wins when both set",
			cfg:  VcsConfig{Provider: "github", Providers: []string{"github", "bitbucket"}},
			want: []string{"github", "bitbucket"},
		},
		{
			name: "singular fallback",
			cfg:  VcsConfig{Provider: "bitbucket"},
			want: []string{"bitbucket"},
		},
		{
			name: "neither set returns empty",
			cfg:  VcsConfig{},
			want: nil,
		},
		{
			name: "plural only",
			cfg:  VcsConfig{Providers: []string{"github"}},
			want: []string{"github"},
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := tc.cfg.ActiveProviders()
			if len(got) != len(tc.want) {
				t.Fatalf("len(got)=%d, want %d (got=%v)", len(got), len(tc.want), got)
			}
			for i := range got {
				if got[i] != tc.want[i] {
					t.Errorf("at %d: got %q, want %q", i, got[i], tc.want[i])
				}
			}
		})
	}
}

// validConfig builds the minimum Config that passes Validate() so each
// test below can mutate exactly the field it cares about.
func validConfig() *Config {
	return &Config{
		Vcs:           VcsConfig{Provider: "github"},
		MessageBus:    MessageBusConfig{Type: "memory"},
		Store:         StoreConfig{Type: "memory"},
		Llm:           LlmConfig{Provider: "fake"},
		Secrets:       SecretsConfig{Provider: "env"},
		Observability: ObservabilityConfig{Sink: "stdout"},
	}
}

func TestConfig_Validate_RejectsEmptyVcs(t *testing.T) {
	c := validConfig()
	c.Vcs = VcsConfig{}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error for empty vcs")
	}
	if !strings.Contains(err.Error(), "vcs") {
		t.Errorf("error should mention vcs: %v", err)
	}
}

func TestConfig_Validate_RejectsUnknownProvider(t *testing.T) {
	c := validConfig()
	c.Vcs = VcsConfig{Provider: "gitlab"}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error for unknown provider")
	}
}

func TestConfig_Validate_AcceptsMultiProvider(t *testing.T) {
	c := validConfig()
	c.Vcs = VcsConfig{Providers: []string{"github", "bitbucket"}}
	if err := c.Validate(); err != nil {
		t.Errorf("multi-provider config should validate: %v", err)
	}
}

func TestConfig_Validate_RejectsUnknownInMultiProvider(t *testing.T) {
	c := validConfig()
	c.Vcs = VcsConfig{Providers: []string{"github", "perforce"}}
	err := c.Validate()
	if err == nil {
		t.Fatal("expected error when one provider in the list is unknown")
	}
}

// TestVcsConfig_TOMLParse_NestedBlocks pins the wire format for the
// per-provider config blocks. Regressions here would silently route
// both adapters back to a shared webhook_secret — the exact bug the
// nested layout was introduced to prevent.
func TestVcsConfig_TOMLParse_NestedBlocks(t *testing.T) {
	src := `
providers = ["github", "bitbucket"]

[github]
app_id           = "12345"
installation_id  = "67890"
private_key_path = "/etc/key.pem"
webhook_secret   = "gh-secret"

[bitbucket]
client_id      = "bb-client"
client_secret  = "bb-secret"
workspace      = "acme"
webhook_secret = "bb-webhook-secret"
`
	var v VcsConfig
	if err := toml.Unmarshal([]byte(src), &v); err != nil {
		t.Fatalf("toml unmarshal: %v", err)
	}

	if got, want := v.GitHub.AppId, "12345"; got != want {
		t.Errorf("GitHub.AppId: got %q, want %q", got, want)
	}
	if got, want := v.GitHub.WebhookSecret, "gh-secret"; got != want {
		t.Errorf("GitHub.WebhookSecret: got %q, want %q", got, want)
	}
	if got, want := v.Bitbucket.ClientId, "bb-client"; got != want {
		t.Errorf("Bitbucket.ClientId: got %q, want %q", got, want)
	}
	if got, want := v.Bitbucket.WebhookSecret, "bb-webhook-secret"; got != want {
		t.Errorf("Bitbucket.WebhookSecret: got %q, want %q", got, want)
	}
	// The shared field is gone — the github and bitbucket secrets must
	// not be the same value if both blocks set distinct strings.
	if v.GitHub.WebhookSecret == v.Bitbucket.WebhookSecret {
		t.Errorf("per-provider webhook secrets must round-trip distinctly")
	}
}

// TestVcsConfig_TOMLParse_SingleProvider — a github-only deploy should
// only need [vcs.github]. The bitbucket block stays zero-valued.
func TestVcsConfig_TOMLParse_SingleProvider(t *testing.T) {
	src := `
provider = "github"

[github]
app_id          = "1"
installation_id = "2"
private_key     = "PEM"
webhook_secret  = "only-github"
`
	var v VcsConfig
	if err := toml.Unmarshal([]byte(src), &v); err != nil {
		t.Fatalf("toml unmarshal: %v", err)
	}
	if v.GitHub.WebhookSecret != "only-github" {
		t.Errorf("GitHub webhook_secret missing: got %q", v.GitHub.WebhookSecret)
	}
	if v.Bitbucket != (BitbucketVcsConfig{}) {
		t.Errorf("expected zero-value Bitbucket block, got %+v", v.Bitbucket)
	}
}

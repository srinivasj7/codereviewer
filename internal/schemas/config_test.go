package schemas

import (
	"strings"
	"testing"
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

package boot_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codereviewer/internal/boot"
	"codereviewer/internal/ports"
	"codereviewer/internal/schemas"
	"codereviewer/internal/testing/fakes"
)

func validBootstrap() schemas.Config {
	return schemas.Config{
		Llm: schemas.LlmConfig{
			Provider:        "litellm",
			GatewayURL:      "http://litellm:4000",
			PrimaryModelURL: "primary",
			PerPrTokenCap:   30000,
		},
		Vcs:           schemas.VcsConfig{Provider: "memory"},
		Secrets:       schemas.SecretsConfig{Provider: "env"},
		Observability: schemas.ObservabilityConfig{Sink: "stdout"},
		Tenant:        schemas.TenantConfig{Id: "default-tenant"},
		Cost:          schemas.CostConfig{DailyUsdCapDefault: 5.00},
		MessageBus:    schemas.MessageBusConfig{Type: "memory"},
		Store:         schemas.StoreConfig{Type: "memory"},
	}
}

func TestReloader_PicksUpSettingsChange(t *testing.T) {
	settings := fakes.NewSettingsStore()
	bootstrap := validBootstrap()

	r, err := boot.NewReloader(bootstrap, settings, 50*time.Millisecond)
	require.NoError(t, err)

	assert.Equal(t, 30000, r.Current().Llm.PerPrTokenCap, "before reload: bootstrap value")

	require.NoError(t, settings.Set(context.Background(), "llm.per_pr_token_cap", "12345", "test"))

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx, noopLogger{})

	require.Eventually(t, func() bool {
		return r.Current().Llm.PerPrTokenCap == 12345
	}, 2*time.Second, 50*time.Millisecond, "reloader should pick up the overlay change")
}

func TestReloader_NoChangeKeepsSamePointer(t *testing.T) {
	settings := fakes.NewSettingsStore()
	bootstrap := validBootstrap()
	r, err := boot.NewReloader(bootstrap, settings, 50*time.Millisecond)
	require.NoError(t, err)

	first := r.Current()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go r.Run(ctx, noopLogger{})

	time.Sleep(200 * time.Millisecond)
	assert.Same(t, first, r.Current(), "with no settings change the snapshot pointer is reused")
}

type noopLogger struct{}

func (noopLogger) Info(string, ...any)  {}
func (noopLogger) Warn(string, ...any)  {}
func (noopLogger) Error(string, ...any) {}

var _ ports.Logger = noopLogger{}

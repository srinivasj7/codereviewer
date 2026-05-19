package boot

import (
	"context"
	"sync/atomic"
	"time"

	"codereviewer/internal/config"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

// Reloader keeps a live snapshot of the effective config (TOML bootstrap +
// app_settings overlay), refreshed on a polling interval. Long-running
// workers Current() to read live values for hot-reloadable settings.
//
// Not every overlay key is hot-reloadable: see config.LiveReloadKeys vs
// config.RestartRequiredKeys. Settings in the restart set are still
// observed by the polling loop, but the components that depend on them
// were constructed once at boot and won't pick them up — the admin UI
// surfaces this distinction in the save-flash.
type Reloader struct {
	bootstrap schemas.Config
	store     store.SettingsStore
	cur       atomic.Pointer[schemas.Config]
	interval  time.Duration
}

// NewReloader pre-applies the overlay against the bootstrap config and
// stores the first snapshot. Subsequent reloads happen via Run.
func NewReloader(bootstrap schemas.Config, st store.SettingsStore, interval time.Duration) (*Reloader, error) {
	if interval <= 0 {
		interval = 30 * time.Second
	}
	r := &Reloader{
		bootstrap: bootstrap,
		store:     st,
		interval:  interval,
	}
	cfg := bootstrap
	if err := config.ApplyOverlay(context.Background(), &cfg, st); err != nil {
		return nil, err
	}
	r.cur.Store(&cfg)
	return r, nil
}

// Current returns the most recent applied snapshot. The pointer is
// safe to read but not to mutate; if you need a private copy, deref
// and copy the struct.
func (r *Reloader) Current() *schemas.Config { return r.cur.Load() }

// Run polls the SettingsStore on the configured interval and replaces
// the current snapshot when the overlay produces a different config.
// Returns when ctx is canceled.
func (r *Reloader) Run(ctx context.Context, log ports.Logger) {
	t := time.NewTicker(r.interval)
	defer t.Stop()
	log.Info("settings reloader started", "interval_seconds", int(r.interval.Seconds()))
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			cfg := r.bootstrap
			if err := config.ApplyOverlay(ctx, &cfg, r.store); err != nil {
				log.Warn("settings reload failed", "err", err.Error())
				continue
			}
			prev := r.cur.Load()
			if prev != nil && configEqual(*prev, cfg) {
				continue
			}
			r.cur.Store(&cfg)
			log.Info("settings reloaded; live values updated")
		}
	}
}

// configEqual is a coarse comparison over the subset of fields that
// overlays may touch. Avoids re-publishing snapshots when nothing
// changed. We intentionally don't reflect-compare the whole struct —
// fields like *pgxpool.Pool (stored in some sub-configs) would either
// be nil or pointer-equal anyway.
func configEqual(a, b schemas.Config) bool {
	return a.Rules == b.Rules &&
		a.Tenant == b.Tenant &&
		a.Cost == b.Cost &&
		a.Llm.PrimaryModelURL == b.Llm.PrimaryModelURL &&
		a.Llm.FallbackModelURL == b.Llm.FallbackModelURL &&
		a.Llm.EmbeddingsURL == b.Llm.EmbeddingsURL &&
		a.Llm.PerPrTokenCap == b.Llm.PerPrTokenCap &&
		a.Llm.ChatTimeoutSec == b.Llm.ChatTimeoutSec &&
		a.Llm.EmbedTimeoutSec == b.Llm.EmbedTimeoutSec &&
		a.Observability == b.Observability &&
		a.Retention == b.Retention
}

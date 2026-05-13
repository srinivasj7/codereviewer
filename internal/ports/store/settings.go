package store

import (
	"context"
	"time"
)

// SettingsStore manages the app_settings row family. Values are stored
// as TEXT; callers parse to their declared type. The store is intended
// for runtime-tunable operator knobs (cost caps, model choice, rules
// URL, tenant display name, etc.) — bootstrap knobs (DB URL, bus
// transport, secrets provider) MUST stay in TOML so the admin UI can
// never lock itself out of the database it lives on.
type SettingsStore interface {
	// Get returns (value, true, nil) if the key exists; (_, false, nil)
	// if not. Callers fall back to their TOML default when found=false.
	Get(ctx context.Context, key string) (value string, found bool, err error)

	// GetAll returns every setting, oldest-updated first. Used by the
	// admin UI to render the settings page and by config export.
	GetAll(ctx context.Context) ([]Setting, error)

	// Set upserts the key/value pair. updatedBy is recorded for audit;
	// pass "system" for non-UI-initiated writes (boot seeding, scheduled
	// imports).
	Set(ctx context.Context, key, value, updatedBy string) error

	// Delete removes one key. Returns nil even if the key didn't exist —
	// the desired post-state is "key absent."
	Delete(ctx context.Context, key string) error
}

// Setting is one app_settings row.
type Setting struct {
	Key       string
	Value     string
	UpdatedAt time.Time
	UpdatedBy string
}

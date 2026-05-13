package store

import (
	"context"

	"codereviewer/internal/ports"
)

// RepoStore manages the repos and tenants tables. Auto-registration on
// first webhook keeps the audit trail meaningful and lets slice 3's
// backfill enumerate known repos.
type RepoStore interface {
	// EnsureExists inserts the repo (and its tenant if necessary).
	// Idempotent: calling with the same values is a no-op except for
	// the default_branch field, which is refreshed on each call so
	// renames are picked up automatically.
	EnsureExists(ctx context.Context, repo ports.RepoRef) error
	// Get returns the full RepoRef for an id, or found=false if absent.
	Get(ctx context.Context, repoId ports.RepoId) (ref ports.RepoRef, found bool, err error)
}

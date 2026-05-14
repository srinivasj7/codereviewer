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
	// ListByTenant returns all repos for a tenant, ordered by repo_id.
	// Used by the admin UI to enumerate repos for instruction-set
	// assignment.
	ListByTenant(ctx context.Context, tenant ports.TenantId) ([]ports.RepoRef, error)
	// SetEnabled flips repos.enabled. Disabled repos skip the review
	// pipeline; the gateway still ACKs webhooks so GitHub doesn't retry.
	SetEnabled(ctx context.Context, repoId ports.RepoId, enabled bool) error
	// Tombstone clears retrieval data for a disabled repo (code_chunks,
	// review_comments) per design §15. pr_runs and feedback_events are
	// kept for audit; the janitor's retention windows apply later.
	Tombstone(ctx context.Context, repoId ports.RepoId) error
}

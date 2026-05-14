package store

import (
	"context"
	"time"

	"codereviewer/internal/ports"
)

// PrRunStore manages pr_runs rows. Each row is the audit trail entry
// for one pipeline invocation. Begin is idempotent on IdempotencyKey;
// duplicate=true means the run already exists and the caller should
// short-circuit.
type PrRunStore interface {
	Begin(ctx context.Context, args BeginRun) (runId RunId, duplicate bool, err error)
	Finish(ctx context.Context, runId RunId, result RunResult) error
	GetRecent(ctx context.Context, repoId ports.RepoId, prNumber, limit int) ([]PrRun, error)
	// ListAcrossRepos returns the most recent runs across all repos,
	// newest first, capped at limit. Used by the admin UI's recent-runs
	// viewer.
	ListAcrossRepos(ctx context.Context, tenant ports.TenantId, limit int) ([]PrRun, error)
	// GetByRunId returns the single row for runId, or found=false.
	GetByRunId(ctx context.Context, runId RunId) (PrRun, bool, error)
	// DeleteBefore removes rows started_at < cutoff. Returns the row
	// count deleted. Used by the janitor; safe to call on a live system.
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

package store

import (
	"context"

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
}

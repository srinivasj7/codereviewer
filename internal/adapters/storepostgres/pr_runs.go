package storepostgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// PrRunStore is the Postgres implementation of store.PrRunStore.
type PrRunStore struct {
	pool *pgxpool.Pool
}

// Begin inserts a new pr_runs row keyed by IdempotencyKey. On conflict
// returns the existing run_id with duplicate=true so the pipeline can
// short-circuit.
func (s *PrRunStore) Begin(ctx context.Context, args store.BeginRun) (store.RunId, bool, error) {
	id := uuid.NewString()
	// First, try the insert.
	var insertedRunId string
	err := s.pool.QueryRow(ctx, `
INSERT INTO pr_runs (
  run_id, tenant_id, repo_id, pr_number, head_sha, trigger, status, idempotency_key, started_at
) VALUES ($1, $2, $3, $4, $5, $6, 'pending', $7, $8)
ON CONFLICT (idempotency_key) WHERE idempotency_key IS NOT NULL DO NOTHING
RETURNING run_id::text
`,
		id, string(args.Ref.TenantId), string(args.Ref.RepoId), args.Ref.PrNumber,
		args.Ref.HeadSha, string(args.Trigger), args.IdempotencyKey, args.StartedAt,
	).Scan(&insertedRunId)

	if err == nil {
		return store.RunId(insertedRunId), false, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return "", false, fmt.Errorf("begin run: %w", err)
	}

	// Conflict — fetch the existing run id.
	var existing string
	if err := s.pool.QueryRow(ctx,
		`SELECT run_id::text FROM pr_runs WHERE idempotency_key = $1`,
		args.IdempotencyKey,
	).Scan(&existing); err != nil {
		return "", false, fmt.Errorf("fetch existing run: %w", err)
	}
	return store.RunId(existing), true, nil
}

// Finish records the terminal state of a run.
func (s *PrRunStore) Finish(ctx context.Context, runId store.RunId, result store.RunResult) error {
	cmd, err := s.pool.Exec(ctx, `
UPDATE pr_runs SET
  status = $1, model_used = $2, tokens_in = $3, tokens_out = $4,
  cost_usd = $5, finished_at = $6
WHERE run_id = $7
`,
		string(result.Status), nullableText(result.ModelUsed),
		result.TokensIn, result.TokensOut, result.CostUsd, result.FinishedAt,
		string(runId),
	)
	if err != nil {
		return fmt.Errorf("finish run: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("run %s not found", runId)
	}
	return nil
}

// GetRecent returns runs for (repoId, prNumber) ordered by start time desc.
func (s *PrRunStore) GetRecent(ctx context.Context, repoId ports.RepoId, prNumber, limit int) ([]store.PrRun, error) {
	rows, err := s.pool.Query(ctx, `
SELECT run_id::text, tenant_id, repo_id, pr_number, head_sha, trigger,
       status, COALESCE(model_used,''), COALESCE(tokens_in,0), COALESCE(tokens_out,0),
       COALESCE(cost_usd,0)::float8, started_at, COALESCE(finished_at, started_at)
FROM pr_runs
WHERE repo_id = $1 AND pr_number = $2
ORDER BY started_at DESC
LIMIT $3
`, string(repoId), prNumber, limit)
	if err != nil {
		return nil, fmt.Errorf("get recent runs: %w", err)
	}
	defer rows.Close()

	var out []store.PrRun
	for rows.Next() {
		var r store.PrRun
		var tenant, repo, status, trigger string
		if err := rows.Scan(&r.RunId, &tenant, &repo, &r.Ref.PrNumber, &r.Ref.HeadSha, &trigger,
			&status, &r.ModelUsed, &r.TokensIn, &r.TokensOut, &r.CostUsd,
			&r.StartedAt, &r.FinishedAt); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		r.Ref.TenantId = ports.TenantId(tenant)
		r.Ref.RepoId = ports.RepoId(repo)
		r.Trigger = ports.Trigger(trigger)
		r.Status = store.RunStatus(status)
		out = append(out, r)
	}
	return out, rows.Err()
}

// ListAcrossRepos returns the most recent runs across all repos for a tenant.
func (s *PrRunStore) ListAcrossRepos(ctx context.Context, tenant ports.TenantId, limit int) ([]store.PrRun, error) {
	if limit <= 0 {
		limit = 50
	}
	rows, err := s.pool.Query(ctx, `
SELECT run_id::text, tenant_id, repo_id, pr_number, head_sha, trigger,
       status, COALESCE(model_used,''), COALESCE(tokens_in,0), COALESCE(tokens_out,0), COALESCE(cost_usd,0),
       started_at, COALESCE(finished_at, started_at), COALESCE(error,'')
FROM pr_runs
WHERE tenant_id = $1
ORDER BY started_at DESC
LIMIT $2
`, string(tenant), limit)
	if err != nil {
		return nil, fmt.Errorf("list runs: %w", err)
	}
	defer rows.Close()
	var out []store.PrRun
	for rows.Next() {
		var r store.PrRun
		var tenantStr, repo, status, trigger, errStr string
		if err := rows.Scan(&r.RunId, &tenantStr, &repo, &r.Ref.PrNumber, &r.Ref.HeadSha, &trigger,
			&status, &r.ModelUsed, &r.TokensIn, &r.TokensOut, &r.CostUsd,
			&r.StartedAt, &r.FinishedAt, &errStr); err != nil {
			return nil, fmt.Errorf("scan run: %w", err)
		}
		r.Ref.TenantId = ports.TenantId(tenantStr)
		r.Ref.RepoId = ports.RepoId(repo)
		r.Trigger = ports.Trigger(trigger)
		r.Status = store.RunStatus(status)
		r.Error = errStr
		out = append(out, r)
	}
	return out, rows.Err()
}

// GetByRunId returns one row by its id.
func (s *PrRunStore) GetByRunId(ctx context.Context, runId store.RunId) (store.PrRun, bool, error) {
	var r store.PrRun
	var tenant, repo, status, trigger, errStr string
	err := s.pool.QueryRow(ctx, `
SELECT run_id::text, tenant_id, repo_id, pr_number, head_sha, trigger,
       status, COALESCE(model_used,''), COALESCE(tokens_in,0), COALESCE(tokens_out,0), COALESCE(cost_usd,0),
       started_at, COALESCE(finished_at, started_at), COALESCE(error,'')
FROM pr_runs WHERE run_id = $1
`, string(runId)).Scan(&r.RunId, &tenant, &repo, &r.Ref.PrNumber, &r.Ref.HeadSha, &trigger,
		&status, &r.ModelUsed, &r.TokensIn, &r.TokensOut, &r.CostUsd,
		&r.StartedAt, &r.FinishedAt, &errStr)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.PrRun{}, false, nil
	}
	if err != nil {
		return store.PrRun{}, false, fmt.Errorf("get run: %w", err)
	}
	r.Ref.TenantId = ports.TenantId(tenant)
	r.Ref.RepoId = ports.RepoId(repo)
	r.Trigger = ports.Trigger(trigger)
	r.Status = store.RunStatus(status)
	r.Error = errStr
	return r, true, nil
}

// DeleteBefore removes rows started_at < cutoff.
func (s *PrRunStore) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM pr_runs WHERE started_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete pr_runs: %w", err)
	}
	return tag.RowsAffected(), nil
}

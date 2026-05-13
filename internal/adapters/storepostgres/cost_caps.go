package storepostgres

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// CostCapStore is the Postgres implementation of store.CostCapStore.
// Per-repo caps fall back to the per-tenant default (repo_id IS NULL),
// then to defaults from config.
type CostCapStore struct {
	pool                 *pgxpool.Pool
	defaultDailyUsdCap   float64
	defaultPerPrTokenCap int
}

// GetEffective returns the most-specific cap row, defaulting to in-memory defaults.
func (s *CostCapStore) GetEffective(ctx context.Context, tenantId ports.TenantId, repoId ports.RepoId) (store.CostCap, error) {
	var cap store.CostCap
	err := s.pool.QueryRow(ctx, `
SELECT daily_usd_cap::float8, per_pr_token_cap
FROM cost_caps
WHERE tenant_id = $1 AND repo_id = $2
`, string(tenantId), string(repoId)).Scan(&cap.DailyUsdCap, &cap.PerPrTokenCap)
	if err == nil {
		return cap, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return store.CostCap{}, fmt.Errorf("get cap (repo): %w", err)
	}

	err = s.pool.QueryRow(ctx, `
SELECT daily_usd_cap::float8, per_pr_token_cap
FROM cost_caps
WHERE tenant_id = $1 AND repo_id IS NULL
`, string(tenantId)).Scan(&cap.DailyUsdCap, &cap.PerPrTokenCap)
	if err == nil {
		return cap, nil
	}
	if !errors.Is(err, pgx.ErrNoRows) {
		return store.CostCap{}, fmt.Errorf("get cap (tenant default): %w", err)
	}

	return store.CostCap{
		DailyUsdCap:   s.defaultDailyUsdCap,
		PerPrTokenCap: s.defaultPerPrTokenCap,
	}, nil
}

// RecordSpend inserts a spend row in a daily-aggregated table. The
// design just stores per_run cost; here we sum on read instead of
// maintaining a separate spend ledger. So this is currently a no-op —
// spend is summed from pr_runs in TodaySpend. RecordSpend is retained
// for future use (e.g., separate ledger).
func (s *CostCapStore) RecordSpend(_ context.Context, _ ports.TenantId, _ ports.RepoId, _ float64, _ time.Time) error {
	return nil
}

// TodaySpend sums today's cost_usd from pr_runs for the (tenant, repo).
// The tz argument names the timezone for the day boundary.
func (s *CostCapStore) TodaySpend(ctx context.Context, tenantId ports.TenantId, repoId ports.RepoId, tz string) (float64, error) {
	if tz == "" {
		tz = "UTC"
	}
	var total float64
	err := s.pool.QueryRow(ctx, `
SELECT COALESCE(SUM(cost_usd)::float8, 0)
FROM pr_runs
WHERE tenant_id = $1
  AND repo_id = $2
  AND started_at >= date_trunc('day', now() AT TIME ZONE $3) AT TIME ZONE $3
`, string(tenantId), string(repoId), tz).Scan(&total)
	if err != nil {
		return 0, fmt.Errorf("today spend: %w", err)
	}
	return total, nil
}

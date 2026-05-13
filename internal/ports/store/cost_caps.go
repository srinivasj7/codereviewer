package store

import (
	"context"
	"time"

	"codereviewer/internal/ports"
)

// CostCapStore reads effective caps and records daily spend. The review
// pipeline calls GetEffective + TodaySpend BEFORE any LLM call.
type CostCapStore interface {
	GetEffective(ctx context.Context, tenantId ports.TenantId, repoId ports.RepoId) (CostCap, error)
	RecordSpend(ctx context.Context, tenantId ports.TenantId, repoId ports.RepoId, usd float64, at time.Time) error
	TodaySpend(ctx context.Context, tenantId ports.TenantId, repoId ports.RepoId, tz string) (float64, error)
}

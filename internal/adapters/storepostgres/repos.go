package storepostgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"

	"codereviewer/internal/ports"
)

// RepoStore is the Postgres implementation of store.RepoStore.
type RepoStore struct {
	pool *pgxpool.Pool
}

// EnsureExists upserts the tenant and repo for one webhook source.
// The repos.UNIQUE(owner, name) constraint provides idempotency.
func (s *RepoStore) EnsureExists(ctx context.Context, repo ports.RepoRef) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	if _, err := tx.Exec(ctx, `
INSERT INTO tenants (tenant_id, name) VALUES ($1, $2)
ON CONFLICT (tenant_id) DO NOTHING
`, string(repo.TenantId), tenantDisplayName(repo.TenantId)); err != nil {
		return fmt.Errorf("ensure tenant: %w", err)
	}

	if _, err := tx.Exec(ctx, `
INSERT INTO repos (repo_id, tenant_id, owner, name, default_branch)
VALUES ($1, $2, $3, $4, $5)
ON CONFLICT (owner, name) DO UPDATE SET
  default_branch = EXCLUDED.default_branch,
  tenant_id      = EXCLUDED.tenant_id
`, string(repo.RepoId), string(repo.TenantId), repo.Owner, repo.Name, repo.DefaultBranch); err != nil {
		return fmt.Errorf("ensure repo: %w", err)
	}

	return tx.Commit(ctx)
}

// Get returns the repo by id.
func (s *RepoStore) Get(ctx context.Context, repoId ports.RepoId) (ports.RepoRef, bool, error) {
	var ref ports.RepoRef
	var defaultBranch *string
	err := s.pool.QueryRow(ctx, `
SELECT tenant_id, owner, name, default_branch
FROM repos
WHERE repo_id = $1
`, string(repoId)).Scan((*string)(&ref.TenantId), &ref.Owner, &ref.Name, &defaultBranch)
	if errors.Is(err, pgx.ErrNoRows) {
		return ports.RepoRef{}, false, nil
	}
	if err != nil {
		return ports.RepoRef{}, false, fmt.Errorf("get repo: %w", err)
	}
	ref.RepoId = repoId
	if defaultBranch != nil {
		ref.DefaultBranch = *defaultBranch
	}
	return ref, true, nil
}

func tenantDisplayName(id ports.TenantId) string {
	if id == "" {
		return "default"
	}
	return string(id)
}

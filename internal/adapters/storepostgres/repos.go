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

	// Provider is the VCS adapter that owns the repo. Empty (which
	// happens for refs constructed before slice 6B's RepoRef.Provider
	// field existed, or for in-memory fakes) maps to the schema's
	// default of 'github' via the COALESCE — the CHECK constraint on
	// repos.provider rejects anything else.
	provider := string(repo.Provider)
	if _, err := tx.Exec(ctx, `
INSERT INTO repos (repo_id, tenant_id, owner, name, default_branch, provider)
VALUES ($1, $2, $3, $4, $5, COALESCE(NULLIF($6, ''), 'github'))
ON CONFLICT (owner, name) DO UPDATE SET
  default_branch = EXCLUDED.default_branch,
  tenant_id      = EXCLUDED.tenant_id,
  provider       = EXCLUDED.provider
`, string(repo.RepoId), string(repo.TenantId), repo.Owner, repo.Name, repo.DefaultBranch, provider); err != nil {
		return fmt.Errorf("ensure repo: %w", err)
	}

	return tx.Commit(ctx)
}

// Get returns the repo by id.
func (s *RepoStore) Get(ctx context.Context, repoId ports.RepoId) (ports.RepoRef, bool, error) {
	var ref ports.RepoRef
	var defaultBranch *string
	var provider string
	err := s.pool.QueryRow(ctx, `
SELECT tenant_id, owner, name, default_branch, COALESCE(enabled, true), provider
FROM repos
WHERE repo_id = $1
`, string(repoId)).Scan((*string)(&ref.TenantId), &ref.Owner, &ref.Name, &defaultBranch, &ref.Enabled, &provider)
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
	ref.Provider = ports.VcsProvider(provider)
	return ref, true, nil
}

// ListByTenant returns all repos for a tenant.
func (s *RepoStore) ListByTenant(ctx context.Context, tenant ports.TenantId) ([]ports.RepoRef, error) {
	rows, err := s.pool.Query(ctx, `
SELECT repo_id, tenant_id, owner, name, default_branch, COALESCE(enabled, true), provider
FROM repos WHERE tenant_id = $1
ORDER BY repo_id
`, string(tenant))
	if err != nil {
		return nil, fmt.Errorf("list repos: %w", err)
	}
	defer rows.Close()
	var out []ports.RepoRef
	for rows.Next() {
		var ref ports.RepoRef
		var defaultBranch *string
		var provider string
		if err := rows.Scan(&ref.RepoId, (*string)(&ref.TenantId), &ref.Owner, &ref.Name, &defaultBranch, &ref.Enabled, &provider); err != nil {
			return nil, fmt.Errorf("scan repo: %w", err)
		}
		if defaultBranch != nil {
			ref.DefaultBranch = *defaultBranch
		}
		ref.Provider = ports.VcsProvider(provider)
		out = append(out, ref)
	}
	return out, rows.Err()
}

// SetEnabled toggles repos.enabled.
func (s *RepoStore) SetEnabled(ctx context.Context, repoId ports.RepoId, enabled bool) error {
	_, err := s.pool.Exec(ctx,
		`UPDATE repos SET enabled = $2 WHERE repo_id = $1`,
		string(repoId), enabled)
	if err != nil {
		return fmt.Errorf("set enabled: %w", err)
	}
	return nil
}

// Tombstone deletes retrieval data for repoId so subsequent reviews
// (if the repo is later re-enabled with a fresh index) start clean.
// review_comments are also cleared so the LLM can't see comments from
// a deleted-then-rejoined repo.
func (s *RepoStore) Tombstone(ctx context.Context, repoId ports.RepoId) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM code_chunks WHERE repo_id = $1`, string(repoId)); err != nil {
		return fmt.Errorf("tombstone code_chunks: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM review_comments WHERE repo_id = $1`, string(repoId)); err != nil {
		return fmt.Errorf("tombstone review_comments: %w", err)
	}
	return tx.Commit(ctx)
}

func tenantDisplayName(id ports.TenantId) string {
	if id == "" {
		return "default"
	}
	return string(id)
}

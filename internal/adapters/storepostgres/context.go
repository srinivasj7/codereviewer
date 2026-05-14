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

// ContextStore is the Postgres implementation of store.ContextStore.
type ContextStore struct {
	pool *pgxpool.Pool
}

// UpsertInstructionSet inserts or updates by set_id. If SetId is empty
// a new UUID is generated.
func (s *ContextStore) UpsertInstructionSet(ctx context.Context, set store.InstructionSet) error {
	id := set.SetId
	if id == "" {
		id = uuid.NewString()
	}
	updatedBy := set.UpdatedBy
	if updatedBy == "" {
		updatedBy = "system"
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO instruction_sets (set_id, tenant_id, name, body, updated_at, updated_by)
VALUES ($1,$2,$3,$4,now(),$5)
ON CONFLICT (set_id) DO UPDATE SET
  name = EXCLUDED.name,
  body = EXCLUDED.body,
  updated_at = now(),
  updated_by = EXCLUDED.updated_by
`, id, string(set.TenantId), set.Name, set.Body, updatedBy)
	if err != nil {
		return fmt.Errorf("upsert instruction set: %w", err)
	}
	return nil
}

// ListInstructionSets returns all sets for one tenant.
func (s *ContextStore) ListInstructionSets(ctx context.Context, tenant ports.TenantId) ([]store.InstructionSet, error) {
	rows, err := s.pool.Query(ctx, `
SELECT set_id, tenant_id, name, body, updated_at, updated_by
FROM instruction_sets WHERE tenant_id = $1
ORDER BY name
`, string(tenant))
	if err != nil {
		return nil, fmt.Errorf("list sets: %w", err)
	}
	defer rows.Close()
	var out []store.InstructionSet
	for rows.Next() {
		var st store.InstructionSet
		var tenantStr string
		if err := rows.Scan(&st.SetId, &tenantStr, &st.Name, &st.Body, &st.UpdatedAt, &st.UpdatedBy); err != nil {
			return nil, fmt.Errorf("scan set: %w", err)
		}
		st.TenantId = ports.TenantId(tenantStr)
		out = append(out, st)
	}
	return out, rows.Err()
}

// GetInstructionSet by id.
func (s *ContextStore) GetInstructionSet(ctx context.Context, setId string) (store.InstructionSet, bool, error) {
	var st store.InstructionSet
	var tenantStr string
	err := s.pool.QueryRow(ctx, `
SELECT set_id, tenant_id, name, body, updated_at, updated_by
FROM instruction_sets WHERE set_id = $1
`, setId).Scan(&st.SetId, &tenantStr, &st.Name, &st.Body, &st.UpdatedAt, &st.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.InstructionSet{}, false, nil
	}
	if err != nil {
		return store.InstructionSet{}, false, fmt.Errorf("get set: %w", err)
	}
	st.TenantId = ports.TenantId(tenantStr)
	return st, true, nil
}

// DeleteInstructionSet removes the row and any repo assignments to it.
func (s *ContextStore) DeleteInstructionSet(ctx context.Context, setId string) error {
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return err
	}
	defer tx.Rollback(ctx)
	if _, err := tx.Exec(ctx, `DELETE FROM repo_instruction_sets WHERE set_id = $1`, setId); err != nil {
		return fmt.Errorf("delete assignments: %w", err)
	}
	if _, err := tx.Exec(ctx, `DELETE FROM instruction_sets WHERE set_id = $1`, setId); err != nil {
		return fmt.Errorf("delete set: %w", err)
	}
	return tx.Commit(ctx)
}

// AssignSetToRepo upserts the repo->set link.
func (s *ContextStore) AssignSetToRepo(ctx context.Context, repoId ports.RepoId, setId string) error {
	_, err := s.pool.Exec(ctx, `
INSERT INTO repo_instruction_sets (repo_id, set_id) VALUES ($1,$2)
ON CONFLICT (repo_id) DO UPDATE SET set_id = EXCLUDED.set_id
`, string(repoId), setId)
	if err != nil {
		return fmt.Errorf("assign set to repo: %w", err)
	}
	return nil
}

// UnassignFromRepo deletes the repo's assignment, if any.
func (s *ContextStore) UnassignFromRepo(ctx context.Context, repoId ports.RepoId) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM repo_instruction_sets WHERE repo_id = $1`, string(repoId))
	return err
}

// GetSetForRepo returns the assigned set joined through repo_instruction_sets.
func (s *ContextStore) GetSetForRepo(ctx context.Context, repoId ports.RepoId) (store.InstructionSet, bool, error) {
	var st store.InstructionSet
	var tenantStr string
	err := s.pool.QueryRow(ctx, `
SELECT s.set_id, s.tenant_id, s.name, s.body, s.updated_at, s.updated_by
FROM repo_instruction_sets r
JOIN instruction_sets s ON s.set_id = r.set_id
WHERE r.repo_id = $1
`, string(repoId)).Scan(&st.SetId, &tenantStr, &st.Name, &st.Body, &st.UpdatedAt, &st.UpdatedBy)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.InstructionSet{}, false, nil
	}
	if err != nil {
		return store.InstructionSet{}, false, fmt.Errorf("get set for repo: %w", err)
	}
	st.TenantId = ports.TenantId(tenantStr)
	return st, true, nil
}

// AppendPrContext stores a context item. ItemId is generated if empty.
func (s *ContextStore) AppendPrContext(ctx context.Context, item store.PrContextItem) error {
	id := item.ItemId
	if id == "" {
		id = uuid.NewString()
	}
	createdBy := item.CreatedBy
	if createdBy == "" {
		createdBy = "system"
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO pr_context_items (item_id, tenant_id, repo_id, pr_number, source, title, body, created_at, created_by)
VALUES ($1,$2,$3,$4,$5,$6,$7,now(),$8)
`, id, string(item.TenantId), string(item.RepoId), item.PrNumber, item.Source, item.Title, item.Body, createdBy)
	if err != nil {
		return fmt.Errorf("append pr context: %w", err)
	}
	return nil
}

// ListPrContext returns items for a PR, newest first.
func (s *ContextStore) ListPrContext(ctx context.Context, ref ports.PrRef) ([]store.PrContextItem, error) {
	rows, err := s.pool.Query(ctx, `
SELECT item_id, tenant_id, repo_id, pr_number, source, title, body, created_at, created_by
FROM pr_context_items
WHERE tenant_id = $1 AND repo_id = $2 AND pr_number = $3
ORDER BY created_at DESC
`, string(ref.TenantId), string(ref.RepoId), ref.PrNumber)
	if err != nil {
		return nil, fmt.Errorf("list pr context: %w", err)
	}
	defer rows.Close()
	var out []store.PrContextItem
	for rows.Next() {
		var item store.PrContextItem
		var tenantStr, repoStr string
		if err := rows.Scan(&item.ItemId, &tenantStr, &repoStr, &item.PrNumber, &item.Source,
			&item.Title, &item.Body, &item.CreatedAt, &item.CreatedBy); err != nil {
			return nil, fmt.Errorf("scan pr context: %w", err)
		}
		item.TenantId = ports.TenantId(tenantStr)
		item.RepoId = ports.RepoId(repoStr)
		out = append(out, item)
	}
	return out, rows.Err()
}

// DeletePrContextItem removes one item.
func (s *ContextStore) DeletePrContextItem(ctx context.Context, itemId string) error {
	_, err := s.pool.Exec(ctx, `DELETE FROM pr_context_items WHERE item_id = $1`, itemId)
	return err
}

// DeletePrContextBefore removes items with created_at < cutoff.
func (s *ContextStore) DeletePrContextBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM pr_context_items WHERE created_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete pr_context_items: %w", err)
	}
	return tag.RowsAffected(), nil
}

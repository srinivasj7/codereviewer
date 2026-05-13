package storepostgres

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// RuleStore is the Postgres implementation of store.RuleStore.
type RuleStore struct {
	pool *pgxpool.Pool
}

// UpsertFromRepo replaces rules whose source_commit matches the new
// commit and inserts any new ones. Each rule's stable id derives from
// the upsert input.
func (s *RuleStore) UpsertFromRepo(ctx context.Context, sourceCommit string, rules []store.RuleUpsert) error {
	if len(rules) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, r := range rules {
		id := string(r.RuleId)
		if id == "" {
			id = uuid.NewString()
		}
		_, err := tx.Exec(ctx, `
INSERT INTO rules (
  rule_id, tenant_id, scope, title, description, source_commit, embedding, enabled, updated_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, true, now())
ON CONFLICT (rule_id) DO UPDATE SET
  scope = EXCLUDED.scope,
  title = EXCLUDED.title,
  description = EXCLUDED.description,
  source_commit = EXCLUDED.source_commit,
  embedding = EXCLUDED.embedding,
  enabled = true,
  updated_at = now()
`,
			id, string(r.TenantId), r.Scope, r.Title, r.Description,
			sourceCommit, pgvector.NewVector(r.Embedding),
		)
		if err != nil {
			return fmt.Errorf("upsert rule %s: %w", id, err)
		}
	}
	return tx.Commit(ctx)
}

// ListForScope returns enabled rules whose scope matches any of paths
// or is the wildcard "*". Scope syntax (from design Appendix B):
//   - "*"               → match all
//   - "repo:foo/bar"    → match this repo by repoId
//   - "path:**/*.sql"   → match files by glob
func (s *RuleStore) ListForScope(ctx context.Context, repoId ports.RepoId, paths []string) ([]store.Rule, error) {
	rows, err := s.pool.Query(ctx, `
SELECT rule_id::text, scope, title, description, enabled
FROM rules
WHERE enabled = true
`)
	if err != nil {
		return nil, fmt.Errorf("list rules: %w", err)
	}
	defer rows.Close()

	var all []store.Rule
	for rows.Next() {
		var r store.Rule
		if err := rows.Scan(&r.RuleId, &r.Scope, &r.Title, &r.Description, &r.Enabled); err != nil {
			return nil, fmt.Errorf("scan rule: %w", err)
		}
		all = append(all, r)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	// Filter in-memory; the rule count per tenant is bounded.
	out := make([]store.Rule, 0, len(all))
	for _, r := range all {
		if ruleMatches(r.Scope, repoId, paths) {
			out = append(out, r)
		}
	}
	return out, nil
}

// TombstoneMissing disables rules whose source_commit is the given one
// but whose id is not in knownIds. Returns the number disabled.
func (s *RuleStore) TombstoneMissing(ctx context.Context, sourceCommit string, knownIds []store.RuleId) (int, error) {
	ids := make([]string, len(knownIds))
	for i, id := range knownIds {
		ids[i] = string(id)
	}
	cmd, err := s.pool.Exec(ctx, `
UPDATE rules SET enabled = false, updated_at = now()
WHERE source_commit = $1 AND NOT (rule_id::text = ANY($2::text[])) AND enabled = true
`, sourceCommit, ids)
	if err != nil {
		return 0, fmt.Errorf("tombstone rules: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

func ruleMatches(scope string, repoId ports.RepoId, paths []string) bool {
	if scope == "*" || scope == "" {
		return true
	}
	if strings.HasPrefix(scope, "repo:") {
		return strings.TrimPrefix(scope, "repo:") == string(repoId)
	}
	if strings.HasPrefix(scope, "path:") {
		pattern := strings.TrimPrefix(scope, "path:")
		for _, p := range paths {
			if matched, _ := filepath.Match(pattern, p); matched {
				return true
			}
			// Also try with ** expanded to a single component (filepath.Match
			// doesn't support **, so this is a coarse approximation).
			if matched, _ := filepath.Match(strings.ReplaceAll(pattern, "**", "*"), p); matched {
				return true
			}
		}
		return false
	}
	return false
}

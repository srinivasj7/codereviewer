package storepostgres

import (
	"context"
	"errors"
	"fmt"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// CommentStore is the Postgres implementation of store.CommentStore.
type CommentStore struct {
	pool *pgxpool.Pool
}

// Upsert inserts or replaces by github_id when present. Returns the
// authoritative comment_id from the database — on conflict, this is
// the id of the pre-existing row, not the freshly generated one.
func (s *CommentStore) Upsert(ctx context.Context, c store.CommentUpsert) (store.CommentId, error) {
	id := uuid.NewString()
	var stored string
	err := s.pool.QueryRow(ctx, `
INSERT INTO review_comments (
  comment_id, tenant_id, repo_id, pr_number, source, github_id,
  file_path, start_line, end_line, diff_hunk, comment_text, category,
  outcome, outcome_signal, embedding
) VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15)
ON CONFLICT (github_id) WHERE github_id IS NOT NULL DO UPDATE SET
  comment_text   = EXCLUDED.comment_text,
  category       = EXCLUDED.category,
  outcome        = EXCLUDED.outcome,
  outcome_signal = EXCLUDED.outcome_signal,
  embedding      = EXCLUDED.embedding
RETURNING comment_id::text
`,
		id, string(c.TenantId), string(c.RepoId), c.PrNumber, c.Source, c.GithubId,
		c.FilePath, c.StartLine, c.EndLine, nullableText(c.DiffHunk), c.CommentText, nullableText(c.Category),
		string(c.Outcome), string(c.OutcomeSignal), nullableVector(c.Embedding),
	).Scan(&stored)
	if err != nil {
		return "", fmt.Errorf("upsert comment: %w", err)
	}
	return store.CommentId(stored), nil
}

// SearchByEmbedding returns top-K most-similar comments. Accepted
// comments are boosted, dismissed comments are penalized — matches
// the weight scheme in design §6.1 step 6.
func (s *CommentStore) SearchByEmbedding(ctx context.Context, args store.SearchComments) ([]store.CommentHit, error) {
	rows, err := s.pool.Query(ctx, `
SELECT comment_id, COALESCE(file_path,''), comment_text, COALESCE(category,''), COALESCE(outcome,'pending'),
       (embedding <=> $1) + CASE outcome
         WHEN 'accepted'  THEN -0.1
         WHEN 'dismissed' THEN  0.1
         ELSE 0 END AS adjusted_distance
FROM review_comments
WHERE repo_id = $2 AND embedding IS NOT NULL
ORDER BY adjusted_distance
LIMIT $3
`, pgvector.NewVector(args.Embedding), string(args.RepoId), args.K)
	if err != nil {
		return nil, fmt.Errorf("search comments: %w", err)
	}
	defer rows.Close()

	var hits []store.CommentHit
	for rows.Next() {
		var h store.CommentHit
		if err := rows.Scan(&h.CommentId, &h.FilePath, &h.CommentText, &h.Category, &h.Outcome, &h.Distance); err != nil {
			return nil, fmt.Errorf("scan comment hit: %w", err)
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// UpdateOutcome sets outcome and outcome_signal.
func (s *CommentStore) UpdateOutcome(ctx context.Context, id store.CommentId, outcome store.Outcome, signal store.OutcomeSignal) error {
	cmd, err := s.pool.Exec(ctx, `
UPDATE review_comments SET outcome = $1, outcome_signal = $2, resolved_at = now()
WHERE comment_id = $3
`, string(outcome), string(signal), string(id))
	if err != nil {
		return fmt.Errorf("update outcome: %w", err)
	}
	if cmd.RowsAffected() == 0 {
		return fmt.Errorf("comment %s not found", id)
	}
	return nil
}

// GetByGithubId looks up a comment by its VCS-side external id.
func (s *CommentStore) GetByGithubId(ctx context.Context, githubId int64) (store.Comment, bool, error) {
	var c store.Comment
	var tenant, repo string
	var gid *int64
	err := s.pool.QueryRow(ctx, `
SELECT comment_id, tenant_id, repo_id, pr_number, source, github_id,
       COALESCE(file_path,''), COALESCE(start_line,0), COALESCE(end_line,0),
       comment_text, COALESCE(category,''), COALESCE(outcome,'pending'), created_at
FROM review_comments WHERE github_id = $1
`, githubId).Scan(&c.CommentId, &tenant, &repo, &c.PrNumber, &c.Source, &gid,
		&c.FilePath, &c.StartLine, &c.EndLine, &c.CommentText, &c.Category, &c.Outcome, &c.CreatedAt)
	if errors.Is(err, pgx.ErrNoRows) {
		return store.Comment{}, false, nil
	}
	if err != nil {
		return store.Comment{}, false, fmt.Errorf("get by github id: %w", err)
	}
	c.TenantId = ports.TenantId(tenant)
	c.RepoId = ports.RepoId(repo)
	c.GithubId = gid
	return c, true, nil
}

// ListByPr returns all comments for a PR.
func (s *CommentStore) ListByPr(ctx context.Context, ref ports.PrRef) ([]store.Comment, error) {
	rows, err := s.pool.Query(ctx, `
SELECT comment_id, tenant_id, repo_id, pr_number, source, github_id,
       COALESCE(file_path,''), start_line, end_line, comment_text,
       COALESCE(category,''), COALESCE(outcome,'pending'), created_at
FROM review_comments
WHERE tenant_id = $1 AND repo_id = $2 AND pr_number = $3
ORDER BY created_at ASC
`, string(ref.TenantId), string(ref.RepoId), ref.PrNumber)
	if err != nil {
		return nil, fmt.Errorf("list comments: %w", err)
	}
	defer rows.Close()

	var out []store.Comment
	for rows.Next() {
		var c store.Comment
		var tenant, repo string
		if err := rows.Scan(&c.CommentId, &tenant, &repo, &c.PrNumber, &c.Source, &c.GithubId,
			&c.FilePath, &c.StartLine, &c.EndLine, &c.CommentText, &c.Category, &c.Outcome, &c.CreatedAt); err != nil {
			return nil, fmt.Errorf("scan comment: %w", err)
		}
		c.TenantId = ports.TenantId(tenant)
		c.RepoId = ports.RepoId(repo)
		out = append(out, c)
	}
	return out, rows.Err()
}

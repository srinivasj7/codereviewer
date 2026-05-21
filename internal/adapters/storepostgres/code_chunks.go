package storepostgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// CodeChunkStore is the Postgres implementation of store.CodeChunkStore.
type CodeChunkStore struct {
	pool *pgxpool.Pool
}

// UpsertMany batches chunks into pr_runs in one transaction. Each row's
// chunk_id is upserted by (tenant_id, repo_id, file_path, symbol_name,
// start_line) — caller pre-generates a UUID if not present.
func (s *CodeChunkStore) UpsertMany(ctx context.Context, chunks []store.CodeChunkUpsert) error {
	if len(chunks) == 0 {
		return nil
	}
	tx, err := s.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, c := range chunks {
		chunkId := c.ChunkId
		if chunkId == "" {
			chunkId = uuid.NewString()
		}
		_, err := tx.Exec(ctx, `
INSERT INTO code_chunks (
  chunk_id, tenant_id, repo_id, file_path, symbol_name, symbol_kind,
  start_line, end_line, content, content_hash, commit_sha, embedding, last_indexed_at
) VALUES ($1, $2, $3, $4, $5, $6, $7, $8, $9, $10, $11, $12, now())
ON CONFLICT (chunk_id) DO UPDATE SET
  content = EXCLUDED.content,
  content_hash = EXCLUDED.content_hash,
  commit_sha = EXCLUDED.commit_sha,
  embedding = EXCLUDED.embedding,
  last_indexed_at = now()
`,
			chunkId, string(c.TenantId), string(c.RepoId), c.FilePath,
			nullableText(c.SymbolName), nullableText(c.SymbolKind),
			c.StartLine, c.EndLine, c.Content, c.ContentHash, c.CommitSha,
			pgvector.NewVector(c.Embedding),
		)
		if err != nil {
			return fmt.Errorf("upsert chunk %s: %w", chunkId, err)
		}
	}
	return tx.Commit(ctx)
}

// SearchByEmbedding returns top-K nearest chunks by cosine distance.
// Same-file chunks are ordered first if SameFileBoostPath is set.
func (s *CodeChunkStore) SearchByEmbedding(ctx context.Context, args store.SearchCodeChunks) ([]store.CodeChunkHit, error) {
	rows, err := s.pool.Query(ctx, `
SELECT chunk_id, file_path, COALESCE(symbol_name, ''), start_line, end_line, content,
       embedding <=> $1 AS distance
FROM code_chunks
WHERE repo_id = $2
ORDER BY
  CASE WHEN $3::text <> '' AND file_path = $3 THEN 0 ELSE 1 END,
  embedding <=> $1
LIMIT $4
`, pgvector.NewVector(args.Embedding), string(args.RepoId), args.SameFileBoostPath, args.K)
	if err != nil {
		return nil, fmt.Errorf("search chunks: %w", err)
	}
	defer rows.Close()

	var hits []store.CodeChunkHit
	for rows.Next() {
		var h store.CodeChunkHit
		if err := rows.Scan(&h.ChunkId, &h.FilePath, &h.SymbolName, &h.StartLine, &h.EndLine, &h.Content, &h.Distance); err != nil {
			return nil, fmt.Errorf("scan hit: %w", err)
		}
		hits = append(hits, h)
	}
	return hits, rows.Err()
}

// SoftDeleteMissing removes chunks for repo not in the present set whose
// last_indexed_at is older than olderThan. Slice 1 uses a hard delete;
// the design's "retain 30 days for time-travel" is a slice 4 enhancement.
func (s *CodeChunkStore) SoftDeleteMissing(ctx context.Context, repoId ports.RepoId, present []string, olderThan time.Time) (int, error) {
	cmd, err := s.pool.Exec(ctx, `
DELETE FROM code_chunks
WHERE repo_id = $1
  AND last_indexed_at < $2
  AND NOT (chunk_id = ANY($3::uuid[]))
`, string(repoId), olderThan, present)
	if err != nil {
		return 0, fmt.Errorf("soft delete chunks: %w", err)
	}
	return int(cmd.RowsAffected()), nil
}

// ExistsByContentHash returns true for hashes that already have a chunk
// in the given repo. The indexer uses this to skip re-embedding identical
// content (the cheap path in design §6.2).
func (s *CodeChunkStore) ExistsByContentHash(ctx context.Context, repoId ports.RepoId, hashes []string) (map[string]bool, error) {
	out := make(map[string]bool, len(hashes))
	for _, h := range hashes {
		out[h] = false
	}
	if len(hashes) == 0 {
		return out, nil
	}
	rows, err := s.pool.Query(ctx, `
SELECT DISTINCT content_hash
FROM code_chunks
WHERE repo_id = $1 AND content_hash = ANY($2::text[])
`, string(repoId), hashes)
	if err != nil {
		return nil, fmt.Errorf("exists by hash: %w", err)
	}
	defer rows.Close()
	for rows.Next() {
		var h string
		if err := rows.Scan(&h); err != nil {
			return nil, fmt.Errorf("scan hash: %w", err)
		}
		out[h] = true
	}
	return out, rows.Err()
}

func nullableText(s string) any {
	if s == "" {
		return nil
	}
	return s
}

// nullableVector turns an empty embedding into a SQL NULL so pgvector
// doesn't reject a 0-dim vector. Bot-comment persistence runs before
// any batch-embedding step today, so we may write rows without an
// embedding and backfill later.
func nullableVector(v []float32) any {
	if len(v) == 0 {
		return nil
	}
	return pgvector.NewVector(v)
}

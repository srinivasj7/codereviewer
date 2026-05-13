package storepostgres

import (
	"context"
	"fmt"

	"github.com/jackc/pgx/v5/pgxpool"
	"github.com/pgvector/pgvector-go"

	"codereviewer/internal/ports/store"
)

// EmbeddingCache is the Postgres implementation of store.EmbeddingCache.
// content_hash is PK; identical text never re-embeds.
type EmbeddingCache struct {
	pool *pgxpool.Pool
}

// GetMany fetches cached vectors by hash. Missing entries are absent
// from the returned map.
func (c *EmbeddingCache) GetMany(ctx context.Context, hashes []string) (map[string][]float32, error) {
	out := make(map[string][]float32, len(hashes))
	if len(hashes) == 0 {
		return out, nil
	}
	rows, err := c.pool.Query(ctx, `
SELECT content_hash, embedding
FROM embedding_cache
WHERE content_hash = ANY($1::text[])
`, hashes)
	if err != nil {
		return nil, fmt.Errorf("embedding cache get: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var hash string
		var vec pgvector.Vector
		if err := rows.Scan(&hash, &vec); err != nil {
			return nil, fmt.Errorf("scan cache row: %w", err)
		}
		out[hash] = vec.Slice()
	}
	return out, rows.Err()
}

// PutMany inserts entries; existing hashes are silently kept (cache
// values are deterministic for a given hash and model).
func (c *EmbeddingCache) PutMany(ctx context.Context, entries []store.EmbeddingCacheEntry) error {
	if len(entries) == 0 {
		return nil
	}
	tx, err := c.pool.Begin(ctx)
	if err != nil {
		return fmt.Errorf("begin tx: %w", err)
	}
	defer func() { _ = tx.Rollback(ctx) }()

	for _, e := range entries {
		_, err := tx.Exec(ctx, `
INSERT INTO embedding_cache (content_hash, embedding)
VALUES ($1, $2)
ON CONFLICT (content_hash) DO NOTHING
`, e.Hash, pgvector.NewVector(e.Embedding))
		if err != nil {
			return fmt.Errorf("insert cache entry %s: %w", e.Hash, err)
		}
	}
	return tx.Commit(ctx)
}

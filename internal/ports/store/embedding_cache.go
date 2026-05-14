package store

import "context"

// EmbeddingCache deduplicates embedding calls by content hash. Both the
// indexer and the review pipeline check this cache before calling the
// embeddings endpoint.
type EmbeddingCache interface {
	GetMany(ctx context.Context, hashes []string) (map[string][]float32, error)
	PutMany(ctx context.Context, entries []EmbeddingCacheEntry) error
	// EvictToMax keeps at most maxRows entries, deleting the
	// least-recently-inserted ones first. Returns the count deleted.
	// Adapters that don't track insertion order fall back to deleting
	// by content_hash order (deterministic but not LRU-faithful).
	EvictToMax(ctx context.Context, maxRows int) (int64, error)
}

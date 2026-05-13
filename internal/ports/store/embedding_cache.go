package store

import "context"

// EmbeddingCache deduplicates embedding calls by content hash. Both the
// indexer and the review pipeline check this cache before calling the
// embeddings endpoint.
type EmbeddingCache interface {
	GetMany(ctx context.Context, hashes []string) (map[string][]float32, error)
	PutMany(ctx context.Context, entries []EmbeddingCacheEntry) error
}

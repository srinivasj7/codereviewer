// Package retrieval orchestrates vector retrieval for prompt context.
// Slice 0 ships no-op retrievers; slice 3 wires in real embedding-based
// retrieval against the store ports.
package retrieval

import (
	"context"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// CodeRetriever fetches related code chunks for a diff.
type CodeRetriever interface {
	RetrieveForDiff(ctx context.Context, repoId ports.RepoId, diff string) ([]store.CodeChunkHit, error)
}

// NoopCodeRetriever returns no hits. Used in slice 0 and in tests that
// don't exercise retrieval.
type NoopCodeRetriever struct{}

// RetrieveForDiff always returns empty.
func (NoopCodeRetriever) RetrieveForDiff(_ context.Context, _ ports.RepoId, _ string) ([]store.CodeChunkHit, error) {
	return nil, nil
}

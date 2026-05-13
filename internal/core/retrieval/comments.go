package retrieval

import (
	"context"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// CommentRetriever fetches semantically similar past comments for a diff.
type CommentRetriever interface {
	RetrieveForDiff(ctx context.Context, repoId ports.RepoId, diff string) ([]store.CommentHit, error)
}

// NoopCommentRetriever returns no hits.
type NoopCommentRetriever struct{}

// RetrieveForDiff always returns empty.
func (NoopCommentRetriever) RetrieveForDiff(_ context.Context, _ ports.RepoId, _ string) ([]store.CommentHit, error) {
	return nil, nil
}

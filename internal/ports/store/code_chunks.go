package store

import (
	"context"
	"time"

	"codereviewer/internal/ports"
)

// CodeChunkStore manages symbol-bounded chunks of indexed source code.
// The indexer pipeline writes here; the review pipeline reads here for
// retrieval-augmented prompt context.
type CodeChunkStore interface {
	UpsertMany(ctx context.Context, chunks []CodeChunkUpsert) error
	SearchByEmbedding(ctx context.Context, args SearchCodeChunks) ([]CodeChunkHit, error)
	SoftDeleteMissing(ctx context.Context, repoId ports.RepoId, present []string, olderThan time.Time) (int, error)
	ExistsByContentHash(ctx context.Context, repoId ports.RepoId, hashes []string) (map[string]bool, error)
}

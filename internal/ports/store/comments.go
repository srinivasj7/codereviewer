package store

import (
	"context"

	"codereviewer/internal/ports"
)

// CommentStore manages review_comments rows. Both bot output and human
// backfill flow through here; the embedding column enables retrieval of
// semantically similar past comments.
type CommentStore interface {
	Upsert(ctx context.Context, c CommentUpsert) (CommentId, error)
	SearchByEmbedding(ctx context.Context, args SearchComments) ([]CommentHit, error)
	UpdateOutcome(ctx context.Context, id CommentId, outcome Outcome, signal OutcomeSignal) error
	ListByPr(ctx context.Context, ref ports.PrRef) ([]Comment, error)
}

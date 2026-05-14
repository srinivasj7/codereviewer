package store

import (
	"context"
	"time"
)

// FeedbackStore append-only log of outcome signals. The current outcome
// on review_comments is derived from the latest signal here.
type FeedbackStore interface {
	Append(ctx context.Context, e FeedbackEvent) error
	ListForComment(ctx context.Context, id CommentId) ([]FeedbackEvent, error)
	// DeleteBefore removes events observed_at < cutoff. Returns the row
	// count deleted. Outcomes on review_comments are unaffected (they
	// are denormalized; the latest signal won.)
	DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error)
}

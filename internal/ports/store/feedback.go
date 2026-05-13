package store

import "context"

// FeedbackStore append-only log of outcome signals. The current outcome
// on review_comments is derived from the latest signal here.
type FeedbackStore interface {
	Append(ctx context.Context, e FeedbackEvent) error
	ListForComment(ctx context.Context, id CommentId) ([]FeedbackEvent, error)
}

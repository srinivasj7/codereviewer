package storepostgres

import (
	"context"
	"fmt"
	"time"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5/pgxpool"

	"codereviewer/internal/ports/store"
)

// FeedbackStore is the Postgres implementation of store.FeedbackStore.
type FeedbackStore struct {
	pool *pgxpool.Pool
}

// Append inserts a feedback_events row.
func (s *FeedbackStore) Append(ctx context.Context, e store.FeedbackEvent) error {
	id := e.EventId
	if id == "" {
		id = uuid.NewString()
	}
	_, err := s.pool.Exec(ctx, `
INSERT INTO feedback_events (event_id, tenant_id, comment_id, signal, observed_at)
VALUES ($1, $2, $3, $4, COALESCE(NULLIF($5, '0001-01-01 00:00:00+00'::timestamptz), now()))
`,
		id, string(e.TenantId), string(e.CommentId), string(e.Signal), e.ObservedAt,
	)
	if err != nil {
		return fmt.Errorf("append feedback: %w", err)
	}
	return nil
}

// ListForComment returns events for one comment, oldest first.
func (s *FeedbackStore) ListForComment(ctx context.Context, id store.CommentId) ([]store.FeedbackEvent, error) {
	rows, err := s.pool.Query(ctx, `
SELECT event_id::text, tenant_id, comment_id::text, signal, observed_at
FROM feedback_events
WHERE comment_id = $1
ORDER BY observed_at ASC
`, string(id))
	if err != nil {
		return nil, fmt.Errorf("list feedback: %w", err)
	}
	defer rows.Close()

	var out []store.FeedbackEvent
	for rows.Next() {
		var e store.FeedbackEvent
		var tenant, cid, signal string
		if err := rows.Scan(&e.EventId, &tenant, &cid, &signal, &e.ObservedAt); err != nil {
			return nil, fmt.Errorf("scan event: %w", err)
		}
		e.CommentId = store.CommentId(cid)
		e.Signal = store.OutcomeSignal(signal)
		out = append(out, e)
	}
	return out, rows.Err()
}

// DeleteBefore removes feedback_events with observed_at < cutoff.
func (s *FeedbackStore) DeleteBefore(ctx context.Context, cutoff time.Time) (int64, error) {
	tag, err := s.pool.Exec(ctx, `DELETE FROM feedback_events WHERE observed_at < $1`, cutoff)
	if err != nil {
		return 0, fmt.Errorf("delete feedback_events: %w", err)
	}
	return tag.RowsAffected(), nil
}

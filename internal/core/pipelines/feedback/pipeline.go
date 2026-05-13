// Package feedback captures outcome signals on bot review comments
// (design §6.3). Each FeedbackJob carries one observed signal — a
// reaction added to a bot comment, or a reply posted under it.
//
// Slice 4 implements reactions + replies. The implicit "line-changed"
// signal (diffing new commits against the prior one to see whether the
// commented range was modified) is deferred — it requires per-commit
// diff inspection that isn't yet wired through the pipeline.
package feedback

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

// Deps holds the feedback pipeline's collaborators.
type Deps struct {
	Comments store.CommentStore
	Feedback store.FeedbackStore
	Clock    ports.Clock
	Obs      ports.Obs
}

// Pipeline is the feedback use case.
type Pipeline struct {
	deps Deps
}

// NewPipeline returns a Pipeline ready to Handle.
func NewPipeline(deps Deps) *Pipeline { return &Pipeline{deps: deps} }

// Handle is the bus ConsumeFunc for the feedback queue. It decodes one
// FeedbackJob, looks up the targeted bot comment, classifies the signal,
// appends an event, and updates the comment outcome.
func (p *Pipeline) Handle(ctx context.Context, payload []byte, cctx ports.ConsumeCtx) error {
	var job schemas.FeedbackJob
	if err := json.Unmarshal(payload, &job); err != nil {
		p.deps.Obs.Logger.Warn("feedback: bad payload", "err", err.Error())
		// Bad payload is non-retryable; ack so it doesn't loop.
		return cctx.Ack()
	}

	outcome, signal, ok := classify(job)
	if !ok {
		p.deps.Obs.Logger.Info("feedback: ignored",
			"kind", job.Kind, "reaction", job.Reaction, "comment_external_id", job.CommentExternalId)
		return cctx.Ack()
	}

	c, found, err := p.deps.Comments.GetByGithubId(ctx, job.CommentExternalId)
	if err != nil {
		_ = cctx.Nack("lookup comment failed")
		return fmt.Errorf("get comment by github id %d: %w", job.CommentExternalId, err)
	}
	if !found {
		// Signal targets a comment we didn't author or don't yet have.
		// Ack — replaying won't help.
		p.deps.Obs.Logger.Info("feedback: comment not found",
			"comment_external_id", job.CommentExternalId, "kind", job.Kind)
		return cctx.Ack()
	}
	if c.Source != "bot" {
		// Feedback only meaningful against our own comments.
		return cctx.Ack()
	}

	now := p.deps.Clock.Now()
	if err := p.deps.Feedback.Append(ctx, store.FeedbackEvent{
		TenantId:   c.TenantId,
		CommentId:  c.CommentId,
		Signal:     signal,
		ObservedAt: now,
	}); err != nil {
		_ = cctx.Nack("append feedback failed")
		return fmt.Errorf("append feedback: %w", err)
	}

	if err := p.deps.Comments.UpdateOutcome(ctx, c.CommentId, outcome, signal); err != nil {
		_ = cctx.Nack("update outcome failed")
		return fmt.Errorf("update outcome: %w", err)
	}

	p.deps.Obs.Logger.Info("feedback: recorded",
		"comment_id", string(c.CommentId),
		"outcome", string(outcome),
		"signal", string(signal),
		"latency_ms", time.Since(now).Milliseconds(),
	)
	return cctx.Ack()
}

// classify maps a FeedbackJob into an (outcome, signal). Returns
// ok=false for jobs we don't act on — non-thumbs reactions, empty kinds.
func classify(job schemas.FeedbackJob) (store.Outcome, store.OutcomeSignal, bool) {
	switch job.Kind {
	case "reaction":
		switch job.Reaction {
		case "+1", "thumbs_up", "heart", "hooray", "rocket":
			return store.OutcomeAccepted, store.SignalThumbsUp, true
		case "-1", "thumbs_down", "confused":
			return store.OutcomeDismissed, store.SignalThumbsDown, true
		}
		return "", "", false
	case "reply":
		return store.OutcomeDiscussed, store.SignalReplied, true
	}
	return "", "", false
}

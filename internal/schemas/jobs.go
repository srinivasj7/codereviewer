package schemas

import (
	"context"
	"encoding/json"
	"fmt"

	"codereviewer/internal/ports"
)

// ReviewJob triggers one PR review run.
type ReviewJob struct {
	PrRef   ports.PrRef   `json:"pr_ref"`
	Trigger ports.Trigger `json:"trigger"`
}

// IdempotencyKey is the dedup key used by the bus and pr_runs.
func (j ReviewJob) IdempotencyKey() string {
	return fmt.Sprintf("review:%s:%s:%d:%s", j.PrRef.TenantId, j.PrRef.RepoId, j.PrRef.PrNumber, j.PrRef.HeadSha)
}

// IndexJob triggers one indexer run for a default-branch push.
type IndexJob struct {
	TenantId  ports.TenantId `json:"tenant_id"`
	RepoId    ports.RepoId   `json:"repo_id"`
	Ref       string         `json:"ref"`
	BeforeSha string         `json:"before_sha"`
	HeadSha   string         `json:"head_sha"`
}

// IdempotencyKey for IndexJob.
func (j IndexJob) IdempotencyKey() string {
	return fmt.Sprintf("index:%s:%s:%s", j.TenantId, j.RepoId, j.HeadSha)
}

// FeedbackJob carries one observed signal targeting a bot comment.
// Kind discriminates: "reaction" with Reaction set, or "reply" with
// AuthorId set. CommentExternalId is the GitHub id of the bot comment
// that received the signal (used to look up our internal row).
type FeedbackJob struct {
	TenantId          ports.TenantId `json:"tenant_id"`
	RepoId            ports.RepoId   `json:"repo_id"`
	Kind              string         `json:"kind"`
	CommentExternalId int64          `json:"comment_external_id"`
	Reaction          string         `json:"reaction,omitempty"`
	AuthorId          string         `json:"author_id,omitempty"`
}

// IdempotencyKey for FeedbackJob: a given (kind, comment, reaction,
// author) is one logical signal regardless of redelivery.
func (j FeedbackJob) IdempotencyKey() string {
	return fmt.Sprintf("feedback:%s:%d:%s:%s", j.Kind, j.CommentExternalId, j.Reaction, j.AuthorId)
}

// BackfillJob requests historical comment ingestion.
type BackfillJob struct {
	TenantId    ports.TenantId `json:"tenant_id"`
	RepoId      ports.RepoId   `json:"repo_id"`
	WindowDays  int            `json:"window_days"`
}

// IdempotencyKey for BackfillJob — based on the request, not the data.
func (j BackfillJob) IdempotencyKey() string {
	return fmt.Sprintf("backfill:%s:%s:%d", j.TenantId, j.RepoId, j.WindowDays)
}

// PublishReviewJob marshals a ReviewJob and publishes it to the review queue.
func PublishReviewJob(ctx context.Context, bus ports.MessageBus, job ReviewJob) error {
	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal review job: %w", err)
	}
	return bus.Publish(ctx, ports.QueueReview, body, ports.PublishOpts{IdempotencyKey: job.IdempotencyKey()})
}

// PublishIndexJob marshals an IndexJob and publishes it.
func PublishIndexJob(ctx context.Context, bus ports.MessageBus, job IndexJob) error {
	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal index job: %w", err)
	}
	return bus.Publish(ctx, ports.QueueIndex, body, ports.PublishOpts{IdempotencyKey: job.IdempotencyKey()})
}

// PublishFeedbackJob marshals a FeedbackJob and publishes it.
func PublishFeedbackJob(ctx context.Context, bus ports.MessageBus, job FeedbackJob) error {
	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal feedback job: %w", err)
	}
	return bus.Publish(ctx, ports.QueueFeedback, body, ports.PublishOpts{IdempotencyKey: job.IdempotencyKey()})
}

// PublishBackfillJob marshals a BackfillJob and publishes it.
func PublishBackfillJob(ctx context.Context, bus ports.MessageBus, job BackfillJob) error {
	body, err := json.Marshal(job)
	if err != nil {
		return fmt.Errorf("marshal backfill job: %w", err)
	}
	return bus.Publish(ctx, ports.QueueBackfill, body, ports.PublishOpts{IdempotencyKey: job.IdempotencyKey()})
}

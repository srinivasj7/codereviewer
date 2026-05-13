package ports

import (
	"context"
	"net/http"
	"time"
)

// VcsSource is the version-control system port. The pilot implementation
// is GitHub Cloud via a GitHub App. Webhooks are verified via the same
// adapter that fetches diffs and posts reviews so signature secrets stay
// confined to one package.
type VcsSource interface {
	VerifyWebhook(ctx context.Context, headers http.Header, rawBody []byte) (WebhookEvent, error)
	FetchDiff(ctx context.Context, ref PrRef) (UnifiedDiff, error)
	FetchFileAt(ctx context.Context, repoId RepoId, sha, path string) (string, error)
	ListChangedFiles(ctx context.Context, repoId RepoId, baseSha, headSha string) ([]ChangedFile, error)
	ListPrComments(ctx context.Context, ref PrRef) ([]HumanComment, error)
	PostReview(ctx context.Context, ref PrRef, review ReviewPayload) (PostedReview, error)
	UpdateCheck(ctx context.Context, ref PrRef, result CheckResult) error
}

// WebhookEventKind discriminates the WebhookEvent union.
type WebhookEventKind string

const (
	WebhookKindPullRequest   WebhookEventKind = "pull_request"
	WebhookKindPush          WebhookEventKind = "push"
	WebhookKindReviewComment WebhookEventKind = "pull_request_review_comment"
	WebhookKindReaction      WebhookEventKind = "reaction"
	WebhookKindCheckRun      WebhookEventKind = "check_run"
)

// WebhookEvent is a verified inbound event from the VCS. Exactly one of
// the pointer fields is populated, matched by Kind.
type WebhookEvent struct {
	Kind          WebhookEventKind
	DeliveryId    string
	PullRequest   *PullRequestPayload
	Push          *PushPayload
	ReviewComment *ReviewCommentPayload
	Reaction      *ReactionPayload
}

// PullRequestPayload is the subset of pull_request webhook fields we use.
type PullRequestPayload struct {
	Action   string
	Ref      PrRef
	Repo     RepoRef
	BaseSha  string
	IsDraft  bool
}

// PushPayload is the subset of push webhook fields we use.
type PushPayload struct {
	Repo     RepoRef
	Ref      string
	BeforeSha string
	HeadSha   string
}

// ReviewCommentPayload is fired for PR-level comments (including slash commands).
type ReviewCommentPayload struct {
	Ref      PrRef
	AuthorId string
	Body     string
	IsBot    bool
}

// ReactionPayload is fired when reactions are added to comments.
type ReactionPayload struct {
	CommentExternalId int64
	Reaction          string
	UserId            string
}

// UnifiedDiff is the diff for a single PR. Content is the raw unified-diff
// text; Files lists the files touched (parsed from the same source).
type UnifiedDiff struct {
	HeadSha string
	BaseSha string
	Content string
	Files   []DiffFile
}

// DiffFile is the per-file slice of a UnifiedDiff.
type DiffFile struct {
	Path   string
	Hunks  []DiffHunk
	Status string // "added" | "modified" | "renamed" | "deleted"
}

// DiffHunk is one @@ ... @@ section.
type DiffHunk struct {
	StartLine int // line number in the new file
	EndLine   int
	Content   string
}

// ChangedFile is returned by ListChangedFiles between two revisions.
type ChangedFile struct {
	Path   string
	Status string
}

// HumanComment is a review comment authored by a human, used as
// additional prompt context when enabled.
type HumanComment struct {
	ExternalId int64
	Author     string
	Body       string
	Path       string
	StartLine  int
	EndLine    int
	CreatedAt  time.Time
}

// ReviewPayload is what the bot posts. Comments are inline; Body is the
// summary text attached to the review object.
type ReviewPayload struct {
	Body     string
	Comments []BotComment
}

// BotComment is a single inline comment the bot posts.
type BotComment struct {
	File      string
	StartLine int
	EndLine   int
	Body      string
	Category  string // bug | security | style | suggestion | question
	Severity  string // high | medium | low
}

// PostedReview is the VCS's acknowledgment of a successful post.
type PostedReview struct {
	ReviewId   int64
	PostedAt   time.Time
	CommentIds []int64 // GitHub IDs for each posted inline comment, in order
}

// CheckResult is the status-check update for the head sha.
type CheckResult struct {
	Name       string
	Conclusion string // success | failure | neutral | timed_out
	Summary    string
	DetailsURL string
}

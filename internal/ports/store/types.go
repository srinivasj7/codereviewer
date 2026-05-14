// Package store defines the persistence ports. Each table family from
// the design has its own interface; adapters typically implement all of
// them against one backing database, but the split lets each pipeline
// depend only on what it uses.
package store

import (
	"time"

	"codereviewer/internal/ports"
)

// CommentId is the internal UUID-as-string for a review_comments row.
type CommentId string

// RuleId is the internal UUID-as-string for a rules row.
type RuleId string

// RunId is the internal UUID-as-string for a pr_runs row.
type RunId string

// Outcome classifies a comment's eventual fate. Updated by the feedback pipeline.
type Outcome string

const (
	OutcomePending   Outcome = "pending"
	OutcomeAccepted  Outcome = "accepted"
	OutcomeDismissed Outcome = "dismissed"
	OutcomeDiscussed Outcome = "discussed"
)

// OutcomeSignal records WHY the outcome was assigned.
type OutcomeSignal string

const (
	SignalImplicitLineChanged OutcomeSignal = "implicit-line-changed"
	SignalThumbsUp            OutcomeSignal = "thumbs-up"
	SignalThumbsDown          OutcomeSignal = "thumbs-down"
	SignalReplied             OutcomeSignal = "replied"
	SignalDismissed           OutcomeSignal = "dismissed"
)

// RunStatus is the terminal state of a pr_runs row.
type RunStatus string

const (
	RunStatusPending         RunStatus = "pending"
	RunStatusPosted          RunStatus = "posted"
	RunStatusFailedOpen      RunStatus = "failed-open"
	RunStatusBudgetExceeded  RunStatus = "budget-exceeded"
)

// BeginRun is the input to PrRunStore.Begin.
type BeginRun struct {
	Ref            ports.PrRef
	Trigger        ports.Trigger
	IdempotencyKey string
	StartedAt      time.Time
}

// RunResult is the input to PrRunStore.Finish.
type RunResult struct {
	Status         RunStatus
	ModelUsed      string
	TokensIn       int
	TokensOut      int
	CostUsd        float64
	FinishedAt     time.Time
	PostedReviewId int64
	Error          string
}

// PrRun is the persisted row.
type PrRun struct {
	RunId      RunId
	Ref        ports.PrRef
	Trigger    ports.Trigger
	Status     RunStatus
	ModelUsed  string
	TokensIn   int
	TokensOut  int
	CostUsd    float64
	StartedAt  time.Time
	FinishedAt time.Time
	Error      string
}

// CostCap is the effective cap for a (tenant, repo) pair.
type CostCap struct {
	DailyUsdCap   float64
	PerPrTokenCap int
}

// CodeChunkUpsert is the input to CodeChunkStore.UpsertMany.
type CodeChunkUpsert struct {
	ChunkId      string
	TenantId     ports.TenantId
	RepoId       ports.RepoId
	FilePath     string
	SymbolName   string
	SymbolKind   string
	StartLine    int
	EndLine      int
	Content      string
	ContentHash  string
	CommitSha    string
	Embedding    []float32
}

// CodeChunkHit is a retrieval result.
type CodeChunkHit struct {
	ChunkId    string
	FilePath   string
	SymbolName string
	StartLine  int
	EndLine    int
	Content    string
	Distance   float32 // cosine distance, lower is better
}

// SearchCodeChunks is the input to CodeChunkStore.SearchByEmbedding.
type SearchCodeChunks struct {
	RepoId            ports.RepoId
	Embedding         []float32
	K                 int
	SameFileBoostPath string
}

// CommentUpsert is the input to CommentStore.Upsert.
type CommentUpsert struct {
	TenantId      ports.TenantId
	RepoId        ports.RepoId
	PrNumber      int
	Source        string // "bot" | "human"
	GithubId      *int64
	FilePath      string
	StartLine     int
	EndLine       int
	DiffHunk      string
	CommentText   string
	Category      string
	Outcome       Outcome
	OutcomeSignal OutcomeSignal
	Embedding     []float32
}

// CommentHit is a retrieval result.
type CommentHit struct {
	CommentId   CommentId
	FilePath    string
	CommentText string
	Category    string
	Outcome     Outcome
	Distance    float32
}

// SearchComments is the input to CommentStore.SearchByEmbedding.
type SearchComments struct {
	RepoId    ports.RepoId
	Embedding []float32
	K         int
}

// Comment is the full persisted row.
type Comment struct {
	CommentId   CommentId
	TenantId    ports.TenantId
	RepoId      ports.RepoId
	PrNumber    int
	Source      string
	GithubId    *int64
	FilePath    string
	StartLine   int
	EndLine     int
	CommentText string
	Category    string
	Outcome     Outcome
	CreatedAt   time.Time
}

// RuleUpsert is the input to RuleStore.UpsertFromRepo.
type RuleUpsert struct {
	RuleId      RuleId
	TenantId    ports.TenantId
	Scope       string
	Title       string
	Description string
	Embedding   []float32
}

// Rule is the persisted row, retrieved for prompt context.
type Rule struct {
	RuleId      RuleId
	Scope       string
	Title       string
	Description string
	Enabled     bool
}

// FeedbackEvent records an outcome signal for a comment.
type FeedbackEvent struct {
	EventId    string
	TenantId   ports.TenantId
	CommentId  CommentId
	Signal     OutcomeSignal
	ObservedAt time.Time
}

// EmbeddingCacheEntry is one (hash, vector) pair.
type EmbeddingCacheEntry struct {
	Hash      string
	Embedding []float32
}

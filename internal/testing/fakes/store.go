package fakes

import (
	"context"
	"fmt"
	"sync"
	"time"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// RepoStore is an in-memory RepoStore.
type RepoStore struct {
	mu    sync.Mutex
	repos map[ports.RepoId]ports.RepoRef
}

// NewRepoStore returns an empty RepoStore.
func NewRepoStore() *RepoStore {
	return &RepoStore{repos: make(map[ports.RepoId]ports.RepoRef)}
}

// EnsureExists upserts.
func (s *RepoStore) EnsureExists(_ context.Context, repo ports.RepoRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.repos[repo.RepoId] = repo
	return nil
}

// Get returns the repo by id.
func (s *RepoStore) Get(_ context.Context, repoId ports.RepoId) (ports.RepoRef, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.repos[repoId]
	return r, ok, nil
}

// PrRunStore is an in-memory PrRunStore that tracks all runs by
// idempotency key. Used by smoke and budget tests to assert final state.
type PrRunStore struct {
	mu             sync.Mutex
	runs           map[store.RunId]store.PrRun
	idempotencyMap map[string]store.RunId
	finishResults  map[store.RunId]store.RunResult
	nextId         int
}

// NewPrRunStore returns an empty PrRunStore.
func NewPrRunStore() *PrRunStore {
	return &PrRunStore{
		runs:           make(map[store.RunId]store.PrRun),
		idempotencyMap: make(map[string]store.RunId),
		finishResults:  make(map[store.RunId]store.RunResult),
	}
}

// Begin honors idempotency by returning the existing run id with duplicate=true.
func (s *PrRunStore) Begin(_ context.Context, args store.BeginRun) (store.RunId, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.idempotencyMap[args.IdempotencyKey]; ok {
		return existing, true, nil
	}
	s.nextId++
	id := store.RunId(fmt.Sprintf("run-%d", s.nextId))
	s.runs[id] = store.PrRun{
		RunId:     id,
		Ref:       args.Ref,
		Trigger:   args.Trigger,
		Status:    store.RunStatusPending,
		StartedAt: args.StartedAt,
	}
	s.idempotencyMap[args.IdempotencyKey] = id
	return id, false, nil
}

// Finish records the terminal state on the run.
func (s *PrRunStore) Finish(_ context.Context, runId store.RunId, result store.RunResult) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	run, ok := s.runs[runId]
	if !ok {
		return fmt.Errorf("fake PrRunStore: unknown run %s", runId)
	}
	run.Status = result.Status
	run.ModelUsed = result.ModelUsed
	run.TokensIn = result.TokensIn
	run.TokensOut = result.TokensOut
	run.CostUsd = result.CostUsd
	run.FinishedAt = result.FinishedAt
	s.runs[runId] = run
	s.finishResults[runId] = result
	return nil
}

// GetRecent returns runs for a (repo, pr) sorted by start time descending.
func (s *PrRunStore) GetRecent(_ context.Context, repoId ports.RepoId, prNumber, limit int) ([]store.PrRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.PrRun
	for _, r := range s.runs {
		if r.Ref.RepoId == repoId && r.Ref.PrNumber == prNumber {
			out = append(out, r)
		}
	}
	if len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// AllRuns returns every recorded run. Test helper.
func (s *PrRunStore) AllRuns() []store.PrRun {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.PrRun, 0, len(s.runs))
	for _, r := range s.runs {
		out = append(out, r)
	}
	return out
}

// CostCapStore is an in-memory CostCapStore.
type CostCapStore struct {
	mu         sync.Mutex
	caps       map[string]store.CostCap
	spends     []SpendRecord
	DefaultCap store.CostCap
	GetErr     error
	SpendErr   error
}

// SpendRecord is one RecordSpend invocation.
type SpendRecord struct {
	TenantId ports.TenantId
	RepoId   ports.RepoId
	UsdAmt   float64
	At       time.Time
}

// NewCostCapStore returns a store with reasonable defaults.
func NewCostCapStore() *CostCapStore {
	return &CostCapStore{
		caps:       make(map[string]store.CostCap),
		DefaultCap: store.CostCap{DailyUsdCap: 5.00, PerPrTokenCap: 30000},
	}
}

// SetCap installs a per-repo cap.
func (c *CostCapStore) SetCap(tenantId ports.TenantId, repoId ports.RepoId, cap store.CostCap) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.caps[capKey(tenantId, repoId)] = cap
}

// GetEffective returns the installed cap or DefaultCap.
func (c *CostCapStore) GetEffective(_ context.Context, tenantId ports.TenantId, repoId ports.RepoId) (store.CostCap, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.GetErr != nil {
		return store.CostCap{}, c.GetErr
	}
	if cap, ok := c.caps[capKey(tenantId, repoId)]; ok {
		return cap, nil
	}
	return c.DefaultCap, nil
}

// RecordSpend appends a spend record.
func (c *CostCapStore) RecordSpend(_ context.Context, tenantId ports.TenantId, repoId ports.RepoId, usd float64, at time.Time) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.SpendErr != nil {
		return c.SpendErr
	}
	c.spends = append(c.spends, SpendRecord{TenantId: tenantId, RepoId: repoId, UsdAmt: usd, At: at})
	return nil
}

// TodaySpend sums all recorded spends for the (tenant, repo).
func (c *CostCapStore) TodaySpend(_ context.Context, tenantId ports.TenantId, repoId ports.RepoId, _ string) (float64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var total float64
	for _, s := range c.spends {
		if s.TenantId == tenantId && s.RepoId == repoId {
			total += s.UsdAmt
		}
	}
	return total, nil
}

// Spends returns a snapshot of all RecordSpend invocations.
func (c *CostCapStore) Spends() []SpendRecord {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make([]SpendRecord, len(c.spends))
	copy(out, c.spends)
	return out
}

func capKey(t ports.TenantId, r ports.RepoId) string { return fmt.Sprintf("%s:%s", t, r) }

// CodeChunkStore is a minimal in-memory CodeChunkStore. Slice 0 doesn't
// exercise retrieval, so SearchByEmbedding always returns empty.
type CodeChunkStore struct {
	mu     sync.Mutex
	chunks []store.CodeChunkUpsert
}

// NewCodeChunkStore returns an empty store.
func NewCodeChunkStore() *CodeChunkStore { return &CodeChunkStore{} }

// UpsertMany appends.
func (s *CodeChunkStore) UpsertMany(_ context.Context, chunks []store.CodeChunkUpsert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.chunks = append(s.chunks, chunks...)
	return nil
}

// SearchByEmbedding returns no hits in slice 0.
func (s *CodeChunkStore) SearchByEmbedding(_ context.Context, _ store.SearchCodeChunks) ([]store.CodeChunkHit, error) {
	return nil, nil
}

// SoftDeleteMissing always succeeds with 0.
func (s *CodeChunkStore) SoftDeleteMissing(_ context.Context, _ ports.RepoId, _ []string, _ time.Time) (int, error) {
	return 0, nil
}

// ExistsByContentHash checks against the in-memory chunk set.
func (s *CodeChunkStore) ExistsByContentHash(_ context.Context, _ ports.RepoId, hashes []string) (map[string]bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make(map[string]bool, len(hashes))
	existing := make(map[string]struct{})
	for _, c := range s.chunks {
		existing[c.ContentHash] = struct{}{}
	}
	for _, h := range hashes {
		_, ok := existing[h]
		out[h] = ok
	}
	return out, nil
}

// CommentStore is a minimal in-memory CommentStore.
type CommentStore struct {
	mu       sync.Mutex
	comments []store.Comment
	nextId   int
}

// NewCommentStore returns an empty store.
func NewCommentStore() *CommentStore { return &CommentStore{} }

// Upsert assigns an id and stores.
func (s *CommentStore) Upsert(_ context.Context, c store.CommentUpsert) (store.CommentId, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextId++
	id := store.CommentId(fmt.Sprintf("comment-%d", s.nextId))
	s.comments = append(s.comments, store.Comment{
		CommentId:   id,
		TenantId:    c.TenantId,
		RepoId:      c.RepoId,
		PrNumber:    c.PrNumber,
		Source:      c.Source,
		GithubId:    c.GithubId,
		FilePath:    c.FilePath,
		StartLine:   c.StartLine,
		EndLine:     c.EndLine,
		CommentText: c.CommentText,
		Category:    c.Category,
		Outcome:     c.Outcome,
		CreatedAt:   time.Now(),
	})
	return id, nil
}

// SearchByEmbedding returns empty in slice 0.
func (s *CommentStore) SearchByEmbedding(_ context.Context, _ store.SearchComments) ([]store.CommentHit, error) {
	return nil, nil
}

// UpdateOutcome updates the comment's outcome.
func (s *CommentStore) UpdateOutcome(_ context.Context, id store.CommentId, outcome store.Outcome, _ store.OutcomeSignal) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	for i := range s.comments {
		if s.comments[i].CommentId == id {
			s.comments[i].Outcome = outcome
			return nil
		}
	}
	return fmt.Errorf("fake CommentStore: unknown comment %s", id)
}

// ListByPr returns comments for a PR.
func (s *CommentStore) ListByPr(_ context.Context, ref ports.PrRef) ([]store.Comment, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.Comment
	for _, c := range s.comments {
		if c.RepoId == ref.RepoId && c.PrNumber == ref.PrNumber {
			out = append(out, c)
		}
	}
	return out, nil
}

// RuleStore is a minimal in-memory RuleStore.
type RuleStore struct {
	mu    sync.Mutex
	rules []store.Rule
}

// NewRuleStore returns an empty store.
func NewRuleStore() *RuleStore { return &RuleStore{} }

// UpsertFromRepo replaces the rule set.
func (s *RuleStore) UpsertFromRepo(_ context.Context, _ string, rules []store.RuleUpsert) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rules = make([]store.Rule, 0, len(rules))
	for _, r := range rules {
		s.rules = append(s.rules, store.Rule{
			RuleId:      r.RuleId,
			Scope:       r.Scope,
			Title:       r.Title,
			Description: r.Description,
			Enabled:     true,
		})
	}
	return nil
}

// ListForScope returns rules whose scope is "*" or matches one of paths.
// For the fake, scope matching is exact string equality; the production
// adapter implements glob matching.
func (s *RuleStore) ListForScope(_ context.Context, _ ports.RepoId, paths []string) ([]store.Rule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.Rule
	for _, r := range s.rules {
		if !r.Enabled {
			continue
		}
		if r.Scope == "*" {
			out = append(out, r)
			continue
		}
		for _, p := range paths {
			if r.Scope == p {
				out = append(out, r)
				break
			}
		}
	}
	return out, nil
}

// TombstoneMissing disables rules not in knownIds.
func (s *RuleStore) TombstoneMissing(_ context.Context, _ string, knownIds []store.RuleId) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	known := make(map[store.RuleId]struct{}, len(knownIds))
	for _, id := range knownIds {
		known[id] = struct{}{}
	}
	n := 0
	for i := range s.rules {
		if _, ok := known[s.rules[i].RuleId]; !ok && s.rules[i].Enabled {
			s.rules[i].Enabled = false
			n++
		}
	}
	return n, nil
}

// FeedbackStore is a minimal in-memory FeedbackStore.
type FeedbackStore struct {
	mu     sync.Mutex
	events []store.FeedbackEvent
}

// NewFeedbackStore returns an empty store.
func NewFeedbackStore() *FeedbackStore { return &FeedbackStore{} }

// Append records the event.
func (s *FeedbackStore) Append(_ context.Context, e store.FeedbackEvent) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, e)
	return nil
}

// ListForComment returns events for a comment id.
func (s *FeedbackStore) ListForComment(_ context.Context, id store.CommentId) ([]store.FeedbackEvent, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.FeedbackEvent
	for _, e := range s.events {
		if e.CommentId == id {
			out = append(out, e)
		}
	}
	return out, nil
}

// EmbeddingCache is a minimal in-memory cache.
type EmbeddingCache struct {
	mu      sync.Mutex
	entries map[string][]float32
}

// NewEmbeddingCache returns an empty cache.
func NewEmbeddingCache() *EmbeddingCache {
	return &EmbeddingCache{entries: make(map[string][]float32)}
}

// GetMany returns the cached vectors for the requested hashes.
func (c *EmbeddingCache) GetMany(_ context.Context, hashes []string) (map[string][]float32, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	out := make(map[string][]float32, len(hashes))
	for _, h := range hashes {
		if v, ok := c.entries[h]; ok {
			out[h] = v
		}
	}
	return out, nil
}

// PutMany stores entries.
func (c *EmbeddingCache) PutMany(_ context.Context, entries []store.EmbeddingCacheEntry) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for _, e := range entries {
		c.entries[e.Hash] = e.Embedding
	}
	return nil
}

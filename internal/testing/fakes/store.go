package fakes

import (
	"context"
	"fmt"
	"sort"
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

// EnsureExists upserts. Preserves Enabled if a row already exists so
// auto-registration on webhook never resurrects a disabled repo.
func (s *RepoStore) EnsureExists(_ context.Context, repo ports.RepoRef) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if existing, ok := s.repos[repo.RepoId]; ok {
		repo.Enabled = existing.Enabled
	} else {
		repo.Enabled = true
	}
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

// ListByTenant returns all repos belonging to tenant.
func (s *RepoStore) ListByTenant(_ context.Context, tenant ports.TenantId) ([]ports.RepoRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []ports.RepoRef
	for _, r := range s.repos {
		if r.TenantId == tenant {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].RepoId < out[j].RepoId })
	return out, nil
}

// SetEnabled toggles Enabled on the stored repo.
func (s *RepoStore) SetEnabled(_ context.Context, repoId ports.RepoId, enabled bool) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if r, ok := s.repos[repoId]; ok {
		r.Enabled = enabled
		s.repos[repoId] = r
	}
	return nil
}

// Tombstone is a no-op for the fake; tests assert on enabled flag.
func (s *RepoStore) Tombstone(_ context.Context, _ ports.RepoId) error { return nil }

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

// ListAcrossRepos returns the most recent runs across all repos for tenant.
func (s *PrRunStore) ListAcrossRepos(_ context.Context, tenant ports.TenantId, limit int) ([]store.PrRun, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []store.PrRun
	for _, r := range s.runs {
		if r.Ref.TenantId == tenant {
			out = append(out, r)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].StartedAt.After(out[j].StartedAt) })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out, nil
}

// GetByRunId returns one row by id.
func (s *PrRunStore) GetByRunId(_ context.Context, runId store.RunId) (store.PrRun, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.runs[runId]
	return r, ok, nil
}

// DeleteBefore removes runs older than cutoff.
func (s *PrRunStore) DeleteBefore(_ context.Context, cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var deleted int64
	for id, r := range s.runs {
		if r.StartedAt.Before(cutoff) {
			delete(s.runs, id)
			deleted++
		}
	}
	return deleted, nil
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

// GetByGithubId returns the first comment with a matching github_id.
func (s *CommentStore) GetByGithubId(_ context.Context, githubId int64) (store.Comment, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for _, c := range s.comments {
		if c.GithubId != nil && *c.GithubId == githubId {
			return c, true, nil
		}
	}
	return store.Comment{}, false, nil
}

// AllComments returns a snapshot of every upserted comment. Test helper.
func (s *CommentStore) AllComments() []store.Comment {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]store.Comment, len(s.comments))
	copy(out, s.comments)
	return out
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

// DeleteBefore removes events observed_at < cutoff.
func (s *FeedbackStore) DeleteBefore(_ context.Context, cutoff time.Time) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var kept []store.FeedbackEvent
	var deleted int64
	for _, e := range s.events {
		if e.ObservedAt.Before(cutoff) {
			deleted++
			continue
		}
		kept = append(kept, e)
	}
	s.events = kept
	return deleted, nil
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

// EvictToMax trims to maxRows; order is the map's iteration order
// (non-deterministic) which is good enough for unit tests that only
// care about size, not which keys survive.
func (c *EmbeddingCache) EvictToMax(_ context.Context, maxRows int) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if maxRows <= 0 || len(c.entries) <= maxRows {
		return 0, nil
	}
	toDelete := len(c.entries) - maxRows
	deleted := int64(0)
	for k := range c.entries {
		if deleted >= int64(toDelete) {
			break
		}
		delete(c.entries, k)
		deleted++
	}
	return deleted, nil
}

// ContextStore is a minimal in-memory ContextStore.
type ContextStore struct {
	mu      sync.Mutex
	sets    map[string]store.InstructionSet
	repoSet map[ports.RepoId]string
	prCtx   []store.PrContextItem
}

// NewContextStore returns an empty ContextStore.
func NewContextStore() *ContextStore {
	return &ContextStore{
		sets:    make(map[string]store.InstructionSet),
		repoSet: make(map[ports.RepoId]string),
	}
}

// UpsertInstructionSet stores by SetId; generates one if empty.
func (c *ContextStore) UpsertInstructionSet(_ context.Context, s store.InstructionSet) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if s.SetId == "" {
		s.SetId = fmt.Sprintf("set-%d", len(c.sets)+1)
	}
	s.UpdatedAt = time.Now()
	c.sets[s.SetId] = s
	return nil
}

// ListInstructionSets returns sets for the tenant, by name.
func (c *ContextStore) ListInstructionSets(_ context.Context, tenant ports.TenantId) ([]store.InstructionSet, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []store.InstructionSet
	for _, s := range c.sets {
		if s.TenantId == tenant {
			out = append(out, s)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// GetInstructionSet by id.
func (c *ContextStore) GetInstructionSet(_ context.Context, setId string) (store.InstructionSet, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.sets[setId]
	return s, ok, nil
}

// DeleteInstructionSet removes the set + any assignments to it.
func (c *ContextStore) DeleteInstructionSet(_ context.Context, setId string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.sets, setId)
	for r, s := range c.repoSet {
		if s == setId {
			delete(c.repoSet, r)
		}
	}
	return nil
}

// AssignSetToRepo links repo -> set.
func (c *ContextStore) AssignSetToRepo(_ context.Context, repoId ports.RepoId, setId string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.repoSet[repoId] = setId
	return nil
}

// UnassignFromRepo removes the link.
func (c *ContextStore) UnassignFromRepo(_ context.Context, repoId ports.RepoId) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	delete(c.repoSet, repoId)
	return nil
}

// GetSetForRepo joins repo -> set.
func (c *ContextStore) GetSetForRepo(_ context.Context, repoId ports.RepoId) (store.InstructionSet, bool, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	setId, ok := c.repoSet[repoId]
	if !ok {
		return store.InstructionSet{}, false, nil
	}
	s, ok := c.sets[setId]
	return s, ok, nil
}

// AppendPrContext stores an item; assigns ItemId if empty.
func (c *ContextStore) AppendPrContext(_ context.Context, item store.PrContextItem) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	if item.ItemId == "" {
		item.ItemId = fmt.Sprintf("ctx-%d", len(c.prCtx)+1)
	}
	if item.CreatedAt.IsZero() {
		item.CreatedAt = time.Now()
	}
	c.prCtx = append(c.prCtx, item)
	return nil
}

// ListPrContext returns items for the PR, newest first.
func (c *ContextStore) ListPrContext(_ context.Context, ref ports.PrRef) ([]store.PrContextItem, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []store.PrContextItem
	for _, it := range c.prCtx {
		if it.TenantId == ref.TenantId && it.RepoId == ref.RepoId && it.PrNumber == ref.PrNumber {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreatedAt.After(out[j].CreatedAt) })
	return out, nil
}

// DeletePrContextItem removes one item.
func (c *ContextStore) DeletePrContextItem(_ context.Context, itemId string) error {
	c.mu.Lock()
	defer c.mu.Unlock()
	for i, it := range c.prCtx {
		if it.ItemId == itemId {
			c.prCtx = append(c.prCtx[:i], c.prCtx[i+1:]...)
			return nil
		}
	}
	return nil
}

// DeletePrContextBefore removes items created_at < cutoff.
func (c *ContextStore) DeletePrContextBefore(_ context.Context, cutoff time.Time) (int64, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var kept []store.PrContextItem
	var deleted int64
	for _, it := range c.prCtx {
		if it.CreatedAt.Before(cutoff) {
			deleted++
			continue
		}
		kept = append(kept, it)
	}
	c.prCtx = kept
	return deleted, nil
}

// SettingsStore is a minimal in-memory SettingsStore.
type SettingsStore struct {
	mu       sync.Mutex
	settings map[string]store.Setting
}

// NewSettingsStore returns an empty store.
func NewSettingsStore() *SettingsStore {
	return &SettingsStore{settings: make(map[string]store.Setting)}
}

// Get returns the value for key.
func (s *SettingsStore) Get(_ context.Context, key string) (string, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	v, ok := s.settings[key]
	if !ok {
		return "", false, nil
	}
	return v.Value, true, nil
}

// GetAll returns every setting, ordered by key.
func (s *SettingsStore) GetAll(_ context.Context) ([]store.Setting, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	keys := make([]string, 0, len(s.settings))
	for k := range s.settings {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]store.Setting, 0, len(keys))
	for _, k := range keys {
		out = append(out, s.settings[k])
	}
	return out, nil
}

// Set upserts.
func (s *SettingsStore) Set(_ context.Context, key, value, updatedBy string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if updatedBy == "" {
		updatedBy = "system"
	}
	s.settings[key] = store.Setting{
		Key:       key,
		Value:     value,
		UpdatedAt: time.Now(),
		UpdatedBy: updatedBy,
	}
	return nil
}

// Delete removes the key.
func (s *SettingsStore) Delete(_ context.Context, key string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.settings, key)
	return nil
}

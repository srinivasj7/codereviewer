// Package review implements the per-PR review pipeline (design §6.1).
// It is constructed once at boot with the appropriate ports wired in and
// then invoked via Handle for each ReviewJob delivered by the bus.
package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"codereviewer/internal/core/budgets"
	"codereviewer/internal/core/llm"
	"codereviewer/internal/core/prompt"
	"codereviewer/internal/core/retrieval"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
)

// stopwatch records per-stage durations. Each Mark snapshots the time
// since the previous mark and stores it under the stage name. Kv emits
// them as slog key/value pairs for one-line latency summaries — useful
// for p95 measurement via log aggregation before OTel ships.
type stopwatch struct {
	clock     ports.Clock
	start     time.Time
	last      time.Time
	durations []stageDuration
}

type stageDuration struct {
	Name string
	Ms   int64
}

func newStopwatch(clock ports.Clock) *stopwatch {
	now := clock.Now()
	return &stopwatch{clock: clock, start: now, last: now}
}

func (s *stopwatch) Mark(stage string) {
	now := s.clock.Now()
	s.durations = append(s.durations, stageDuration{
		Name: stage,
		Ms:   now.Sub(s.last).Milliseconds(),
	})
	s.last = now
}

func (s *stopwatch) Total() time.Duration {
	return s.clock.Now().Sub(s.start)
}

func (s *stopwatch) Kv() []any {
	out := make([]any, 0, 2+len(s.durations)*2)
	out = append(out, "total_ms", s.Total().Milliseconds())
	for _, d := range s.durations {
		out = append(out, d.Name+"_ms", d.Ms)
	}
	return out
}

// Deps holds the pipeline's collaborators. Construct via NewPipeline.
type Deps struct {
	Vcs              ports.VcsSource
	Llm              ports.LlmGateway
	Clock            ports.Clock
	Obs              ports.Obs
	Repos            store.RepoStore
	CodeChunks       store.CodeChunkStore
	Comments         store.CommentStore
	Rules            store.RuleStore
	PrRuns           store.PrRunStore
	CostCaps         store.CostCapStore
	EmbeddingCache   store.EmbeddingCache
	ContextProviders []ports.ContextProvider
	TokenCap         int    // 0 = default
	SystemPrompt     string // empty = default
	EmbeddingModel   string // empty = adapter default
}

// Pipeline is the per-PR review use case.
type Pipeline struct {
	deps Deps
}

// NewPipeline applies defaults and returns a ready-to-run pipeline.
func NewPipeline(deps Deps) *Pipeline {
	if deps.TokenCap <= 0 {
		deps.TokenCap = budgets.DefaultPerPrTokenCap
	}
	if deps.SystemPrompt == "" {
		deps.SystemPrompt = prompt.DefaultSystemPrompt
	}
	return &Pipeline{deps: deps}
}

// Handle is the bus consumer entry point. It MUST ack or nack the
// delivery exactly once before returning.
func (p *Pipeline) Handle(ctx context.Context, payload []byte, cctx ports.ConsumeCtx) error {
	var job schemas.ReviewJob
	if err := json.Unmarshal(payload, &job); err != nil {
		_ = cctx.Nack(fmt.Sprintf("invalid review job: %v", err))
		return fmt.Errorf("unmarshal review job: %w", err)
	}
	if err := p.process(ctx, job); err != nil {
		p.deps.Obs.Logger.Error("review pipeline failed",
			"err", err.Error(),
			"pr_number", job.PrRef.PrNumber,
			"head_sha", job.PrRef.HeadSha,
		)
	}
	return cctx.Ack()
}

func (p *Pipeline) process(ctx context.Context, job schemas.ReviewJob) error {
	ref := job.PrRef
	sw := newStopwatch(p.deps.Clock)

	// Disabled repos are silently skipped. Webhook traffic still acks
	// (the gateway doesn't know the repo is disabled until this point);
	// the job consumes one delivery and exits.
	if p.deps.Repos != nil {
		if repo, found, err := p.deps.Repos.Get(ctx, ref.RepoId); err == nil && found && !repo.Enabled {
			p.deps.Obs.Logger.Info("repo disabled; skipping review",
				"repo_id", string(ref.RepoId), "pr_number", ref.PrNumber)
			return nil
		}
	}

	runId, dup, err := p.deps.PrRuns.Begin(ctx, store.BeginRun{
		Ref:            ref,
		Trigger:        job.Trigger,
		IdempotencyKey: job.IdempotencyKey(),
		StartedAt:      p.deps.Clock.Now(),
	})
	sw.Mark("begin")
	if err != nil {
		return fmt.Errorf("begin run: %w", err)
	}
	if dup {
		p.deps.Obs.Logger.Info("duplicate review job; skipping",
			"pr_number", ref.PrNumber, "head_sha", ref.HeadSha)
		return nil
	}

	costCap, err := p.deps.CostCaps.GetEffective(ctx, ref.TenantId, ref.RepoId)
	if err != nil {
		return p.failOpen(ctx, ref, runId, fmt.Errorf("get cost cap: %w", err))
	}
	spend, err := p.deps.CostCaps.TodaySpend(ctx, ref.TenantId, ref.RepoId, "UTC")
	if err != nil {
		return p.failOpen(ctx, ref, runId, fmt.Errorf("get today spend: %w", err))
	}
	sw.Mark("budget_check")
	if budgets.ExceedsDailyCap(spend, costCap.DailyUsdCap) {
		return p.postBudgetExceeded(ctx, ref, runId)
	}

	diff, err := p.deps.Vcs.FetchDiff(ctx, ref)
	sw.Mark("fetch_diff")
	if err != nil {
		return p.failOpen(ctx, ref, runId, fmt.Errorf("fetch diff: %w", err))
	}

	changedPaths := extractChangedFiles(diff.Content)
	var sameFile string
	if len(changedPaths) > 0 {
		sameFile = changedPaths[0]
	}

	cacheKey := "review-query:" + string(ref.RepoId) + ":" + ref.HeadSha
	queryEmbedding, embedErr := retrieval.EmbedQuery(
		ctx, p.deps.Llm, p.deps.EmbeddingCache, cacheKey, diff.Content, p.deps.EmbeddingModel,
	)
	if embedErr != nil {
		p.deps.Obs.Logger.Warn("retrieval embedding failed; reviewing without context",
			"err", embedErr.Error(), "pr_number", ref.PrNumber)
	}
	sw.Mark("embed_query")

	codeHits, err := retrieval.RetrieveCode(ctx, p.deps.CodeChunks, ref.RepoId, queryEmbedding, sameFile, 0)
	if err != nil {
		p.deps.Obs.Logger.Warn("code retrieval failed", "err", err.Error())
	}
	commentHits, err := retrieval.RetrieveComments(ctx, p.deps.Comments, ref.RepoId, queryEmbedding, 0)
	if err != nil {
		p.deps.Obs.Logger.Warn("comment retrieval failed", "err", err.Error())
	}
	ruleHits, err := retrieval.RetrieveRules(ctx, p.deps.Rules, ref.RepoId, changedPaths)
	if err != nil {
		p.deps.Obs.Logger.Warn("rule retrieval failed", "err", err.Error())
	}
	sw.Mark("retrieve")

	related := retrieval.FormatCode(codeHits)
	pastReviews := retrieval.FormatComments(commentHits)
	ruleStrings := retrieval.FormatRules(ruleHits)

	contextSections := p.fetchContext(ctx, ref)
	sw.Mark("fetch_context")

	tokenCap := p.deps.TokenCap
	if costCap.PerPrTokenCap > 0 && costCap.PerPrTokenCap < tokenCap {
		tokenCap = costCap.PerPrTokenCap
	}
	assembled := prompt.Assemble(prompt.Inputs{
		SystemPrompt:       p.deps.SystemPrompt,
		Diff:               diff.Content,
		RelatedCode:        related,
		PastReviews:        pastReviews,
		Context:            contextSections,
		Rules:              ruleStrings,
		ClosingInstruction: prompt.DefaultClosingInstruction,
	}, tokenCap, func(s string) int { return p.deps.Llm.EstimateTokens(s, "") })

	if assembled.DiffOverflow {
		p.deps.Obs.Logger.Warn("diff exceeds token cap; chunking not yet implemented",
			"pr_number", ref.PrNumber, "tokens_estimated", assembled.TokensEstimated)
	}

	resp, err := llm.ChatWithRetry(ctx, p.deps.Llm.Chat, ports.ChatRequest{
		SystemPrompt:    assembled.SystemPrompt,
		UserPrompt:      assembled.UserPrompt,
		MaxOutputTokens: budgets.MaxOutputTokens(tokenCap),
		ResponseFormat:  "json",
	}, llm.DefaultRetryPolicy)
	sw.Mark("llm_chat")
	if err != nil {
		return p.failOpen(ctx, ref, runId, fmt.Errorf("llm chat: %w", err))
	}

	raw, err := llm.ParseOutput(resp.Content)
	if err != nil {
		return p.failOpen(ctx, ref, runId, fmt.Errorf("parse llm output: %w", err))
	}

	botComments := make([]ports.BotComment, 0, len(raw))
	hasHighSeverity := false
	for _, c := range raw {
		botComments = append(botComments, ports.BotComment{
			File:      c.File,
			StartLine: c.StartLine,
			EndLine:   c.EndLine,
			Body:      c.Comment,
			Category:  c.Category,
			Severity:  c.Severity,
		})
		if c.Category == "bug" || c.Category == "security" {
			hasHighSeverity = true
		}
	}

	review := ports.ReviewPayload{
		Body:     summaryBody(len(botComments), hasHighSeverity),
		Comments: botComments,
	}
	posted, err := p.deps.Vcs.PostReview(ctx, ref, review)
	sw.Mark("post_review")
	if err != nil {
		return p.failOpen(ctx, ref, runId, fmt.Errorf("post review: %w", err))
	}

	conclusion := "success"
	if hasHighSeverity {
		conclusion = "failure"
	}
	if err := p.deps.Vcs.UpdateCheck(ctx, ref, ports.CheckResult{
		Name:       "code-review-bot/review",
		Conclusion: conclusion,
		Summary:    fmt.Sprintf("%d comments posted", len(botComments)),
	}); err != nil {
		return p.failOpen(ctx, ref, runId, fmt.Errorf("update check: %w", err))
	}
	sw.Mark("update_check")

	if err := p.deps.CostCaps.RecordSpend(ctx, ref.TenantId, ref.RepoId, resp.CostUsd, p.deps.Clock.Now()); err != nil {
		p.deps.Obs.Logger.Warn("record spend failed", "err", err.Error())
	}

	finishErr := p.deps.PrRuns.Finish(ctx, runId, store.RunResult{
		Status:         store.RunStatusPosted,
		ModelUsed:      resp.ModelUsed,
		TokensIn:       resp.TokensIn,
		TokensOut:      resp.TokensOut,
		CostUsd:        resp.CostUsd,
		FinishedAt:     p.deps.Clock.Now(),
		PostedReviewId: posted.ReviewId,
	})
	sw.Mark("finish")

	logKv := append([]any{
		"pr_number", ref.PrNumber,
		"head_sha", ref.HeadSha,
		"comments_posted", len(botComments),
		"tokens_in", resp.TokensIn,
		"tokens_out", resp.TokensOut,
		"cost_usd", resp.CostUsd,
		"model", resp.ModelUsed,
		"check_conclusion", conclusion,
		"retrieved_code", len(codeHits),
		"retrieved_comments", len(commentHits),
		"retrieved_rules", len(ruleHits),
		"dropped_sections", droppedNames(assembled.Dropped),
	}, sw.Kv()...)
	p.deps.Obs.Logger.Info("review completed", logKv...)
	return finishErr
}

// extractChangedFiles parses the unified-diff "+++ b/<path>" headers
// to enumerate the post-image file paths touched by the PR. Deleted
// files (header == "/dev/null") are skipped — there's nothing for
// the LLM to comment on in the after state.
func extractChangedFiles(diff string) []string {
	var paths []string
	seen := make(map[string]struct{})
	for _, line := range strings.Split(diff, "\n") {
		if !strings.HasPrefix(line, "+++ ") {
			continue
		}
		rest := strings.TrimSpace(strings.TrimPrefix(line, "+++ "))
		if rest == "/dev/null" {
			continue
		}
		rest = strings.TrimPrefix(rest, "b/")
		if rest == "" {
			continue
		}
		if _, ok := seen[rest]; ok {
			continue
		}
		seen[rest] = struct{}{}
		paths = append(paths, rest)
	}
	return paths
}

func droppedNames(sections []prompt.Section) []string {
	if len(sections) == 0 {
		return nil
	}
	out := make([]string, len(sections))
	for i, s := range sections {
		out[i] = s.String()
	}
	return out
}

func (p *Pipeline) postBudgetExceeded(ctx context.Context, ref ports.PrRef, runId store.RunId) error {
	if _, err := p.deps.Vcs.PostReview(ctx, ref, ports.ReviewPayload{
		Body: "Code review bot: daily budget exceeded; review skipped. The check passes by policy.",
	}); err != nil {
		p.deps.Obs.Logger.Warn("post neutral comment failed", "err", err.Error())
	}
	if err := p.deps.Vcs.UpdateCheck(ctx, ref, ports.CheckResult{
		Name:       "code-review-bot/review",
		Conclusion: "success",
		Summary:    "Budget exceeded; review skipped",
	}); err != nil {
		p.deps.Obs.Logger.Warn("update check failed", "err", err.Error())
	}
	return p.deps.PrRuns.Finish(ctx, runId, store.RunResult{
		Status:     store.RunStatusBudgetExceeded,
		FinishedAt: p.deps.Clock.Now(),
	})
}

func (p *Pipeline) failOpen(ctx context.Context, ref ports.PrRef, runId store.RunId, cause error) error {
	p.deps.Obs.Logger.Error("review failing open", "err", cause.Error())
	if _, err := p.deps.Vcs.PostReview(ctx, ref, ports.ReviewPayload{
		Body: "Code review bot: an error occurred; the check passes by policy.",
	}); err != nil {
		p.deps.Obs.Logger.Warn("post neutral comment failed", "err", err.Error())
	}
	if err := p.deps.Vcs.UpdateCheck(ctx, ref, ports.CheckResult{
		Name:       "code-review-bot/review",
		Conclusion: "success",
		Summary:    "Review unavailable",
	}); err != nil {
		p.deps.Obs.Logger.Warn("update check failed", "err", err.Error())
	}
	if err := p.deps.PrRuns.Finish(ctx, runId, store.RunResult{
		Status:     store.RunStatusFailedOpen,
		FinishedAt: p.deps.Clock.Now(),
		Error:      cause.Error(),
	}); err != nil {
		p.deps.Obs.Logger.Warn("finish run failed", "err", err.Error())
	}
	return cause
}

func summaryBody(n int, hasHighSeverity bool) string {
	if n == 0 {
		return "Reviewed; no comments."
	}
	if hasHighSeverity {
		return fmt.Sprintf("Reviewed; %d comments (bug/security flagged).", n)
	}
	return fmt.Sprintf("Reviewed; %d comments.", n)
}

// fetchContext invokes each configured ContextProvider in turn and
// flattens their items into the prompt's ContextSection list, sorted
// by Priority (descending). Providers are isolated: one provider's
// failure does not prevent others from contributing — that's the
// provider's own contract, but we add a defensive recover here too.
func (p *Pipeline) fetchContext(ctx context.Context, ref ports.PrRef) []prompt.ContextSection {
	if len(p.deps.ContextProviders) == 0 {
		return nil
	}
	type scored struct {
		section  prompt.ContextSection
		priority int
	}
	var collected []scored
	for _, cp := range p.deps.ContextProviders {
		items := p.safeFetch(ctx, cp, ref)
		for _, it := range items {
			if strings.TrimSpace(it.Body) == "" {
				continue
			}
			collected = append(collected, scored{
				section: prompt.ContextSection{
					Source: it.Source,
					Title:  it.Title,
					Body:   it.Body,
				},
				priority: it.Priority,
			})
		}
	}
	// Stable insertion sort by priority desc — small N (typically <20),
	// preserving provider order for equal priorities.
	for i := 1; i < len(collected); i++ {
		for j := i; j > 0 && collected[j].priority > collected[j-1].priority; j-- {
			collected[j], collected[j-1] = collected[j-1], collected[j]
		}
	}
	out := make([]prompt.ContextSection, len(collected))
	for i, c := range collected {
		out[i] = c.section
	}
	return out
}

func (p *Pipeline) safeFetch(ctx context.Context, cp ports.ContextProvider, ref ports.PrRef) (items []ports.ContextItem) {
	defer func() {
		if r := recover(); r != nil {
			p.deps.Obs.Logger.Error("context provider panicked",
				"provider", cp.Name(), "recover", fmt.Sprintf("%v", r))
			items = nil
		}
	}()
	items, err := cp.Fetch(ctx, ref)
	if err != nil {
		p.deps.Obs.Logger.Warn("context provider failed",
			"provider", cp.Name(), "err", err.Error())
		return nil
	}
	return items
}

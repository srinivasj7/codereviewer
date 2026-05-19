// Package review implements the per-PR review pipeline (design §6.1).
// It is constructed once at boot with the appropriate ports wired in and
// then invoked via Handle for each ReviewJob delivered by the bus.
package review

import (
	"context"
	"encoding/json"
	"fmt"
	"strconv"
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

	validLines := validRightLines(diff.Content)
	botComments := make([]ports.BotComment, 0, len(raw))
	hasBlocker := false
	dropped := 0
	for _, c := range raw {
		if !commentLinesValid(validLines, c.File, c.StartLine, c.EndLine) {
			dropped++
			p.deps.Obs.Logger.Warn("dropping inline comment with unresolvable line",
				"file", c.File, "start_line", c.StartLine, "end_line", c.EndLine,
				"pr_number", ref.PrNumber)
			continue
		}
		botComments = append(botComments, ports.BotComment{
			File:      c.File,
			StartLine: c.StartLine,
			EndLine:   c.EndLine,
			Body:      c.Comment,
			Category:  c.Category,
			Severity:  c.Severity,
		})
		if c.Category == "bug" || c.Category == "security" || c.Severity == "high" {
			hasBlocker = true
		}
	}

	review := ports.ReviewPayload{
		Body:     summaryBody(len(botComments), hasBlocker),
		Comments: botComments,
	}
	posted, err := p.deps.Vcs.PostReview(ctx, ref, review)
	sw.Mark("post_review")
	if err != nil {
		return p.failOpen(ctx, ref, runId, fmt.Errorf("post review: %w", err))
	}

	// Persist bot comments so the feedback worker can attach reactions/replies
	// by GithubId, and so retrieval can surface them on future PRs. Failures
	// here are non-fatal: the GitHub-side review is already up.
	for i, c := range botComments {
		var githubId *int64
		if i < len(posted.CommentIds) {
			id := posted.CommentIds[i]
			githubId = &id
		}
		if _, err := p.deps.Comments.Upsert(ctx, store.CommentUpsert{
			TenantId:    ref.TenantId,
			RepoId:      ref.RepoId,
			PrNumber:    ref.PrNumber,
			Source:      "bot",
			GithubId:    githubId,
			FilePath:    c.File,
			StartLine:   c.StartLine,
			EndLine:     c.EndLine,
			CommentText: c.Body,
			Category:    c.Category,
		}); err != nil {
			p.deps.Obs.Logger.Warn("persist bot comment failed", "err", err.Error())
		}
	}

	conclusion := "success"
	if hasBlocker {
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
		"comments_dropped_invalid_line", dropped,
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

// validRightLines parses a unified diff and returns, for each file in
// the post-image, the set of line numbers on the RIGHT side that an
// inline review comment may legally anchor to (added or context lines
// within a hunk). The LLM occasionally cites lines outside any hunk
// — GitHub returns 422 for the whole batch when that happens.
func validRightLines(diff string) map[string]map[int]bool {
	out := make(map[string]map[int]bool)
	var path string
	var line int
	for _, l := range strings.Split(diff, "\n") {
		switch {
		case strings.HasPrefix(l, "+++ "):
			p := strings.TrimSpace(strings.TrimPrefix(l, "+++ "))
			if p == "/dev/null" {
				path = ""
				continue
			}
			p = strings.TrimPrefix(p, "b/")
			path = p
			if path != "" {
				out[path] = make(map[int]bool)
			}
		case strings.HasPrefix(l, "@@"):
			start, ok := parseHunkRightStart(l)
			if !ok {
				continue
			}
			line = start
		case strings.HasPrefix(l, "+") && !strings.HasPrefix(l, "+++"):
			if path != "" {
				out[path][line] = true
			}
			line++
		case strings.HasPrefix(l, "-") && !strings.HasPrefix(l, "---"):
			// deleted line — does not advance the right-side cursor.
		case strings.HasPrefix(l, " "):
			if path != "" {
				out[path][line] = true
			}
			line++
		}
	}
	return out
}

// parseHunkRightStart pulls the new-file starting line out of a hunk
// header like "@@ -10,5 +20,7 @@ context". Returns 20, true for that
// example.
func parseHunkRightStart(hdr string) (int, bool) {
	plus := strings.Index(hdr, "+")
	if plus < 0 {
		return 0, false
	}
	rest := hdr[plus+1:]
	end := strings.IndexAny(rest, ", ")
	if end < 0 {
		return 0, false
	}
	n, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0, false
	}
	return n, true
}

// commentLinesValid reports whether the (file, start..end) span the
// LLM emitted is reachable by an inline comment on the post-image.
// A zero StartLine means single-line at EndLine.
func commentLinesValid(valid map[string]map[int]bool, file string, start, end int) bool {
	lines := valid[file]
	if lines == nil {
		return false
	}
	if end <= 0 || !lines[end] {
		return false
	}
	if start > 0 && start != end && !lines[start] {
		return false
	}
	return true
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

func summaryBody(n int, hasBlocker bool) string {
	if n == 0 {
		return "Reviewed; no comments."
	}
	if hasBlocker {
		return fmt.Sprintf("Reviewed; %d comments (bug/security or high-severity flagged).", n)
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

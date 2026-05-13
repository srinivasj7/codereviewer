// Package smoke contains end-to-end tests that wire the full review
// pipeline against fakes and assert on observable side effects.
package smoke

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codereviewer/internal/adapters/busmem"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/schemas"
	"codereviewer/internal/testing/fixtures"
	"codereviewer/internal/testing/harness"
)

const (
	testTenant = ports.TenantId("tenant-1")
	testRepo   = ports.RepoId("repo-1")
)

func testRef() ports.PrRef {
	return ports.PrRef{
		TenantId: testTenant,
		RepoId:   testRepo,
		PrNumber: 42,
		HeadSha:  "abc123",
	}
}

// publish wires busmem + pipeline + publishes a job synchronously.
// busmem.Publish invokes the handler in the caller's goroutine, so by
// the time it returns the pipeline has finished.
func publish(t *testing.T, h *harness.Harness, ref ports.PrRef) {
	t.Helper()
	bus := busmem.New()
	pipeline := h.ReviewPipeline()
	sub, err := bus.Consume(context.Background(), ports.QueueReview, pipeline.Handle)
	require.NoError(t, err)
	defer func() { _ = sub.Stop() }()

	job := schemas.ReviewJob{PrRef: ref, Trigger: ports.TriggerPrOpened}
	body, err := json.Marshal(job)
	require.NoError(t, err)
	require.NoError(t, bus.Publish(context.Background(), ports.QueueReview, body, ports.PublishOpts{
		IdempotencyKey: job.IdempotencyKey(),
	}))
}

func TestReviewPipeline_SuccessPath(t *testing.T) {
	h := harness.New()
	h.Llm.SetChatResponse(ports.ChatResponse{
		Content:   fixtures.SmokeSingleSuggestion,
		TokensIn:  1234,
		TokensOut: 56,
		CostUsd:   0.02,
		ModelUsed: "fake-primary",
	})
	ref := testRef()
	h.Vcs.SetDiff(ref, fixtures.SmokeDiff())

	publish(t, h, ref)

	// PostReview called once with the LLM's comment
	posts := h.Vcs.PostReviews()
	require.Len(t, posts, 1)
	assert.Equal(t, ref, posts[0].Ref)
	require.Len(t, posts[0].Review.Comments, 1)
	assert.Equal(t, "suggestion", posts[0].Review.Comments[0].Category)
	assert.Equal(t, "src/handler.ts", posts[0].Review.Comments[0].File)

	// Check passes (no bug/security categories)
	checks := h.Vcs.UpdateChecks()
	require.Len(t, checks, 1)
	assert.Equal(t, "success", checks[0].Result.Conclusion)

	// Run recorded with token + cost numbers
	runs := h.PrRuns.AllRuns()
	require.Len(t, runs, 1)
	assert.Equal(t, store.RunStatusPosted, runs[0].Status)
	assert.Greater(t, runs[0].TokensIn, 0)
	assert.Greater(t, runs[0].CostUsd, 0.0)
	assert.Equal(t, "fake-primary", runs[0].ModelUsed)

	// Spend recorded for billing
	spends := h.CostCaps.Spends()
	require.Len(t, spends, 1)
	assert.InDelta(t, 0.02, spends[0].UsdAmt, 0.0001)
}

func TestReviewPipeline_BugCategoryFailsCheck(t *testing.T) {
	h := harness.New()
	h.Llm.SetChatResponse(ports.ChatResponse{
		Content:   fixtures.SmokeWithBug,
		TokensIn:  500,
		TokensOut: 20,
		CostUsd:   0.01,
		ModelUsed: "fake-primary",
	})
	ref := testRef()
	h.Vcs.SetDiff(ref, fixtures.SmokeDiff())

	publish(t, h, ref)

	checks := h.Vcs.UpdateChecks()
	require.Len(t, checks, 1)
	assert.Equal(t, "failure", checks[0].Result.Conclusion,
		"a bug-category comment should fail the status check")
}

func TestReviewPipeline_BudgetExceeded_SkipsLLM(t *testing.T) {
	h := harness.New()
	h.CostCaps.SetCap(testTenant, testRepo, store.CostCap{
		DailyUsdCap:   0,
		PerPrTokenCap: 30000,
	})
	ref := testRef()
	h.Vcs.SetDiff(ref, fixtures.SmokeDiff())

	publish(t, h, ref)

	// LLM was never called
	assert.Empty(t, h.Llm.ChatCalls(),
		"expected zero LLM calls when budget is exhausted")

	// A neutral comment was posted (no inline comments)
	posts := h.Vcs.PostReviews()
	require.Len(t, posts, 1)
	assert.Empty(t, posts[0].Review.Comments)
	assert.Contains(t, posts[0].Review.Body, "budget")

	// Check passes by policy
	checks := h.Vcs.UpdateChecks()
	require.Len(t, checks, 1)
	assert.Equal(t, "success", checks[0].Result.Conclusion)

	// Run recorded with budget-exceeded
	runs := h.PrRuns.AllRuns()
	require.Len(t, runs, 1)
	assert.Equal(t, store.RunStatusBudgetExceeded, runs[0].Status)

	// No spend recorded
	assert.Empty(t, h.CostCaps.Spends())
}

func TestReviewPipeline_DuplicateJob_BusLevelDedup(t *testing.T) {
	h := harness.New()
	h.Llm.SetChatResponse(ports.ChatResponse{
		Content:   fixtures.SmokeSingleSuggestion,
		TokensIn:  100,
		TokensOut: 50,
		CostUsd:   0.01,
		ModelUsed: "fake-primary",
	})
	ref := testRef()
	h.Vcs.SetDiff(ref, fixtures.SmokeDiff())

	bus := busmem.New()
	pipeline := h.ReviewPipeline()
	sub, err := bus.Consume(context.Background(), ports.QueueReview, pipeline.Handle)
	require.NoError(t, err)
	defer func() { _ = sub.Stop() }()

	job := schemas.ReviewJob{PrRef: ref, Trigger: ports.TriggerPrOpened}
	body, _ := json.Marshal(job)
	opts := ports.PublishOpts{IdempotencyKey: job.IdempotencyKey()}

	require.NoError(t, bus.Publish(context.Background(), ports.QueueReview, body, opts))
	require.NoError(t, bus.Publish(context.Background(), ports.QueueReview, body, opts))

	// Bus-level dedup: handler called exactly once
	assert.Len(t, h.Llm.ChatCalls(), 1)
	assert.Len(t, h.Vcs.PostReviews(), 1)
}

func TestReviewPipeline_LlmFailure_FailsOpen(t *testing.T) {
	h := harness.New()
	h.Llm.SetChatErr(assertSomeErr())
	ref := testRef()
	h.Vcs.SetDiff(ref, fixtures.SmokeDiff())

	publish(t, h, ref)

	// Neutral comment + check passes
	posts := h.Vcs.PostReviews()
	require.Len(t, posts, 1)
	assert.Empty(t, posts[0].Review.Comments)

	checks := h.Vcs.UpdateChecks()
	require.Len(t, checks, 1)
	assert.Equal(t, "success", checks[0].Result.Conclusion)

	// pr_runs marked failed-open
	runs := h.PrRuns.AllRuns()
	require.Len(t, runs, 1)
	assert.Equal(t, store.RunStatusFailedOpen, runs[0].Status)
}

// assertSomeErr returns a sentinel error; using a helper keeps the test
// signatures clean even though Go has no "any error" sugar.
func assertSomeErr() error { return errLlmDown }

var errLlmDown = &simpleErr{"llm: simulated outage"}

type simpleErr struct{ msg string }

func (e *simpleErr) Error() string { return e.msg }

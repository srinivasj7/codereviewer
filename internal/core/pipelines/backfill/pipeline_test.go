package backfill

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/testing/fakes"
)

func newTestDeps() (*Pipeline, *fakes.Vcs, *fakes.Llm, *fakes.CommentStore, *fakes.EmbeddingCache) {
	vcs := fakes.NewVcs()
	llm := fakes.NewLlm()
	comments := fakes.NewCommentStore()
	cache := fakes.NewEmbeddingCache()
	p := NewPipeline(Deps{
		Vcs:            vcs,
		Llm:            llm,
		Obs:            obsForTest(),
		Comments:       comments,
		EmbeddingCache: cache,
	})
	return p, vcs, llm, comments, cache
}

func obsForTest() ports.Obs {
	// The smoke test harness brings up obsstdout; tests in this package
	// don't need that bulk. A no-op Obs is enough.
	return ports.Obs{
		Tracer: noopTracer{},
		Meter:  noopMeter{},
		Logger: noopLogger{},
	}
}

type noopTracer struct{}
type noopSpan struct{}
type noopMeter struct{}
type noopCounter struct{}
type noopHistogram struct{}
type noopLogger struct{}

func (noopTracer) StartSpan(ctx context.Context, _ string, _ ...ports.Attr) (context.Context, ports.Span) {
	return ctx, noopSpan{}
}
func (noopSpan) SetAttribute(string, any)                        {}
func (noopSpan) RecordError(error)                                {}
func (noopSpan) End()                                             {}
func (noopMeter) Counter(string) ports.Counter                    { return noopCounter{} }
func (noopMeter) Histogram(string) ports.Histogram                { return noopHistogram{} }
func (noopCounter) Add(context.Context, int64, ...ports.Attr)     {}
func (noopHistogram) Record(context.Context, float64, ...ports.Attr) {}
func (noopLogger) Info(string, ...any)                            {}
func (noopLogger) Warn(string, ...any)                            {}
func (noopLogger) Error(string, ...any)                           {}

func TestBackfill_IngestsCommentsWithOutcomeFromReactions(t *testing.T) {
	pipeline, vcs, _, comments, _ := newTestDeps()

	repoId := ports.RepoId("octo/repo")
	vcs.SetClosedPrs([]int{101, 102})

	vcs.SetPrComments(ports.PrRef{RepoId: repoId, PrNumber: 101}, []ports.HumanComment{
		{ExternalId: 1, Body: "ship it", Path: "a.ts", StartLine: 1, EndLine: 1,
			ReactionsPlusOne: 2, ReactionsMinusOne: 0},
		{ExternalId: 2, Body: "nit: rename", Path: "a.ts", StartLine: 5, EndLine: 5,
			ReactionsMinusOne: 1},
	})
	vcs.SetPrComments(ports.PrRef{RepoId: repoId, PrNumber: 102}, []ports.HumanComment{
		{ExternalId: 3, Body: "consider a test", Path: "b.ts", StartLine: 10, EndLine: 12,
			DiffHunk: "@@ -10,3 +10,5 @@"},
	})

	n, err := pipeline.Run(context.Background(), Args{
		TenantId: "tenant-a", RepoId: repoId, WindowDays: 30, Now: time.Now(),
	})
	require.NoError(t, err)
	assert.Equal(t, 3, n)

	upserted := comments.AllComments()
	require.Len(t, upserted, 3)

	byId := map[int64]store.Comment{}
	for _, c := range upserted {
		require.NotNil(t, c.GithubId)
		byId[*c.GithubId] = c
	}
	assert.Equal(t, store.OutcomeAccepted, byId[1].Outcome, "thumbs-up wins")
	assert.Equal(t, store.OutcomeDismissed, byId[2].Outcome, "thumbs-down wins")
	assert.Equal(t, store.OutcomePending, byId[3].Outcome, "no reactions stays pending")
}

func TestBackfill_EmbedsViaCacheOnce(t *testing.T) {
	pipeline, vcs, llm, _, _ := newTestDeps()

	repoId := ports.RepoId("octo/repo")
	vcs.SetClosedPrs([]int{200, 201})
	identical := ports.HumanComment{
		ExternalId: 1, Body: "same text", Path: "a.ts", DiffHunk: "same hunk",
	}
	// Two PRs, one comment each with IDENTICAL body+hunk. Hash dedup
	// should mean only one embedding call.
	vcs.SetPrComments(ports.PrRef{RepoId: repoId, PrNumber: 200}, []ports.HumanComment{identical})
	identicalCopy := identical
	identicalCopy.ExternalId = 2
	vcs.SetPrComments(ports.PrRef{RepoId: repoId, PrNumber: 201}, []ports.HumanComment{identicalCopy})

	_, err := pipeline.Run(context.Background(), Args{
		TenantId: "tenant-a", RepoId: repoId, WindowDays: 30, Now: time.Now(),
	})
	require.NoError(t, err)

	// First PR triggers one embed call (one unique hash); second PR
	// finds the hash already cached and skips. Net: one Embed call.
	calls := llm.EmbedCalls()
	require.Len(t, calls, 1, "duplicate content_hash across PRs should hit the cache")
	assert.Len(t, calls[0].Texts, 1)
}

func TestBackfill_EmptyBodyAndHunkSkipped(t *testing.T) {
	pipeline, vcs, _, comments, _ := newTestDeps()
	repoId := ports.RepoId("octo/repo")
	vcs.SetClosedPrs([]int{301})
	vcs.SetPrComments(ports.PrRef{RepoId: repoId, PrNumber: 301}, []ports.HumanComment{
		{ExternalId: 10, Body: "", DiffHunk: "", Path: "a.ts"},
		{ExternalId: 11, Body: "real comment", Path: "a.ts"},
	})
	n, err := pipeline.Run(context.Background(), Args{
		TenantId: "t", RepoId: repoId, WindowDays: 30, Now: time.Now(),
	})
	require.NoError(t, err)
	assert.Equal(t, 1, n)
	assert.Len(t, comments.AllComments(), 1)
}

func TestExtractFn_OutcomeFromReactions(t *testing.T) {
	cases := []struct {
		plus, minus int
		wantOutcome store.Outcome
		wantSignal  store.OutcomeSignal
	}{
		{2, 0, store.OutcomeAccepted, store.SignalThumbsUp},
		{0, 1, store.OutcomeDismissed, store.SignalThumbsDown},
		{1, 1, store.OutcomePending, ""},
		{0, 0, store.OutcomePending, ""},
	}
	for _, tc := range cases {
		o, sig := outcomeFromReactions(tc.plus, tc.minus)
		assert.Equal(t, tc.wantOutcome, o)
		assert.Equal(t, tc.wantSignal, sig)
	}
}

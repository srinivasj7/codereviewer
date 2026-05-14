package admin

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"codereviewer/internal/adapters/obsstdout"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
	"codereviewer/internal/testing/fakes"
)

func TestJanitor_DeletesOldPrRunsAndFeedback(t *testing.T) {
	runs := fakes.NewPrRunStore()
	feedback := fakes.NewFeedbackStore()
	contextStore := fakes.NewContextStore()
	cache := fakes.NewEmbeddingCache()

	// Seed an old run by Begin + Finish; the fake stamps StartedAt from
	// args. Use a past time.
	pastRef := ports.PrRef{TenantId: "t", RepoId: "r", PrNumber: 1, HeadSha: "a"}
	oldStart := time.Now().Add(-30 * 24 * time.Hour)
	_, _, err := runs.Begin(context.Background(), store.BeginRun{
		Ref: pastRef, Trigger: ports.TriggerPrOpened, IdempotencyKey: "k1", StartedAt: oldStart,
	})
	require.NoError(t, err)
	// And one recent.
	recentRef := ports.PrRef{TenantId: "t", RepoId: "r", PrNumber: 2, HeadSha: "b"}
	_, _, err = runs.Begin(context.Background(), store.BeginRun{
		Ref: recentRef, Trigger: ports.TriggerPrOpened, IdempotencyKey: "k2", StartedAt: time.Now(),
	})
	require.NoError(t, err)

	require.NoError(t, feedback.Append(context.Background(), store.FeedbackEvent{
		EventId: "e1", TenantId: "t", CommentId: "c1",
		Signal: store.SignalThumbsUp, ObservedAt: oldStart,
	}))
	require.NoError(t, feedback.Append(context.Background(), store.FeedbackEvent{
		EventId: "e2", TenantId: "t", CommentId: "c2",
		Signal: store.SignalThumbsUp, ObservedAt: time.Now(),
	}))

	j := &Janitor{
		PrRuns:         runs,
		Feedback:       feedback,
		Context:        contextStore,
		EmbeddingCache: cache,
		PrRunsDays:     7,
		FeedbackDays:   7,
		PrContextDays:  7,
		CacheMaxRows:   0,
		Obs:            obsstdout.New("test").Logger,
	}
	j.sweep(context.Background())

	got := runs.AllRuns()
	require.Len(t, got, 1, "old pr_run should be deleted")
	require.Equal(t, 2, got[0].Ref.PrNumber)

	fe, err := feedback.ListForComment(context.Background(), "c1")
	require.NoError(t, err)
	require.Empty(t, fe, "old feedback should be deleted")
	fe, err = feedback.ListForComment(context.Background(), "c2")
	require.NoError(t, err)
	require.Len(t, fe, 1)
}

func TestJanitor_EvictsEmbeddingCache(t *testing.T) {
	cache := fakes.NewEmbeddingCache()
	for i := 0; i < 10; i++ {
		require.NoError(t, cache.PutMany(context.Background(), []store.EmbeddingCacheEntry{
			{Hash: rune2hex(i), Embedding: []float32{1, 2, 3}},
		}))
	}
	j := &Janitor{
		EmbeddingCache: cache,
		CacheMaxRows:   3,
		Obs:            obsstdout.New("test").Logger,
	}
	j.sweep(context.Background())

	got, err := cache.GetMany(context.Background(), []string{
		rune2hex(0), rune2hex(1), rune2hex(2), rune2hex(3), rune2hex(4),
		rune2hex(5), rune2hex(6), rune2hex(7), rune2hex(8), rune2hex(9),
	})
	require.NoError(t, err)
	require.Len(t, got, 3, "cache should be trimmed to CacheMaxRows")
}

func rune2hex(i int) string {
	return string(rune('a' + i))
}

func TestRotateExportFiles(t *testing.T) {
	dir := t.TempDir()
	// Create 5 config-* files with monotonically increasing mtime.
	for i := 0; i < 5; i++ {
		path := filepath.Join(dir, "config-"+rune2hex(i)+".toml")
		require.NoError(t, os.WriteFile(path, []byte("x"), 0o644))
		mt := time.Now().Add(time.Duration(i) * time.Second)
		require.NoError(t, os.Chtimes(path, mt, mt))
	}
	deleted, err := rotateExportFiles(dir, 3)
	require.NoError(t, err)
	require.Equal(t, 2, deleted, "should delete the oldest 2")

	entries, _ := os.ReadDir(dir)
	require.Len(t, entries, 3)
}

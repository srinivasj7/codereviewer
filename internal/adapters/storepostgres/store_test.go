package storepostgres

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"testing"

	_ "github.com/jackc/pgx/v5/stdlib"
	"github.com/pressly/goose/v3"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"codereviewer/internal/db"
	"codereviewer/internal/ports"
	"codereviewer/internal/ports/store"
)

// Integration tests for storepostgres. They require a running Postgres
// reachable via TESTS_POSTGRES_URL; absent that, each test self-skips.
//
// Bring up Postgres with `docker compose up postgres`, then run
//   TESTS_POSTGRES_URL="postgres://postgres:dev@localhost:5432/codereviewer?sslmode=disable" \
//     go test ./internal/adapters/storepostgres/...

var (
	testStores *Stores
	testCtx    = context.Background()
)

func TestMain(m *testing.M) {
	url := os.Getenv("TESTS_POSTGRES_URL")
	if url != "" {
		if err := setupTestDB(url); err != nil {
			fmt.Fprintln(os.Stderr, "test setup:", err)
			os.Exit(1)
		}
	}
	code := m.Run()
	if testStores != nil {
		testStores.Close()
	}
	os.Exit(code)
}

func setupTestDB(url string) error {
	sub, err := fs.Sub(db.Migrations, "migrations")
	if err != nil {
		return fmt.Errorf("migrations fs: %w", err)
	}
	goose.SetBaseFS(sub)
	if err := goose.SetDialect("postgres"); err != nil {
		return fmt.Errorf("dialect: %w", err)
	}
	sqlDB, err := goose.OpenDBWithDriver("pgx", url)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer sqlDB.Close()
	if err := goose.UpContext(testCtx, sqlDB, "."); err != nil {
		return fmt.Errorf("migrate up: %w", err)
	}

	pool, err := NewPool(testCtx, url)
	if err != nil {
		return fmt.Errorf("pool: %w", err)
	}
	testStores = NewStores(pool)
	return nil
}

// requireDB skips a test with a clear message when TESTS_POSTGRES_URL
// isn't set, and truncates all tables so each test runs against a
// known-clean state.
func requireDB(t *testing.T) *Stores {
	t.Helper()
	if testStores == nil {
		t.Skip("storepostgres integration tests: set TESTS_POSTGRES_URL to enable")
	}
	truncateAll(t)
	return testStores
}

func truncateAll(t *testing.T) {
	t.Helper()
	// CASCADE handles the FKs so order doesn't matter, but listing
	// children first keeps the intent explicit.
	tables := []string{
		"feedback_events", "code_chunks", "review_comments", "rules",
		"pr_runs", "cost_caps", "embedding_cache", "job_idempotency",
		"pr_context_items", "repo_instruction_sets", "instruction_sets",
		"app_settings", "repos", "tenants",
	}
	for _, table := range tables {
		_, err := testStores.Pool.Exec(testCtx, "TRUNCATE TABLE "+table+" CASCADE")
		require.NoError(t, err, "truncate %s", table)
	}
}

func TestRepoStore_EnsureExists_Idempotent(t *testing.T) {
	s := requireDB(t)
	repo := ports.RepoRef{
		TenantId:      "tenant-a",
		RepoId:        "octocat/hello-world",
		Owner:         "octocat",
		Name:          "hello-world",
		DefaultBranch: "main",
	}
	require.NoError(t, s.Repos.EnsureExists(testCtx, repo))
	// Idempotency: second call must succeed without error.
	require.NoError(t, s.Repos.EnsureExists(testCtx, repo))

	got, found, err := s.Repos.Get(testCtx, repo.RepoId)
	require.NoError(t, err)
	require.True(t, found)
	assert.Equal(t, "octocat", got.Owner)
	assert.Equal(t, "main", got.DefaultBranch)

	// Default branch rename is reflected.
	repo.DefaultBranch = "trunk"
	require.NoError(t, s.Repos.EnsureExists(testCtx, repo))
	got, _, _ = s.Repos.Get(testCtx, repo.RepoId)
	assert.Equal(t, "trunk", got.DefaultBranch)
}

func TestRepoStore_Provider_RoundTrips(t *testing.T) {
	s := requireDB(t)

	// A repo written before slice 6B (no Provider set) reads back as
	// "github" because migration 008's DEFAULT clause + the
	// EnsureExists NULLIF('','github') guard converge there.
	pre6B := ports.RepoRef{
		TenantId: "tenant-a", RepoId: "legacy/repo",
		Owner: "legacy", Name: "repo", DefaultBranch: "main",
	}
	require.NoError(t, s.Repos.EnsureExists(testCtx, pre6B))
	got, _, err := s.Repos.Get(testCtx, pre6B.RepoId)
	require.NoError(t, err)
	assert.Equal(t, ports.VcsProviderGitHub, got.Provider, "empty Provider must default to github")

	// A bitbucket repo round-trips and stays bitbucket across
	// subsequent EnsureExists calls.
	bb := ports.RepoRef{
		TenantId: "tenant-a", RepoId: "bbteam/svc",
		Owner: "bbteam", Name: "svc", DefaultBranch: "main",
		Provider: ports.VcsProviderBitbucket,
	}
	require.NoError(t, s.Repos.EnsureExists(testCtx, bb))
	got, _, err = s.Repos.Get(testCtx, bb.RepoId)
	require.NoError(t, err)
	assert.Equal(t, ports.VcsProviderBitbucket, got.Provider)

	require.NoError(t, s.Repos.EnsureExists(testCtx, bb))
	got, _, _ = s.Repos.Get(testCtx, bb.RepoId)
	assert.Equal(t, ports.VcsProviderBitbucket, got.Provider, "provider must not regress to github on re-upsert")

	// ListByTenant returns the provider too.
	list, err := s.Repos.ListByTenant(testCtx, "tenant-a")
	require.NoError(t, err)
	byId := make(map[ports.RepoId]ports.VcsProvider, len(list))
	for _, r := range list {
		byId[r.RepoId] = r.Provider
	}
	assert.Equal(t, ports.VcsProviderGitHub, byId["legacy/repo"])
	assert.Equal(t, ports.VcsProviderBitbucket, byId["bbteam/svc"])
}

func TestPrRunStore_Begin_HonorsIdempotencyKey(t *testing.T) {
	s := requireDB(t)
	require.NoError(t, s.Repos.EnsureExists(testCtx, ports.RepoRef{
		TenantId: "tenant-a", RepoId: "octo/repo", Owner: "octo", Name: "repo", DefaultBranch: "main",
	}))

	ref := ports.PrRef{TenantId: "tenant-a", RepoId: "octo/repo", PrNumber: 7, HeadSha: "abc"}
	key := "review:tenant-a:octo/repo:7:abc"

	id1, dup1, err := s.PrRuns.Begin(testCtx, store.BeginRun{
		Ref: ref, Trigger: ports.TriggerPrOpened, IdempotencyKey: key,
	})
	require.NoError(t, err)
	assert.False(t, dup1)
	assert.NotEmpty(t, id1)

	id2, dup2, err := s.PrRuns.Begin(testCtx, store.BeginRun{
		Ref: ref, Trigger: ports.TriggerPrOpened, IdempotencyKey: key,
	})
	require.NoError(t, err)
	assert.True(t, dup2, "second call with same idempotency_key should return duplicate=true")
	assert.Equal(t, id1, id2, "duplicate must return the original run id")
}

func TestCodeChunkStore_ContentHash_DedupAcrossRuns(t *testing.T) {
	s := requireDB(t)
	require.NoError(t, s.Repos.EnsureExists(testCtx, ports.RepoRef{
		TenantId: "tenant-a", RepoId: "octo/repo", Owner: "octo", Name: "repo", DefaultBranch: "main",
	}))

	vec := make([]float32, 1024) // zero vector is fine for this test
	chunk := store.CodeChunkUpsert{
		TenantId:    "tenant-a",
		RepoId:      "octo/repo",
		FilePath:    "src/handler.ts",
		StartLine:   1, EndLine: 10,
		Content:     "function handle() { ... }",
		ContentHash: "h1",
		CommitSha:   "abc",
		Embedding:   vec,
	}
	require.NoError(t, s.CodeChunks.UpsertMany(testCtx, []store.CodeChunkUpsert{chunk}))

	got, err := s.CodeChunks.ExistsByContentHash(testCtx, "octo/repo", []string{"h1", "h-missing"})
	require.NoError(t, err)
	assert.True(t, got["h1"])
	assert.False(t, got["h-missing"])
}

func TestCodeChunkStore_VectorSearch_OrdersByDistance(t *testing.T) {
	s := requireDB(t)
	require.NoError(t, s.Repos.EnsureExists(testCtx, ports.RepoRef{
		TenantId: "tenant-a", RepoId: "octo/repo", Owner: "octo", Name: "repo", DefaultBranch: "main",
	}))

	// Three chunks with distinct vectors; the query vector matches chunk 2 exactly.
	v1 := unitVec(1024, 1.0, 0.0, 0.0)
	v2 := unitVec(1024, 0.0, 1.0, 0.0)
	v3 := unitVec(1024, 0.0, 0.0, 1.0)
	chunks := []store.CodeChunkUpsert{
		{TenantId: "tenant-a", RepoId: "octo/repo", FilePath: "a.ts", StartLine: 1, EndLine: 1, Content: "v1", ContentHash: "v1h", CommitSha: "x", Embedding: v1},
		{TenantId: "tenant-a", RepoId: "octo/repo", FilePath: "b.ts", StartLine: 1, EndLine: 1, Content: "v2", ContentHash: "v2h", CommitSha: "x", Embedding: v2},
		{TenantId: "tenant-a", RepoId: "octo/repo", FilePath: "c.ts", StartLine: 1, EndLine: 1, Content: "v3", ContentHash: "v3h", CommitSha: "x", Embedding: v3},
	}
	require.NoError(t, s.CodeChunks.UpsertMany(testCtx, chunks))

	hits, err := s.CodeChunks.SearchByEmbedding(testCtx, store.SearchCodeChunks{
		RepoId: "octo/repo", Embedding: v2, K: 3,
	})
	require.NoError(t, err)
	require.GreaterOrEqual(t, len(hits), 1)
	assert.Equal(t, "v2", hits[0].Content, "closest match should win")
}

func TestEmbeddingCache_RoundTrip(t *testing.T) {
	s := requireDB(t)
	v := unitVec(1024, 0.5, 0.5, 0.0)
	require.NoError(t, s.EmbeddingCache.PutMany(testCtx, []store.EmbeddingCacheEntry{
		{Hash: "hash-1", Embedding: v},
	}))
	got, err := s.EmbeddingCache.GetMany(testCtx, []string{"hash-1", "hash-2"})
	require.NoError(t, err)
	require.Len(t, got, 1)
	assert.Equal(t, v[:3], got["hash-1"][:3])
	_, missing := got["hash-2"]
	assert.False(t, missing, "missing keys should not appear in result map")
}

func TestCommentStore_UpsertIdempotentOnGithubId(t *testing.T) {
	s := requireDB(t)
	require.NoError(t, s.Repos.EnsureExists(testCtx, ports.RepoRef{
		TenantId: "tenant-a", RepoId: "octo/repo", Owner: "octo", Name: "repo", DefaultBranch: "main",
	}))

	githubId := int64(12345)
	v := unitVec(1024, 1.0, 0.0, 0.0)
	id1, err := s.Comments.Upsert(testCtx, store.CommentUpsert{
		TenantId: "tenant-a", RepoId: "octo/repo", PrNumber: 7,
		Source:   "human",
		GithubId: &githubId,
		FilePath: "a.ts", StartLine: 1, EndLine: 1,
		CommentText: "original",
		Outcome:     store.OutcomePending,
		Embedding:   v,
	})
	require.NoError(t, err)

	id2, err := s.Comments.Upsert(testCtx, store.CommentUpsert{
		TenantId: "tenant-a", RepoId: "octo/repo", PrNumber: 7,
		Source:   "human",
		GithubId: &githubId,
		FilePath: "a.ts", StartLine: 1, EndLine: 1,
		CommentText: "updated body",
		Outcome:     store.OutcomeAccepted,
		Embedding:   v,
	})
	require.NoError(t, err)
	assert.Equal(t, id1, id2, "second upsert with same github_id must return the original comment_id")

	got, err := s.Comments.ListByPr(testCtx, ports.PrRef{TenantId: "tenant-a", RepoId: "octo/repo", PrNumber: 7})
	require.NoError(t, err)
	require.Len(t, got, 1, "the row count must stay at 1 across re-runs")
	assert.Equal(t, "updated body", got[0].CommentText)
	assert.Equal(t, store.OutcomeAccepted, got[0].Outcome)
}

func TestSettingsStore_RoundTrip(t *testing.T) {
	s := requireDB(t)

	v, found, err := s.Settings.Get(testCtx, "missing-key")
	require.NoError(t, err)
	assert.False(t, found)
	assert.Empty(t, v)

	require.NoError(t, s.Settings.Set(testCtx, "rules.git_url", "https://example.com/rules.git", "alice@example.com"))
	require.NoError(t, s.Settings.Set(testCtx, "cost.daily_usd_cap_default", "12.5", "alice@example.com"))

	v, found, err = s.Settings.Get(testCtx, "rules.git_url")
	require.NoError(t, err)
	assert.True(t, found)
	assert.Equal(t, "https://example.com/rules.git", v)

	all, err := s.Settings.GetAll(testCtx)
	require.NoError(t, err)
	require.Len(t, all, 2)
	assert.Equal(t, "cost.daily_usd_cap_default", all[0].Key)
	assert.Equal(t, "rules.git_url", all[1].Key)
	assert.Equal(t, "alice@example.com", all[0].UpdatedBy)

	// Set is upsert.
	require.NoError(t, s.Settings.Set(testCtx, "rules.git_url", "https://example.com/other.git", "bob@example.com"))
	v, _, _ = s.Settings.Get(testCtx, "rules.git_url")
	assert.Equal(t, "https://example.com/other.git", v)

	// Delete removes; absent key delete is not an error.
	require.NoError(t, s.Settings.Delete(testCtx, "rules.git_url"))
	require.NoError(t, s.Settings.Delete(testCtx, "rules.git_url"))
	_, found, _ = s.Settings.Get(testCtx, "rules.git_url")
	assert.False(t, found)
}

func TestCostCapStore_GetEffective_FallsThroughToDefault(t *testing.T) {
	s := requireDB(t)
	require.NoError(t, s.Repos.EnsureExists(testCtx, ports.RepoRef{
		TenantId: "tenant-a", RepoId: "octo/repo", Owner: "octo", Name: "repo", DefaultBranch: "main",
	}))
	cap, err := s.CostCaps.GetEffective(testCtx, "tenant-a", "octo/repo")
	require.NoError(t, err)
	assert.Equal(t, 5.00, cap.DailyUsdCap, "should fall back to in-memory default when no rows")
	assert.Equal(t, 30000, cap.PerPrTokenCap)
}

func unitVec(dim int, x, y, z float32) []float32 {
	v := make([]float32, dim)
	if dim > 0 {
		v[0] = x
	}
	if dim > 1 {
		v[1] = y
	}
	if dim > 2 {
		v[2] = z
	}
	return v
}

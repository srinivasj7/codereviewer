# Code Review System — Implementation Plan

**Status:** Slice 4 + 4.5 (admin UI) complete; slice 5 next
**Last updated:** 2026-05-13
**Companion to:** [`docs/design.md`](./docs/design.md)

This plan translates the design spec into a concrete, slice-by-slice build. Every slice lands runnable end-to-end. Update the **Progress** table below as work completes.

---

## Locked decisions

| Decision | Choice | Rationale |
|---|---|---|
| Language / runtime | **Go 1.23+** | Performance-optimized: ~3-5x smaller memory footprint than Node, ~50ms cold start, native concurrency via goroutines, single static binary per app. Reduces instance size at scale and simplifies deploy. **Trade-off:** team is TS-dominant; onboarding cost is real. Rust considered and rejected — CPU-bound work is small (network I/O dominates), velocity tax not worth it. |
| Module layout | Single Go module, `internal/` boundary, `cmd/` for apps | Idiomatic Go; `internal/` enforces package privacy at the language level |
| DB access | `pgx/v5` + `sqlc` + `pgvector-go` | `pgx` is the canonical high-performance Postgres driver; `sqlc` generates type-safe Go from raw SQL (no ORM); `pgvector-go` wraps the vector type cleanly |
| Migrations | `goose` | Forward-only versioned SQL, simple CLI, embeddable in CI |
| Validation | Typed structs + `go-playground/validator` at trust boundaries | Standard Go pattern; no runtime overhead inside the core |
| HTTP | `chi` + `net/http` stdlib | Lightweight idiomatic router, OTel-friendly, no framework lock-in |
| Tests | `testing` stdlib + `testify` + `testcontainers-go` + LocalStack | Real infra in CI; hand-written fakes for unit tests |
| Logging | `log/slog` (stdlib, JSON) | Structured, fast, zero-deps |
| LLM gateway | LiteLLM sidecar from day one | Centralized routing/retries/spend tracking; talks OpenAI wire format to our Go client |
| Tenancy | Single-tenant deploy, multi-tenant schema | `tenant_id` everywhere; ship faster |
| Worker runtime | Infrastructure-agnostic Go binary | Same binary on laptop, EC2, or Fargate; runtime is a deploy concern, not a code interface |
| Rules repository | Separate git repo (URL via config) | Matches design rules-sync; non-engineer-editable; clean audit provenance |
| Monorepo root | `D:\code\codereviewer\` directly | Design doc moves to `docs/` |

## Open questions (deferred decisions)

- **Fallback model strategy** — same-vendor variant or cross-vendor for true HA. Default same-vendor for simplicity (design §15). Revisit after pilot.
- **LLM payload retention** — 90 days vs 12 months pending compliance team input (design §15).
- **Allowing GitHub `suggestion` blocks** — default off in v1 (design §15).
- **Per-tenant cost dashboards** — not in v1 since single-tenant deploys; revisit if SaaS becomes the model.

---

## Progress

| Slice | Status | Notes |
|---|---|---|
| 0. Skeleton + contracts + smoke test | **Complete** | `go build`, `go vet`, `go test` all green. 3 test packages (llm, prompt, smoke) covering drop-order, LLM parse, pipeline success/failure/budget/dedup/fail-open paths. |
| 1. Webhook + indexer (local infra) | **Complete** | 5 production adapters (storepostgres, busnats, vcsgithub, llmlitellm, parsertreesitter), full indexer pipeline, chi webhook gateway, cmd/migrate with embedded migrations, docker-compose stack. Verified: all 4 Go images build (including indexer with CGO+tree-sitter), postgres+pgvector and NATS come up healthy, `docker compose run --rm migrate` applies all 4 migrations cleanly (11 tables created in the schema). Slice 0 tests still green. |
| 2. Naive review pipeline | **Complete** | RepoStore + auto-registration on every webhook, migration 005 fixes id-type mismatch (UUID → TEXT), tiktoken-based token estimation for OpenAI models, per-stage latency stopwatch in the review pipeline (greppable p95 line), configurable gateway listen address, /review slash command, storepostgres contract tests (6 tests, external Postgres via TESTS_POSTGRES_URL — no testcontainers dep per library policy). Verified: `go vet/build/test ./...` clean; `make test-integration` passes against `docker compose up postgres`. |
| 3. Retrieval + backfill | **Complete** | Live retrievers wired into the review pipeline (one shared diff embedding → code + comment vector search; rules scope-matched in-memory). Format helpers render `<file>:lines (symbol)`, `[OUTCOME] <file>`, `title\ndescription`. `cmd/backfill-cli` paginates GitHub Search closed-PR results, ingests review comments + diff hunks + reactions, embeds via the cache (hash dedup), upserts with `source='human'` and `RETURNING comment_id` so re-runs return the stable id. New tests: 4 backfill unit tests + 4 format tests + a storepostgres idempotency test (8 contract tests total). |
| 4. Rules + feedback + observability | **Complete** | rulessourcegit (git CLI clone + `**` glob walk), rulessync pipeline (frontmatter+body parser, cached embeddings, tombstoning), feedback pipeline (reactions + replies; implicit line-changed deferred), gateway routes reactions+replies to the feedback queue, obsotel adapter (OTLP HTTP for traces + metrics; stdout fallback on init failure), OTel collector service + dev.toml flip to `sink="otel"`. New tests: 8 feedback pipeline tests + 7 rulessync parser tests + 3 rulessourcegit glob tests. All packages build + test green. |
| 4.5. Admin web UI + import/export | **Complete** | New `app_settings` table (migration 006), `SettingsStore` port + Postgres adapter + fake + contract test. Hybrid config overlay: TOML bootstrap, `app_settings` overlays runtime-tunable keys (rules URL, cost caps, tenant info, model choices, observability sink/endpoint). New `cmd/admin-ui` binary on `:8090` with chi router + html/template; password + signed-cookie session auth; GitHub OAuth as a second login path (org-membership check). Dashboard, settings editor, config import/export (TOML), selective DB export/import (code_chunks + rules + review_comments as JSON), scheduled auto-export to a configured directory. Worker boot order now `PickStores → ApplyOverlay → PickObservability` so live setting changes are visible after a restart. New tests: 15 admin handler/session tests + 6 overlay tests + 1 storepostgres settings contract test. |
| 5. EC2 deploy profile | Not started | |

---

## Honest correction to the design's "12 plug slots"

The design lists 12 slots in §4, but several are infrastructure concerns, not code interfaces.

| Design slot | What it is in code |
|---|---|
| VCS source | Port `VcsSource` |
| HTTP ingress | Port `HttpIngress` (chi wrapper) |
| Message bus | Port `MessageBus` |
| Worker runtime | *Not a port* — Go binary; runtime is a deploy concern |
| LLM gateway | Port `LlmGateway` (talks to LiteLLM URL) |
| Vector + relational store | Split into 7 sub-ports, one per table family |
| OTel collector | *Not a port* — config URL only |
| Observability sink | *Not a port* — downstream of collector |
| LLM endpoint | *Not a port* — LiteLLM upstream config |
| Embeddings endpoint | *Not a port* — LiteLLM upstream config |
| Egress path | *Not a port* — networking concern |
| Secrets | Port `SecretsProvider` |

Plus two ports implied but not enumerated: `ParserRegistry` (tree-sitter) and `Clock` (testability).

**Final code-port surface: 10 top-level interfaces + 7 store sub-interfaces.**

### Note on Go convention

Idiomatic Go prefers small, consumer-defined interfaces declared next to the consumer. We deliberately centralize all ports in `internal/ports/` because the design treats plug slots as the system's load-bearing architecture, and adapter authors need one place to find the contracts. This is a deliberate bend, documented for future maintainers.

---

# Slice 0 — Skeleton, contracts, and smoke test

## Scope

**In:** Go module, all port interfaces, schemas, domain types, core pipeline scaffolds, in-memory adapters, DB migrations (committed but not yet exercised), one smoke test that runs the review pipeline end-to-end against fakes.

**Out:** Real `vcsgithub` / `bussqs` / `busnats` / `llmlitellm` / `storepostgres` adapters, tree-sitter parsing, Terraform, Docker Compose. All of those come in slice 1+.

## Definition of done

- [x] `go build ./...`, `go vet ./...`, `go test ./...` all green on a clean checkout
- [ ] `golangci-lint run` — config in `.golangci.yml`; CI integration deferred to slice 1
- [x] Smoke test boots review-worker against in-memory adapters, publishes a fake `ReviewJob`, asserts:
  - `FakeVcs.PostReview` was called with a well-formed payload
  - `FakeVcs.UpdateCheck` was called with `conclusion: "success"`
  - `FakePrRunStore` has a `posted` row with `TokensIn > 0`, `CostUsd > 0`
- [x] Budget-exceeded test: with `dailyUsdCap = 0`, pipeline short-circuits, LLM is never called, neutral comment posted
- [x] Drop-order test: with tight `perPrTokenCap`, past reviews → related code → rules drop in that order; **diff is never trimmed**
- [ ] `go-cleanarch` enforces no `internal/core → internal/adapters` imports — boundary respected manually; tool wiring deferred to slice 1 CI
- [x] Bonus: high-severity (bug/security) comment fails status check; bus-level idempotency dedups duplicate jobs; LLM outage triggers fail-open path with `pr_runs.status = failed-open`

## Repository layout

```
codereviewer/
├─ go.mod
├─ go.sum
├─ Makefile                              # build, test, lint, migrate, dev-up
├─ .golangci.yml                         # lint config
├─ .gitignore
├─ .editorconfig
├─ README.md                             # bootstrap commands only
│
├─ docs/
│  └─ design.md                          # moved from root
│
├─ cmd/                                  # each app is one main package
│  ├─ webhook-gateway/main.go            # SLICE 0 — chi server, HMAC verify, enqueue
│  ├─ review-worker/main.go              # SLICE 0 — consumes ReviewJob via bus
│  ├─ indexer-worker/main.go             # SLICE 1 — stub
│  ├─ feedback-worker/main.go            # SLICE 4 — stub
│  ├─ backfill-cli/main.go               # SLICE 3 — stub
│  └─ rules-sync/main.go                 # SLICE 5 — stub
│
├─ internal/
│  ├─ ports/                             # SLICE 0 — all interfaces
│  │  ├─ types.go                        # PrRef, RepoRef, TenantId, ...
│  │  ├─ vcs.go                          # VcsSource
│  │  ├─ ingress.go                      # HttpIngress
│  │  ├─ bus.go                          # MessageBus
│  │  ├─ llm.go                          # LlmGateway
│  │  ├─ secrets.go                      # SecretsProvider
│  │  ├─ parser.go                       # ParserRegistry
│  │  ├─ observability.go                # Tracer, Meter, Logger
│  │  ├─ clock.go                        # Clock
│  │  ├─ rules_source.go                 # RulesSource
│  │  └─ store/
│  │     ├─ code_chunks.go               # CodeChunkStore
│  │     ├─ comments.go                  # CommentStore
│  │     ├─ rules.go                     # RuleStore
│  │     ├─ pr_runs.go                   # PrRunStore
│  │     ├─ feedback.go                  # FeedbackStore
│  │     ├─ cost_caps.go                 # CostCapStore
│  │     └─ embedding_cache.go           # EmbeddingCache
│  │
│  ├─ schemas/                           # SLICE 0 — wire-format types + validators
│  │  ├─ config.go                       # TOML config struct + validation
│  │  ├─ jobs.go                         # ReviewJob, IndexJob, FeedbackJob, BackfillJob
│  │  ├─ llm_output.go                   # Appendix A JSON shape + validator
│  │  └─ webhook_github.go               # GitHub webhook payload subset
│  │
│  ├─ core/                              # SLICE 0 — pure domain logic
│  │  ├─ pipelines/
│  │  │  ├─ review/pipeline.go           # ReviewPipeline
│  │  │  ├─ indexer/pipeline.go          # skeleton; full in slice 1
│  │  │  ├─ feedback/pipeline.go         # skeleton
│  │  │  └─ backfill/pipeline.go         # skeleton
│  │  ├─ prompt/
│  │  │  ├─ assemble.go                  # Appendix A template rendering
│  │  │  └─ budget.go                    # drop-order logic
│  │  ├─ retrieval/
│  │  │  ├─ code.go
│  │  │  ├─ comments.go
│  │  │  └─ rules.go
│  │  ├─ budgets/
│  │  │  ├─ cost_cap.go
│  │  │  └─ token_cap.go
│  │  └─ llm/
│  │     ├─ parse_output.go              # validates Appendix A JSON
│  │     └─ retry.go                     # 3x backoff + fallback routing
│  │
│  ├─ config/                            # SLICE 0
│  │  ├─ load.go                         # TOML + ${env} expansion + validation
│  │  └─ pick.go                         # PickBus, PickVcs, PickLlm, ...
│  │
│  ├─ db/                                # SLICE 0 — migrations committed
│  │  ├─ migrations/
│  │  │  ├─ 001_init.sql                 # design §5 verbatim
│  │  │  ├─ 002_embedding_cache.sql
│  │  │  └─ 003_job_idempotency.sql
│  │  ├─ sqlc.yaml                       # config; queries empty until slice 1
│  │  └─ query/                          # sqlc-generated; placeholder until slice 1
│  │
│  ├─ adapters/
│  │  ├─ busmem/                         # SLICE 0 — in-process channels
│  │  ├─ secretsenv/                     # SLICE 0 — os.Getenv
│  │  ├─ obsstdout/                      # SLICE 0 — slog + no-op tracer
│  │  ├─ clocksystem/                    # SLICE 0 — wraps time.Now
│  │  ├─ storepostgres/                  # SLICE 1 — stub
│  │  ├─ bussqs/                         # SLICE 1 — stub
│  │  ├─ busnats/                        # SLICE 1 — stub
│  │  ├─ vcsgithub/                      # SLICE 1 — stub
│  │  ├─ llmlitellm/                     # SLICE 1 — stub
│  │  ├─ parsertreesitter/               # SLICE 1 — stub
│  │  ├─ rulessourcegit/                 # SLICE 5 — stub
│  │  └─ obsotel/                        # SLICE 4 — stub
│  │
│  ├─ boot/                              # SLICE 0 — composition root helpers
│  │  └─ wire.go                         # plain factory funcs, no DI framework
│  │
│  └─ testing/                           # SLICE 0
│     ├─ fakes/
│     │  ├─ vcs.go
│     │  ├─ llm.go
│     │  ├─ store/                       # in-memory impls of all 7 store ports
│     │  └─ parser.go
│     ├─ fixtures/
│     │  ├─ diff.go
│     │  └─ review_output.go
│     └─ harness/
│        └─ harness.go                   # boots full pipeline with fakes
```

## Exact port interface signatures (Go)

```go
// internal/ports/types.go
package ports

type TenantId string
type RepoId   string

type PrRef struct {
    TenantId TenantId
    RepoId   RepoId
    PrNumber int
    HeadSha  string
}

type RepoRef struct {
    TenantId      TenantId
    RepoId        RepoId
    Owner, Name   string
    DefaultBranch string
}
```

```go
// internal/ports/vcs.go
package ports

import (
    "context"
    "net/http"
)

type VcsSource interface {
    VerifyWebhook(ctx context.Context, headers http.Header, rawBody []byte) (WebhookEvent, error)
    FetchDiff(ctx context.Context, ref PrRef) (UnifiedDiff, error)
    FetchFileAt(ctx context.Context, repoId RepoId, sha, path string) (string, error)
    ListChangedFiles(ctx context.Context, repoId RepoId, baseSha, headSha string) ([]ChangedFile, error)
    ListPrComments(ctx context.Context, ref PrRef) ([]HumanComment, error)
    PostReview(ctx context.Context, ref PrRef, review ReviewPayload) (PostedReview, error)
    UpdateCheck(ctx context.Context, ref PrRef, result CheckResult) error
}
```

```go
// internal/ports/bus.go
package ports

import "context"

type QueueName string

type MessageBus interface {
    Publish(ctx context.Context, queue QueueName, payload []byte, opts PublishOpts) error
    Consume(ctx context.Context, queue QueueName, handler ConsumeFunc) (Subscription, error)
    Health(ctx context.Context) (HealthStatus, error)
}

type PublishOpts struct{ IdempotencyKey string }
type ConsumeFunc func(ctx context.Context, payload []byte, cctx ConsumeCtx) error
type ConsumeCtx struct {
    Ack     func() error
    Nack    func(reason string) error
    Attempt int
}
type Subscription interface{ Stop() error }
```

Note: the bus port deals in `[]byte` to keep adapter contracts simple across SQS/NATS/Kafka. Typed wrappers live in `internal/schemas` (e.g. `schemas.PublishReviewJob(ctx, bus, job)` marshals JSON and computes the idempotency key).

```go
// internal/ports/llm.go
package ports

import "context"

type LlmTier string

const (
    LlmTierPrimary  LlmTier = "primary"
    LlmTierFallback LlmTier = "fallback"
)

type LlmGateway interface {
    Chat(ctx context.Context, req ChatRequest) (ChatResponse, error)
    Embed(ctx context.Context, texts []string, opts EmbedOpts) ([]EmbeddingResult, error)
    EstimateTokens(text, model string) int          // tiktoken-based
}

type ChatRequest struct {
    Tier             LlmTier
    SystemPrompt     string // cacheable prefix
    UserPrompt       string
    MaxOutputTokens  int
    ResponseFormat   string // "json" | "text"
}

type ChatResponse struct {
    Content    string
    TokensIn   int
    TokensOut  int
    CostUsd    float64
    ModelUsed  string
}
```

```go
// internal/ports/store/code_chunks.go
package store

import (
    "context"
    "time"

    "codereviewer/internal/ports"
)

type CodeChunkStore interface {
    UpsertMany(ctx context.Context, chunks []CodeChunkUpsert) error
    SearchByEmbedding(ctx context.Context, args SearchByEmbedding) ([]CodeChunkHit, error)
    SoftDeleteMissing(ctx context.Context, repoId ports.RepoId, present []string, olderThan time.Time) (int, error)
    ExistsByContentHash(ctx context.Context, repoId ports.RepoId, hashes []string) (map[string]bool, error)
}

type SearchByEmbedding struct {
    RepoId            ports.RepoId
    Embedding         []float32
    K                 int
    SameFileBoostPath string // optional
}
```

```go
// internal/ports/store/comments.go
type CommentStore interface {
    Upsert(ctx context.Context, c CommentUpsert) (CommentId, error)
    SearchByEmbedding(ctx context.Context, args SearchCommentsByEmbedding) ([]CommentHit, error)
    UpdateOutcome(ctx context.Context, id CommentId, outcome Outcome, signal OutcomeSignal) error
    ListByPr(ctx context.Context, ref ports.PrRef) ([]Comment, error)
}

// internal/ports/store/pr_runs.go
type PrRunStore interface {
    Begin(ctx context.Context, args BeginRun) (RunId, bool /* duplicate */, error)
    Finish(ctx context.Context, runId RunId, result RunResult) error
    GetRecent(ctx context.Context, repoId ports.RepoId, prNumber, limit int) ([]PrRun, error)
}

// internal/ports/store/rules.go
type RuleStore interface {
    UpsertFromRepo(ctx context.Context, sourceCommit string, rules []RuleUpsert) error
    ListForScope(ctx context.Context, repoId ports.RepoId, paths []string) ([]Rule, error)
    TombstoneMissing(ctx context.Context, sourceCommit string, knownIds []RuleId) (int, error)
}

// internal/ports/store/feedback.go
type FeedbackStore interface {
    Append(ctx context.Context, e FeedbackEvent) error
    ListForComment(ctx context.Context, id CommentId) ([]FeedbackEvent, error)
}

// internal/ports/store/cost_caps.go
type CostCapStore interface {
    GetEffective(ctx context.Context, tenantId ports.TenantId, repoId ports.RepoId) (CostCap, error)
    RecordSpend(ctx context.Context, tenantId ports.TenantId, repoId ports.RepoId, usd float64, at time.Time) error
    TodaySpend(ctx context.Context, tenantId ports.TenantId, repoId ports.RepoId, tz string) (float64, error)
}

// internal/ports/store/embedding_cache.go
type EmbeddingCache interface {
    GetMany(ctx context.Context, hashes []string) (map[string][]float32, error)
    PutMany(ctx context.Context, entries []EmbeddingCacheEntry) error
}
```

```go
// internal/ports/secrets.go
type SecretsProvider interface { Get(ctx context.Context, name string) (string, error) }

// internal/ports/parser.go
type ParserRegistry interface {
    Supports(filePath string) bool
    ParseChunks(filePath, content string) ([]ParsedChunk, error)
}

// internal/ports/observability.go
type Tracer interface { StartSpan(ctx context.Context, name string, attrs ...Attr) (context.Context, Span) }
type Span   interface { SetAttribute(key string, value any); RecordError(err error); End() }
type Meter  interface { Counter(name string) Counter; Histogram(name string) Histogram }
type Logger interface { Info(msg string, kv ...any); Warn(msg string, kv ...any); Error(msg string, kv ...any) }

// internal/ports/clock.go
type Clock interface { Now() time.Time }

// internal/ports/rules_source.go
type RulesSource interface {
    FetchAt(ctx context.Context, gitUrl, ref string) (commitSha string, files []RawRuleFile, err error)
}

// internal/ports/ingress.go
type HttpIngress interface {
    Start(ctx context.Context, routes []RouteDef, opts StartOpts) (Server, error)
}
```

## Domain types

Defined in `internal/ports/types.go` and `internal/ports/store/types.go` as plain Go structs:

`UnifiedDiff`, `DiffHunk`, `ChangedFile`, `HumanComment`, `ReviewPayload`, `BotComment`, `PostedReview`, `CheckResult`, `WebhookEvent` (sum type via tag field `Kind` with `PullRequestEvent | PushEvent | ReviewCommentEvent | ReactionEvent`), `ParsedChunk`, `CodeChunkUpsert`, `CodeChunkHit`, `RuleUpsert`, `Rule`, `Outcome`, `OutcomeSignal`, `Trigger`, `RunResult`, `CostCap`.

`validator` tags only on the structs that cross trust boundaries (config, webhook payloads, LLM output). Internal port↔core types are unvalidated structs — compile-time safety is enough.

## Composition root example — `cmd/review-worker/main.go`

```go
func main() {
    ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
    defer stop()

    cfgPath := flag.String("config", "config.toml", "path to TOML config")
    flag.Parse()

    cfg, err := config.Load(*cfgPath)
    if err != nil { log.Fatal(err) }

    secrets := boot.PickSecrets(cfg.Secrets)                 // "aws" | "env" | "vault"
    clock   := clocksystem.New()
    obs     := boot.PickObservability(cfg.Observability)     // "stdout" | "otel"
    bus     := boot.PickBus(cfg.MessageBus, obs)             // "memory" | "sqs" | "nats"
    vcs     := boot.PickVcs(cfg.Vcs, secrets)                // "github" | ...
    llm     := boot.PickLlm(cfg.Llm, secrets, obs)           // "litellm" | ...
    store   := boot.PickStore(ctx, cfg.Store, obs)           // returns all 7 store ports

    pipeline := review.NewPipeline(review.Deps{
        Vcs: vcs, Llm: llm, Clock: clock, Obs: obs,
        CodeChunks: store.CodeChunks, Comments: store.Comments,
        Rules: store.Rules, PrRuns: store.PrRuns,
        CostCaps: store.CostCaps, EmbeddingCache: store.EmbeddingCache,
    })

    sub, err := bus.Consume(ctx, "review-jobs", pipeline.Handle)
    if err != nil { log.Fatal(err) }
    defer sub.Stop()

    <-ctx.Done()
}
```

**Swapping an adapter is one switch case in `internal/boot/wire.go`. No `core` changes.**

## Migrations

`internal/db/migrations/001_init.sql` is a near-verbatim transcription of design §5. Two additions:

1. `embedding_cache(content_hash PK, embedding vector(1024), created_at)` — billing optimization: never re-embed identical text.
2. `job_idempotency(idempotency_key PK, observed_at)` — bus-agnostic dedup.

`goose` runs them from a CLI subcommand: `go run ./cmd/migrate up`.

## Workspace tooling

`Makefile` targets:

```
build:        go build ./...
test:         go test -race ./...
typecheck:    go vet ./...
lint:         golangci-lint run
migrate-up:   go run ./cmd/migrate up
dev-review:   go run ./cmd/review-worker --config=dev.toml
dev-gateway:  go run ./cmd/webhook-gateway --config=dev.toml
generate:    sqlc generate
```

Conventions:
- Go 1.23 minimum (`toolchain go1.23.0` in `go.mod`)
- `internal/` blocks external imports (built-in)
- `golangci-lint` with: `errcheck`, `govet`, `staticcheck`, `revive`, `gocyclo`, `gosec`, `unused`, `gocritic`
- Architectural boundary enforced by `go-cleanarch` (or a custom `analysistools` package) in CI:
  - `internal/core/...` may NOT import `internal/adapters/...`
  - `cmd/...` may import everything
  - `internal/adapters/<x>` may NOT import other adapters
  - No circular imports (Go enforces this by default, but we check anyway)

## What slice 0 deliberately does NOT do

- No real Postgres connection. Migrations are committed but not run.
- No HTTPS listener in production mode. Webhook gateway has unit tests for HMAC + route logic only.
- No tree-sitter; `FakeParser` splits content by `\n\n` for tests.
- No Docker, no Terraform, no CI workflow files (those arrive with slice 1).
- No `sqlc generate` run yet — `internal/db/sqlc.yaml` is committed but `query/*.sql` is empty.

## Minor deviations from the plan (slice 3)

- **Outcome heuristic from reactions only.** The design's full feedback logic (line-changed-after-comment, reply detection) lives in the slice 4 feedback pipeline. Backfill uses the coarser thumbs-up/down signal which is good enough to seed retrieval weighting; the feedback worker will overwrite outcomes as it observes implicit signals.
- **Single embedding per review for both code and comment search.** Per design §6.1 the two searches use the same diff embedding; we materialize it once via `EmbeddingCache` (keyed `review-query:<repo>:<head_sha>`) and pass it to both retrievers. Cheap and keeps the cost predictable.
- **Diff embedded with simple char-ratio truncation** at ~8K tokens. Splitting the diff per-file and merging search results is a slice 4+ enhancement; for typical PRs the diff fits comfortably.
- **Rule scope matching `path:**/*` patterns** is approximate via `filepath.Match` (which doesn't natively support `**`). Slice 4 can substitute a glob library or hand-rolled matcher; current behavior is "good enough for the documented rule shapes."
- **`CommentStore.Upsert` now uses `RETURNING comment_id`.** Slice 1 returned the freshly-generated UUID even when the row already existed under a different id — broken for idempotency. Fixed; the storepostgres contract test guards the invariant.
- **GitHub Search API rate-limit handling** is delegated to the go-github library's default retry-on-429. For windows that return >1000 PRs the API caps results; date-range chunking would be needed and is a slice 4 enhancement.

## Minor deviations from the plan (slice 2)

- **No testcontainers-go.** The plan originally suggested testcontainers; per the library policy ([see memory](file:///C:/Users/srinu/.claude/projects/D--code-codereviewer/memory/feedback_library_policy.md)), test infra is precisely the kind of dep we own ourselves. Contract tests now read `TESTS_POSTGRES_URL` from the environment; `make test-integration` is the convenience target that brings up Postgres via the existing `docker-compose.yml`. Same coverage, zero new deps.
- **`pkoukk/tiktoken-go` kept** with a documented security rationale: single-purpose, deterministic, no network after BPE load, widely deployed. Anthropic-tokenizer integration when a Claude model becomes primary.
- **Migration 005 converts UUID → TEXT** for `tenant_id` and `repo_id` columns across all 8 affected tables. Slice 1's "default-tenant" and "owner/name" strings would have failed UUID parsing on first write; no production data existed yet so this is forward-only with no compensating migration needed.
- **`make test-race` still requires CGO** (Windows users without a C toolchain can't run it locally; CI runs it on Linux). Unchanged from slice 0.
- **`x-litellm-response-cost` from response headers** still deferred. Client-side cost via the price table in `llmlitellm` is exact enough for budget caps; precise per-request cost arrives with the OTel instrumentation in slice 4.

## Minor deviations from the plan (slice 1)

- **sqlc deferred.** Hand-written pgx queries in `storepostgres/`. Rationale: pgvector cosine search with conditional same-file boost and outcome-weighted re-ranking is cleaner hand-written than generated. `sqlc.yaml` is committed for slice 2+ if the CRUD surface grows enough to justify codegen.
- **Tree-sitter under CGO build tags.** `parsertreesitter/parser_cgo.go` is `//go:build cgo` (real impl); `parser_nocgo.go` is `//go:build !cgo` (stub that errors). `go build ./...` works on Windows without a C toolchain; the Dockerized indexer always has CGO. `go.mod` needs `exclude github.com/smacker/go-tree-sitter/javascript v0.0.1` to disambiguate the package from the smacker repo's parent module.
- **`go mod tidy` with CGO disabled prunes tree-sitter + goose.** Run with `CGO_ENABLED=1` when adjusting deps (or in Docker/Linux/macOS).
- **Single GitHub App installation per deployment.** `vcs.installation_id` is fixed in config. Multi-installation routing is a slice 2 enhancement.
- **`vcs.repo_id` shape = "owner/name".** Carried as an opaque string through the system; `vcsgithub` splits when calling REST. A more abstract repo identifier waits for slice 2.
- **Cost computed client-side from a price table** in `llmlitellm/`. Slice 2 can read `x-litellm-response-cost` from LiteLLM response headers for exact values (go-openai doesn't expose headers today).
- **EstimateTokens is `len(text)/4`** across all adapters. Per-provider tokenizers (tiktoken-go, anthropic-tokenizer) arrive with slice 2.
- **`webhook-gateway` hardcodes `:8080`.** Configurable in slice 2.
- **`defaultTenantId = "default-tenant"` in the gateway.** Single-tenant deploy; multi-tenant routing waits.
- **No adapter contract tests** (testcontainers-based). Slice 0 fakes-based tests still cover the pipeline logic. Real-DB integration tests can come with slice 2.

## Minor deviations from the plan (slice 0)

- **`internal/config/pick.go` consolidated into `internal/boot/wire.go`.** Originally listed in both places; keeping it only in `boot` avoids the circular import risk (config would otherwise depend on adapters). `internal/config` is now pure loading + validation; `internal/boot` is the only package that imports adapters.
- **Stub adapter directories (`vcsgithub`, `bussqs`, `busnats`, `llmlitellm`, `storepostgres`, `parsertreesitter`, `obsotel`, `rulessourcegit`) not created.** Empty Go packages need a `doc.go`; cheaper to create them when their first real code lands. `boot.Pick*` returns descriptive "not yet implemented" errors for these config values today.
- **Makefile split `test` and `test-race`.** Go's race detector requires CGO; on Windows without a C toolchain that breaks the default. `test` runs without race; CI on Linux uses `test-race`.

---

# Slice 1 — Webhook + indexer (local infrastructure)

**Goal:** index a real repo locally end-to-end.

**Adds:**
- `internal/adapters/storepostgres` — `pgx/v5` + `sqlc`-generated queries + `pgvector-go` for the vector type
- `internal/adapters/busnats` — embedded NATS for local dev; same server config works on EC2
- `internal/adapters/vcsgithub` — `google/go-github` + `ghinstallation/v2` for GitHub App auth
- `internal/adapters/parsertreesitter` — `smacker/go-tree-sitter` for ts/tsx/js/jsx/json
- `internal/adapters/llmlitellm` — `sashabaranov/go-openai` configured against the LiteLLM URL
- `cmd/indexer-worker` — fully wired, consumes `index-jobs`
- `cmd/webhook-gateway` — real HTTPS listener via chi
- `docker-compose.yml` — Postgres+pgvector, LiteLLM, NATS, the gateway, the indexer
- `sqlc generate` produces `internal/db/query/*.go`; CI re-runs and diffs

**Done when:** pushing to a default branch on a configured pilot repo causes the indexer to populate `code_chunks` with embeddings for changed files only; content-hash skip works on the cheap path.

---

# Slice 2 — Naive review pipeline

**Goal:** bot posts inline + summary + status check on a real test PR.

**Adds:**
- `cmd/review-worker` — fully wired
- Cost cap + token cap enforced before any LLM call
- No retrieval-augmented context yet — just diff + system prompt
- Baseline cost measurement against pilot PRs

**Done when:** opening a PR on a pilot repo produces a posted review within design's 4-min p95 target. `pr_runs` rows include token counts and cost.

---

# Slice 3 — Retrieval and backfill

**Goal:** review-worker uses past comments + related code + rules.

**Adds:**
- `internal/core/retrieval/*` fully wired into prompt assembly
- `cmd/backfill-cli` — paginates GitHub Search API, populates `review_comments` with `source='human'`, embeds `comment_text || diff_hunk`
- Prompt drop-order under budget pressure (already unit-tested in slice 0, now exercised live)

**Done when:** 9-month backfill completes idempotently for one pilot repo, retrieval surfaces semantically similar past comments in new reviews. Cost-per-PR delta measured vs. slice 2.

---

# Slice 4 — Rules, feedback, observability

**Goal:** quality improves from human feedback; team has a dashboard.

**Adds:**
- `cmd/feedback-worker` — implicit (line-changed) + explicit (thumbs) signal capture
- `cmd/rules-sync` — push to rules repo updates `rules` table, embeds rule descriptions
- `internal/adapters/obsotel` — replaces `obsstdout` in cloud profiles
- Dashboard panels for the metrics in design §10

**Done when:** thumbs-down on a bot comment is recorded within seconds. Adding a rule in the rules repo and pushing → rule is enforced in the next review. Dashboard shows review.duration, cost, false-positive trend.

## Slice 4 deviations from the design

- **Implicit "line-changed" feedback signal deferred.** Design §6.3 step 1 fires `implicit-line-changed` when a new commit modifies the lines a bot commented on. Implementing this requires diffing successive commits inside the feedback pipeline (or a `pull_request.synchronize` handler that re-evaluates each open bot comment). The current implementation captures thumbs-up/down reactions and replies — enough to drive the retrieval weighting. Tracked for slice 5+.
- **Dashboard panels not authored.** Slice 4 ships the OTel collector and the per-pipeline metrics (counters and histograms) flowing through it, but the visualization surface — Grafana dashboard JSON or equivalent — is left to whoever owns the operational tooling in deployment. The names are stable so dashboards can be built without further code changes.
- **rulessync is a CLI, not a webhook handler.** Design §6.5 has the rules sync triggered by `push` on the rules repo. Slice 4 ships it as `cmd/rules-sync` (run via cron or `docker compose run --rm rules-sync`). The webhook trigger is a thin gateway addition deferred to slice 5.
- **OTLP exporter uses `WithInsecure()`.** Default for local docker-compose where the collector is a sibling container. Production deploys MUST flip this — either via TLS to a remote collector or via an in-pod sidecar bound to localhost. Tracked as a slice 5 hardening item.
- **`feedback_queue_url` field exists in TOML but the bus adapter shares the single NATS URL across queues.** No behavior gap — JetStream subjects partition the queues — but it leaves a misleading config knob. Cleanup deferred.
- **Single-tenant rules.** rulessync writes all rules under one TenantId (the configured `tenant.id`); design's multi-tenant rules-sync where one rules repo serves many tenants needs a tenant-aware folder convention which isn't pinned down yet.

---

# Slice 4.5 — Admin web UI + import/export

**Goal:** an authenticated operator can configure the system from a browser without editing TOML files, and back up the durable retrieval data to a portable JSON snapshot.

**Adds:**
- `cmd/admin-ui` binary serving `:8090` — chi router, server-rendered `html/template` views, signed-cookie session auth.
- Two login paths: single admin password (env-sourced via SecretsProvider) and optional GitHub OAuth with org-membership check.
- `app_settings` table (migration 006) + `SettingsStore` port. Hybrid overlay: TOML defines bootstrap (DB URL, secrets provider, listen addr, bus URLs); the settings table overrides runtime-tunable values listed in `config.OverlayKeys`.
- Dashboard with table counts, current overlay values, export/import buttons.
- Config import/export as TOML; selective DB export/import (code_chunks + rules + review_comments) as JSON with embeddings included.
- Optional `AutoExporter` goroutine writes timestamped TOML + JSON snapshots into a configurable directory at a configurable interval.

**Done when:** an operator can open `http://localhost:8090`, log in with the admin password, change `rules.git_url`, save, restart the workers via `docker compose restart`, and have the new value take effect. Re-importing a previous JSON snapshot restores the retrieval data without manual SQL.

## Slice 4.5 deviations from the design

- **Restart required for worker uptake of new settings.** Workers apply the overlay at boot; live SIGHUP / config-reload is not implemented. The admin UI saves the value and shows "Restart workers to pick them up" in the success flash. Hot-reload would require either polling `app_settings` from each pipeline or pushing settings change events through the bus; deferred until there's a real operational need.
- **Single admin user.** The password is one secret shared by every operator. Audit identifies a writer only as `password` (or `github:<login>` when OAuth is used). True per-user accounts are deferred — the pilot deployment has ~5 operators and Git history on `app_settings.updated_by` is sufficient.
- **CSRF protection is implicit.** The admin UI accepts state-changing POSTs only on same-site cookies (`SameSiteLaxMode`) and password-validates the session. No CSRF token is rendered into forms; if the UI ever needs to accept cross-origin requests, this becomes a hardening item.
- **Cookie secure flag is off by default.** Set by the operator's reverse proxy (TLS terminator). The compose file binds `:8090` plain HTTP for local dev. Production deploys must set `secure: true` when constructing the Server.
- **DB export bundles `tenants` + `repos` + the three retrieval tables.** Parent tables are included so a cold-start import on a fresh database satisfies FK constraints before the gateway has a chance to auto-register them on first webhook. `pr_runs`, caches, `feedback_events`, and `app_settings` are still excluded. `app_settings` is included implicitly via the config export — re-importing the config TOML restores the settings.
- **Overlay string values are env-expanded at apply time.** `${VAR}` references in any overlay string value (e.g. `observability.otlp_endpoint = ${OTEL_ENDPOINT}`) resolve from the worker's environment, so the same exported settings file can target docker-compose (where `otel-collector` is a service hostname) and EC2 (where it isn't). Missing variables expand to empty.
- **No retention policy on auto-export files.** The scheduler appends new files; pruning is the operator's problem. The export volume is a docker named volume, so disk pressure is at least bounded by the host.
- **OAuth callback URL is fixed at boot.** It comes from `admin.github_oauth.callback_url` in TOML. Multiple admin-ui replicas serving different hostnames would need a stable URL fronted by a load balancer.

---

# Slice 5 — EC2 deploy profile

**Goal:** one Terraform `apply` brings up a working single-node deployment.

**Adds:**
- `infra/profiles/lean-self-hosted/` — single EC2, embedded NATS, self-hosted Postgres, stdout OTLP
- Production Dockerfile (multi-stage; final stage is `gcr.io/distroless/static-debian12` with the static Go binary)
- Systemd units that launch the Go binaries directly (no container needed if preferred)
- GitHub Actions workflow: vet + test + lint + build images for amd64 and arm64 (Graviton)

**Done when:** `terraform apply` in a fresh AWS account brings up a host capable of reviewing PRs on a pilot repo. The same binary runs locally and on the EC2 host with only TOML config changes.

---

## How to use this document

- Update **Progress** table as slices land.
- When a binding decision is made, move it from **Open questions** to **Locked decisions** with the rationale.
- Cross-slice scope creep should be added to a future slice, not the current one. Resist expanding the current slice — that's how skeleton work calcifies into a "small-detour" PR that takes a week.
- If a slice's "Done when" criterion turns out to be unmeasurable or wrong, edit it here before changing the code to match.

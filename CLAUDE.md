# CLAUDE.md

Guidance for Claude Code (and humans) working in this repo.

## What this is

An AI-assisted code review system for a ~35-developer aviation software team. Posts inline comments, summary, and a required status check on every PR. Learns from accepted vs dismissed feedback. Infrastructure-pluggable: any external dependency can be swapped without touching application code.

**Authoritative documents:**
- [`docs/design.md`](./docs/design.md) — system design spec (the *what* and *why*)
- [`implementation-plan.md`](./implementation-plan.md) — slice-by-slice build plan, progress tracker, locked decisions

If you change a load-bearing decision, update `implementation-plan.md` first, then the code.

## Stack at a glance

- **Language:** Go 1.23+ (single module; `internal/` enforces boundaries)
- **DB:** Postgres 16 + `pgvector` + `pg_trgm`; access via `pgx/v5` + `sqlc`-generated queries
- **Bus:** abstracted; NATS for local, SQS/Kafka/NATS in cloud (config picks)
- **LLM:** LiteLLM sidecar speaks OpenAI wire format to any provider URL
- **VCS:** GitHub App (Cloud); other providers behind the same `VcsSource` port
- **Migrations:** `goose`
- **HTTP:** `chi` + `net/http`
- **Logging:** `log/slog` (JSON)
- **Observability:** OpenTelemetry (OTLP)
- **Tests:** stdlib `testing` + `testify` + `testcontainers-go` + LocalStack

## Architecture: hexagonal / ports & adapters

Every external dependency is a port (interface) in `internal/ports/`. Implementations live in `internal/adapters/<x>/`. The application's `cmd/<x>/main.go` is the composition root — the **only** place adapter concrete types are constructed.

**Hard rule, enforced in CI:**
- `internal/core/...` **must not** import `internal/adapters/...`
- `cmd/...` may import everything
- `internal/adapters/<x>` **must not** import other adapters

If you find yourself wanting to break this rule, the abstraction is wrong. Add what's missing to a port instead.

## Repository layout

```
codereviewer/
├─ cmd/                            # apps (each = one main package, one binary)
├─ internal/
│  ├─ ports/                       # all interface definitions (10 top + 7 store sub)
│  ├─ schemas/                     # wire-format types (config, jobs, webhook payloads, LLM output)
│  ├─ core/                        # pure domain logic; NEVER imports adapters
│  │  ├─ pipelines/{review,indexer,feedback,backfill}/
│  │  ├─ prompt/                   # template assembly + drop-order budget logic
│  │  ├─ retrieval/                # vector retrieval orchestration
│  │  ├─ budgets/                  # cost & token cap enforcement
│  │  └─ llm/                      # output parsing + retry/fallback policy
│  ├─ config/                      # TOML loader + validation
│  ├─ db/migrations/               # SQL migrations (goose-managed)
│  ├─ adapters/                    # one per concrete impl (busnats, vcsgithub, etc.)
│  ├─ boot/                        # wire.go: factory funcs that pick adapters from config
│  └─ testing/                     # fakes + fixtures + harness for integration-style tests
├─ docs/                           # design.md and other long-form docs
├─ infra/                          # Terraform profiles (slice 5+)
├─ implementation-plan.md          # progress + decisions
└─ docs/design.md                  # design spec
```

## Common tasks (make targets)

```
make build         # go build ./...
make test          # go test -race ./...
make typecheck     # go vet ./...
make lint          # golangci-lint run
make migrate-up    # goose up against $POSTGRES_URL
make generate      # sqlc generate
make dev-review    # run review-worker with dev.toml
make dev-gateway   # run webhook-gateway with dev.toml
```

## Adding a new adapter for an existing port (most common change)

1. Create `internal/adapters/<name>/` with a constructor returning the port interface.
2. Add a switch case in `internal/boot/wire.go` keyed on the relevant config string (e.g. `cfg.MessageBus.Type == "kafka"`).
3. Add a row to the config schema in `internal/schemas/config.go`.
4. Write an adapter contract test in the adapter package — use `testcontainers-go` if real infra is needed.
5. Update the **Locked decisions** or **Progress** sections in `implementation-plan.md` if the new adapter changes a default.

Do **not** add the new adapter as a dependency of `internal/core/`. Core only sees the port interface.

## Adding a new port (rare; means we found a new plug slot)

Justify it. Most "new ports" are actually a new method on an existing port. Adding a port has a cost: every adapter has to grow to match, every test harness needs a new fake. If it's truly a new external concern (a new VCS, a new vector store), then:

1. Define the interface in `internal/ports/<name>.go`.
2. Write at least one in-memory adapter for testing in `internal/testing/fakes/`.
3. Write at least one production adapter in `internal/adapters/<name><impl>/`.
4. Wire it into `internal/boot/wire.go`.
5. Update `implementation-plan.md` — add it to the port count, document the contract.

## Cost / billing optimization (touch carefully)

The system has six layers of cost defense, cheapest-first. Don't disable any of them without explicit approval:

1. **Pre-enqueue idempotency** — webhook gateway dedupes `${repo}:${pr}:${head_sha}`
2. **Cost-cap circuit breaker** — review worker checks `cost_caps` before LLM call; exceeded → neutral comment + check passes
3. **Embedding cache** — `embedding_cache.content_hash` is PK; never re-embed identical text
4. **Pre-flight token accounting** — `LlmGateway.EstimateTokens` (tiktoken) + drop-order before assembly; **the diff is never trimmed**
5. **Prompt-prefix caching** — system+rules prefix is stable; LiteLLM/Anthropic/OpenAI cache it
6. **Fallback model routing** — primary failure → fallback tier; both tracked in `pr_runs`

When you touch the review pipeline, verify all six still apply.

## Security / privacy guardrails (regulated aviation context)

- **No payload logging.** Diffs, code chunks, LLM request/response bodies never appear in logs or spans. Only structured metadata (token counts, model name, latency, error class).
- **Audit log = `pr_runs` + OTel traces.** Anything that bypasses these breaks SOC 2 evidence.
- **Secrets** come from `SecretsProvider`. Never from string literals, log lines, or test fixtures committed to git.
- **HMAC** every webhook before enqueuing. Invalid signature → 401, never enqueued, audit event emitted.
- **Right to delete:** disabling a repo tombstones all `code_chunks`, `review_comments`, and `pr_runs` for that repo.

## Style and tone for code

- **Don't add error handling, fallbacks, or validation for scenarios that can't happen.** Trust internal code. Validate only at trust boundaries (webhook payloads, LLM output, config).
- **Default to writing no comments.** Names should carry the meaning. Reserve comments for non-obvious *why*: a hidden constraint, a deliberate workaround, a surprising invariant.
- **No premature abstraction.** Three similar lines is better than a wrong abstraction. New port = real new external concern, not "might be useful later."
- **Errors flow up via `%w` wrapping.** `pr_runs.status` is the canonical record of pipeline outcomes.

## When testing

- Unit tests for `internal/core/...` use fakes from `internal/testing/fakes/`. No real infra.
- Adapter tests use `testcontainers-go` (Postgres) or LocalStack (SQS). They live next to the adapter.
- Smoke / harness tests live in `internal/testing/` and assemble a full pipeline against fakes.
- **Test the budget paths.** Budget-exceeded, drop-order, fallback-model — these are silent in normal traffic but critical when they fire.

## Open questions (currently deferred)

See `implementation-plan.md` → **Open questions** section. When one is resolved, move it to **Locked decisions** with the rationale.

# codereviewer

AI-assisted code review system. Posts inline comments, summary, and a required status check on every PR. Learns from accepted vs dismissed feedback.

See:
- [`docs/design.md`](./docs/design.md) — design spec
- [`implementation-plan.md`](./implementation-plan.md) — slice-by-slice build plan and progress
- [`CLAUDE.md`](./CLAUDE.md) — repo conventions and Claude Code guidance

## Quickstart — full stack in Docker

Prerequisites: Docker Desktop, an OpenAI (or Anthropic) API key, and a GitHub App registered with the permissions in `docs/design.md` Appendix C.

```sh
# 1. Configure secrets
cp .env.example .env
# Edit .env to set OPENAI_API_KEY, GITHUB_APP_ID, GITHUB_INSTALLATION_ID,
# GITHUB_WEBHOOK_SECRET, LITELLM_MASTER_KEY.

# 2. Drop your GitHub App private key
cp /path/to/your-app.private-key.pem docker/github-app-key.pem

# 3. Bring up the stack
docker compose up --build

# 4. Smee the webhook to your local gateway (or use ngrok)
# Configure the GitHub App webhook URL to http://localhost:8080/github/webhook
```

Services that come up:
- **postgres** (with pgvector) — port 5432
- **nats** (with JetStream) — port 4222, monitor 8222
- **litellm** — OpenAI-compatible proxy at port 4000
- **migrate** — one-shot init container; runs goose up against postgres
- **webhook-gateway** — chi HTTP server on port 8080, verifies HMAC and enqueues to NATS
- **review-worker** — consumes review-jobs queue
- **indexer-worker** — consumes index-jobs queue

Health checks: `curl http://localhost:8080/health`, `curl http://localhost:4000/health/liveliness`.

## Local Go development (no Docker)

```sh
go mod tidy           # CGO_ENABLED=1 recommended; see note below
go test ./...
go build ./...
```

**Note on CGO on Windows:** the indexer-worker imports `github.com/smacker/go-tree-sitter`, which requires CGO. Local `go build ./...` on Windows without a C toolchain falls back to a stub parser (`internal/adapters/parsertreesitter/parser_nocgo.go`) so the rest of the project still compiles. The real parser is always available in the Docker indexer image.

`go mod tidy` may prune tree-sitter deps when CGO is disabled; run with `CGO_ENABLED=1` (or in WSL/Linux/macOS) when adjusting dependencies.

## Backfilling historical comments

Once the stack is up and you've configured your GitHub App credentials, seed the retrieval index from the past 9 months of merged PRs:

```sh
docker compose run --rm -v $PWD/docker/dev.toml:/app/config.toml \
  $(docker compose images backfill-cli -q 2>/dev/null || docker compose build backfill-cli) \
  --config /app/config.toml --repo your-org/your-repo --window-days 270
```

Or, locally:

```sh
go run ./cmd/backfill-cli --config docker/dev.toml --repo your-org/your-repo --window-days 270
```

The backfill is idempotent on `github_id`; re-running with a longer window extends history without duplicating rows.

## Project status

Slices 0–3 — skeleton, infrastructure, naive review pipeline, retrieval + backfill — complete.

Remaining slices in [`implementation-plan.md`](./implementation-plan.md):
- Slice 4: rules sync, feedback worker, OTel observability
- Slice 5: Terraform deploy profile (lean-self-hosted EC2)

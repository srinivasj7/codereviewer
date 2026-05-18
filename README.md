# codereviewer

AI-assisted code review system. Posts inline comments, summary, and a required status check on every PR. Learns from accepted vs dismissed feedback.

See:
- [`docs/design.md`](./docs/design.md) — design spec
- [`implementation-plan.md`](./implementation-plan.md) — slice-by-slice build plan and progress
- [`CLAUDE.md`](./CLAUDE.md) — repo conventions and Claude Code guidance

## Verify locally (no GitHub / LLM credentials required)

```sh
make verify           # full run: bring up stack, test, fire synthetic webhooks
make verify-keep      # same, but leaves containers running for inspection
make verify-no-stack  # skip compose up; run against an already-up stack
# or directly:
bash scripts/verify-local.sh [--keep] [--no-stack]
```

`scripts/verify-local.sh` walks 8 phases: prereqs → env / dummy-PEM bootstrap → `go test` → `compose up` → storepostgres integration tests → HMAC-signed synthetic webhooks (pull_request, /context, reaction, tampered HMAC) → rate-limit + body-cap probes → disabled-repo smoke. It writes a throwaway `.env` and a 2048-bit RSA key on first run, then exits 0 if every check passes.

See [`fixtures/README.md`](./fixtures/README.md) for the canned webhook payloads.

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
- **admin-ui** — operator web UI on port 8090 (set `ADMIN_PASSWORD` in `.env`)
- **review-worker** — consumes review-jobs queue
- **indexer-worker** — consumes index-jobs queue
- **feedback-worker** — consumes feedback-events queue (reactions + replies on bot comments)
- **otel-collector** — OTLP/HTTP receiver on port 4318; debug exporter prints to stdout for local dev
- **rules-sync** (profile `tools`) — one-shot rules-repo sync; run with `docker compose run --rm rules-sync`

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

## Syncing rules

The review pipeline pulls rules from a separate git repo (config: `[rules].git_url`). Run a one-shot sync via:

```sh
docker compose --profile tools run --rm rules-sync --config /app/config.toml
```

Or locally: `go run ./cmd/rules-sync --config docker/dev.toml`.

Each rule file under `rules/**/*.md` has a YAML frontmatter block (scope, category, severity, title) and a markdown body. Files removed from the repo become `enabled=false` rows; nothing is hard-deleted.

## Feedback signals

Once `feedback-worker` is running, the system captures:
- **Reactions on bot comments** — `+1`/`heart`/`hooray`/`rocket` → `accepted` (`thumbs-up`); `-1`/`confused` → `dismissed` (`thumbs-down`).
- **Replies under bot comments** → `discussed` (`replied`).

The implicit "lines-modified-after-the-comment" signal from design §6.3 is tracked but not yet implemented; see [`implementation-plan.md`](./implementation-plan.md) deviations for slice 4.

## Observability

Workers emit OTLP/HTTP traces + metrics to the collector at `otel-collector:4318`. The dev collector config (`docker/otel-collector.yaml`) prints everything through the `debug` exporter — swap in OTLP-to-vendor exporters for production. To run with stdout-only observability instead, set `[observability].sink = "stdout"` in `dev.toml`.

## Admin UI

`cmd/admin-ui` (port `8090` in docker-compose) gives operators a browser-driven way to manage the deployment without editing TOML files.

```sh
# Set these in .env before bringing up the stack:
ADMIN_PASSWORD=...
ADMIN_SESSION_SECRET=...  # 32+ random bytes

docker compose up admin-ui   # http://localhost:8090
```

What you can do from the UI:
- **Sign in** with the admin password (or GitHub OAuth if you've configured `[admin.github_oauth]` and registered an OAuth app).
- **View the dashboard** — current overlay values, table counts, links to import/export.
- **Edit runtime settings** — `rules.git_url`, `cost.daily_usd_cap_default`, `llm.primary_model_url`, etc. These persist in the `app_settings` table and overlay the TOML defaults when workers restart. Bootstrap config (DB URL, secrets provider, bus URL) stays in TOML — by design. Any string setting may reference an env var via `${VAR}` and will resolve per-environment at worker boot — handy when the same export targets both compose and EC2 (e.g. set `observability.otlp_endpoint` to `${OTEL_ENDPOINT}`).
- **Export & import config** — download the current settings as TOML, upload a previously-saved file to restore. Secrets are never included.
- **Export & import durable data** — download `tenants`, `repos`, `code_chunks`, `rules`, and `review_comments` as one JSON file (embeddings included). Parent tables are bundled so a cold-start import on a fresh database satisfies foreign keys before any webhook traffic. Re-import upserts by primary key. `pr_runs`, caches, and `feedback_events` are intentionally excluded.
- **Automatic backups** — set `[admin].auto_export_enabled = true` and `[admin].export_dir = "/app/exports"` (volume mount). The admin process writes a timestamped TOML + JSON pair every `auto_export_hours`.

> Workers don't watch `app_settings` live. After saving a setting, `docker compose restart review-worker indexer-worker feedback-worker webhook-gateway` to apply.

## Review context — per-repo, issue trackers, ad-hoc notes

The reviewer pulls extra context into each prompt from configurable sources:

- **Repository conventions** — Define a named "instruction set" in the admin UI (e.g. "Go services") and assign it to one or more repos. A `.codereviewer.md` at the repo root overrides the assigned set when present.
- **JIRA / GitHub Issues / Linear** — Configure any subset in `[context]`. The reviewer scans PR titles, branch names, and bodies for issue references and fetches each ticket's summary + description.
- **Per-PR ad-hoc context** — Two surfaces:
  - Post `/context <body>` as a PR comment; the body is attached to that PR.
  - Use the admin UI's "PR context" page to paste text, upload a file, or fetch a URL (allow-list enforced via `[context].allowed_url_hosts`).

All sources merge into the prompt's `[CONTEXT]` section. Under token pressure the order of preservation is: diff → past reviews → related code → context → rules.

## Limits, retention, and operability (slice 4.7)

The system bounds its own growth and surfaces operational state for debugging:

- **Retention** — A janitor goroutine in `admin-ui` sweeps every `[retention].janitor_interval_hours` (default 6). Defaults: keep 365 days of `pr_runs`, 730 days of `feedback_events`, 90 days of `pr_context_items`, top 100k `embedding_cache` rows, last 30 auto-export files. Tune via `[retention]` or the admin Settings page.
- **Rate limits** — `/login` is capped at 5 attempts per IP per 15 min. `/github/webhook` is capped at 100 req/sec per IP and 1 MiB body. Both honor `X-Forwarded-For` for reverse-proxy deployments.
- **PII scrubber** — Every `ports.Logger` is wrapped to redact diff hunks, code fences, and oversize strings. Belt-and-suspenders for the no-payload-logging rule.
- **Recent runs viewer** — `/runs` in the admin UI shows the last 50 pipeline invocations (status, model, cost, error). One-click **retry** re-publishes the `ReviewJob` against the original head sha.
- **Enable / disable repo** — `/repos` lets you toggle a repo. Disabling tombstones its `code_chunks` and `review_comments`; subsequent webhooks short-circuit silently. Re-enabling means the next default-branch push re-indexes from scratch.

## Production deploy (slice 5)

### Container images

```sh
make docker-build-prod                  # local-arch distroless images
docker images | grep codereviewer-
```

CI tags (`v*`) trigger `.github/workflows/release.yml`, which builds multi-arch (amd64 + arm64/Graviton) distroless images for every binary and pushes them to GHCR as `ghcr.io/<owner>/codereviewer-<binary>:<tag>`.

### Lean self-hosted (single EC2)

```sh
cd infra/profiles/lean-self-hosted
cat > terraform.tfvars <<EOF
region      = "us-east-1"
image_owner = "ghcr.io/your-org/codereviewer"
image_tag   = "v0.5.0"
EOF
terraform init && terraform apply
```

The user-data script installs Docker, writes a production compose file, and registers a `codereviewer.service` systemd unit. Finish bootstrap by dropping `.env`, `config.toml`, the GitHub App private key, and the Postgres password into `/opt/codereviewer/` via SSM Session Manager — exact paths in `/opt/codereviewer/NEXT_STEPS.txt`.

If you'd rather skip Docker entirely, `infra/profiles/lean-self-hosted/systemd/` ships per-binary unit files that run the Go binaries directly against host Postgres + NATS.

See [`infra/profiles/lean-self-hosted/README.md`](./infra/profiles/lean-self-hosted/README.md) for details.

## Project status

Slices 0–5 complete — skeleton, infrastructure, review pipeline, retrieval + backfill, rules + feedback + observability, admin web UI, per-repo + issue trackers + ad-hoc context, limits + retention + operability, EC2 deploy profile.

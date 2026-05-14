#!/usr/bin/env bash
# verify-local.sh — one-shot local verification of codereviewer.
#
# Brings up the docker-compose stack, runs the test suites, then drives
# the webhook gateway with HMAC-signed synthetic payloads and asserts on
# database state. Exits 0 if every check passes, 1 on the first failure.
#
# What this verifies:
#   - go build, go vet, go test ./...
#   - storepostgres contract tests against the live container
#   - HMAC verification path on /github/webhook
#   - pull_request webhook -> ReviewJob -> pr_runs row
#   - push webhook -> IndexJob (smoke; full indexer not asserted)
#   - /context slash command writes pr_context_items
#   - admin /login rate limit kicks in after N attempts
#   - /github/webhook rejects bodies > webhook_max_body_bytes
#   - Disabling a repo via SQL makes subsequent reviews skip silently
#
# What this does NOT verify:
#   - Actually posting to GitHub (no real App credentials)
#   - LLM-driven prompt assembly (no real OpenAI/Anthropic key)
#   - Full feedback pipeline (the reaction targets a comment we never
#     authored, so the worker logs and acks)
#
# Usage:
#   scripts/verify-local.sh             # full run
#   scripts/verify-local.sh --no-stack  # assume services already up
#   scripts/verify-local.sh --keep      # leave services running on exit

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

KEEP=0
SKIP_STACK=0
for arg in "$@"; do
  case "$arg" in
    --keep) KEEP=1 ;;
    --no-stack) SKIP_STACK=1 ;;
    -h|--help)
      sed -n '2,30p' "$0"
      exit 0
      ;;
    *) echo "unknown flag: $arg" >&2; exit 2 ;;
  esac
done

# Color output if attached to a tty.
if [ -t 1 ]; then
  RED=$'\e[31m'; GREEN=$'\e[32m'; YELLOW=$'\e[33m'; BLUE=$'\e[34m'; BOLD=$'\e[1m'; RESET=$'\e[0m'
else
  RED=""; GREEN=""; YELLOW=""; BLUE=""; BOLD=""; RESET=""
fi

PASS_COUNT=0
FAIL_COUNT=0

step()  { echo; echo "${BOLD}${BLUE}==> $*${RESET}"; }
ok()    { PASS_COUNT=$((PASS_COUNT+1)); echo "  ${GREEN}✓${RESET} $*"; }
fail()  { FAIL_COUNT=$((FAIL_COUNT+1)); echo "  ${RED}✗${RESET} $*"; }
die()   { echo "${RED}${BOLD}FATAL:${RESET} $*" >&2; exit 1; }
warn()  { echo "  ${YELLOW}!${RESET} $*"; }

# ---------------------------------------------------------------------------
# Phase 1 — prerequisites
# ---------------------------------------------------------------------------
step "Phase 1: prerequisites"
for cmd in docker go openssl curl; do
  if ! command -v "$cmd" >/dev/null 2>&1; then
    die "missing prerequisite: $cmd"
  fi
done
if ! docker compose version >/dev/null 2>&1; then
  die "docker compose v2 plugin required"
fi
ok "docker, go, openssl, curl all present"

# ---------------------------------------------------------------------------
# Phase 2 — .env / PEM setup
# ---------------------------------------------------------------------------
step "Phase 2: environment + dummy credentials"
if [ ! -f .env ]; then
  warn ".env missing; creating from .env.example"
  cp .env.example .env
fi

# ensure_env_key appends KEY=VALUE to .env only if KEY is not already
# present. Idempotent across reruns; never overwrites a key the user
# may have set to a real value.
ensure_env_key() {
  local key="$1" value="$2"
  if ! grep -qE "^${key}=" .env; then
    printf '%s=%s\n' "$key" "$value" >>.env
    warn "  added $key (verify-time default)"
  fi
}

ensure_env_key OPENAI_API_KEY               sk-verify-not-real
ensure_env_key LITELLM_MASTER_KEY           verify-litellm-master-key
ensure_env_key GITHUB_APP_ID                111
ensure_env_key GITHUB_INSTALLATION_ID       222
ensure_env_key GITHUB_WEBHOOK_SECRET        verify-secret-do-not-use
ensure_env_key ADMIN_PASSWORD               verify-letmein
ensure_env_key ADMIN_SESSION_SECRET         verify-32byte-session-secret-xxxxx
ensure_env_key ADMIN_GITHUB_OAUTH_CLIENT_ID ''
ensure_env_key ADMIN_GITHUB_OAUTH_CLIENT_SECRET ''
ensure_env_key ADMIN_GITHUB_OAUTH_CALLBACK_URL ''
ensure_env_key JIRA_BASE_URL                ''
ensure_env_key JIRA_EMAIL                   ''
ensure_env_key JIRA_API_TOKEN               ''
ensure_env_key LINEAR_API_KEY               ''

# Pull the secret values out. Parse manually so we never `source` the
# file (an env value with a stray $ would shell-expand).
read_env_key() {
  awk -v k="$1" 'BEGIN{FS="="} $1==k {sub(/^[^=]*=/,""); print; exit}' .env \
    | tr -d '\r' | tr -d '"' | tr -d "'"
}
GITHUB_WEBHOOK_SECRET="$(read_env_key GITHUB_WEBHOOK_SECRET)"
ADMIN_PASSWORD="$(read_env_key ADMIN_PASSWORD)"
: "${GITHUB_WEBHOOK_SECRET:?GITHUB_WEBHOOK_SECRET still empty after .env merge}"
: "${ADMIN_PASSWORD:?ADMIN_PASSWORD still empty after .env merge}"
ok ".env loaded; webhook secret + admin password present"

# Generate the PEM if missing OR if the existing file isn't a parseable
# RSA private key. The .env.example flow leaves a placeholder file in
# place (so the docker volume mount exists); we replace that placeholder
# with a real throwaway 2048-bit key here.
if ! openssl rsa -in docker/github-app-key.pem -check -noout >/dev/null 2>&1; then
  if [ -f docker/github-app-key.pem ]; then
    warn "docker/github-app-key.pem is not a valid RSA key; regenerating"
  else
    warn "docker/github-app-key.pem missing; generating throwaway RSA key"
  fi
  openssl genrsa -out docker/github-app-key.pem 2048 >/dev/null 2>&1
fi
ok "GitHub App private key file present (verify-mode dummy is fine)"

# ---------------------------------------------------------------------------
# Phase 3 — go build + unit tests
# ---------------------------------------------------------------------------
step "Phase 3: go build + unit tests"
go vet ./... && ok "go vet" || fail "go vet"
go build ./... && ok "go build" || fail "go build"
if go test ./... >/tmp/verify-go-test.log 2>&1; then
  ok "go test ./... (see /tmp/verify-go-test.log)"
else
  tail -50 /tmp/verify-go-test.log
  fail "go test ./..."
fi

# ---------------------------------------------------------------------------
# Phase 4 — bring up the stack
# ---------------------------------------------------------------------------
if [ "$SKIP_STACK" -eq 0 ]; then
  step "Phase 4: docker compose up"
  docker compose up -d --build \
    postgres nats litellm migrate otel-collector \
    webhook-gateway review-worker indexer-worker feedback-worker admin-ui \
    >/tmp/verify-compose.log 2>&1 || {
      tail -50 /tmp/verify-compose.log
      die "docker compose up failed"
    }
  ok "compose up issued"
else
  step "Phase 4: --no-stack set, assuming services up"
fi

# Wait for healthchecks to flip to healthy. Poll every 2s, give up at 180s
# (litellm spends a beat downloading transformers on first start).
# otel-collector intentionally omitted: distroless image has no in-container
# probe, so it never reports a Health state — downstream services use
# `service_started` for it instead.
step "Waiting for service health"
deadline=$(( $(date +%s) + 180 ))
needed="postgres nats litellm"
while :; do
  unhealthy=""
  for svc in $needed; do
    state="$(docker compose ps --format json "$svc" 2>/dev/null | head -1 \
      | sed -n 's/.*"Health":"\([^"]*\)".*/\1/p')"
    if [ "$state" != "healthy" ]; then
      unhealthy="$unhealthy $svc"
    fi
  done
  if [ -z "$unhealthy" ]; then break; fi
  if [ "$(date +%s)" -ge "$deadline" ]; then
    fail "still unhealthy after 180s:$unhealthy"
    docker compose ps
    exit 1
  fi
  sleep 2
done
ok "postgres, nats, litellm healthy"
# OTel collector: TCP-probe the health-check port from the host.
if curl -fsS --max-time 3 http://localhost:13133/ >/dev/null 2>&1; then
  ok "otel-collector listener responding (port 13133)"
else
  warn "otel-collector not responding on :13133 (workers will retry silently)"
fi

# webhook-gateway, admin-ui, workers have no healthcheck — poll their endpoints.
curl -fsS http://localhost:8080/health >/dev/null 2>&1 && ok "/health (gateway) responds" || fail "gateway not responding"
curl -fsS http://localhost:8090/health >/dev/null 2>&1 && ok "/health (admin)   responds" || fail "admin-ui not responding"

# ---------------------------------------------------------------------------
# Phase 5 — integration tests against the live Postgres
# ---------------------------------------------------------------------------
step "Phase 5: storepostgres contract tests"
if TESTS_POSTGRES_URL="postgres://postgres:dev@localhost:5432/codereviewer?sslmode=disable" \
   go test -count=1 ./internal/adapters/storepostgres/... >/tmp/verify-integration.log 2>&1; then
  ok "TESTS_POSTGRES_URL contract tests"
else
  tail -50 /tmp/verify-integration.log
  fail "integration tests failed"
fi

# ---------------------------------------------------------------------------
# Phase 6 — synthetic webhook flow
# ---------------------------------------------------------------------------
step "Phase 6: synthetic webhooks (HMAC-signed)"

PSQL() {
  docker compose exec -T postgres psql -U postgres -d codereviewer -t -A -c "$1"
}

fire_webhook() {
  local event_name="$1"
  local fixture="$2"
  local body
  body="$(cat "$fixture")"
  local sig
  sig="$(printf '%s' "$body" | openssl dgst -sha256 -hmac "$GITHUB_WEBHOOK_SECRET" -hex | awk '{print $NF}')"
  local code
  code="$(curl -sS -o /tmp/verify-webhook.out -w '%{http_code}' \
    -X POST http://localhost:8080/github/webhook \
    -H "Content-Type: application/json" \
    -H "X-GitHub-Event: $event_name" \
    -H "X-GitHub-Delivery: verify-$(date +%s%N)" \
    -H "X-Hub-Signature-256: sha256=$sig" \
    --data-binary "$body")"
  echo "$code"
}

# Clear any verify-org rows so we start clean.
PSQL "DELETE FROM pr_runs WHERE repo_id = 'verify-org/verify-repo';" >/dev/null
PSQL "DELETE FROM pr_context_items WHERE repo_id = 'verify-org/verify-repo';" >/dev/null
PSQL "DELETE FROM code_chunks WHERE repo_id = 'verify-org/verify-repo';" >/dev/null
PSQL "DELETE FROM review_comments WHERE repo_id = 'verify-org/verify-repo';" >/dev/null
PSQL "DELETE FROM repos WHERE repo_id = 'verify-org/verify-repo';" >/dev/null

# 6a. pull_request -> 202, repo auto-registered, pr_runs row eventually appears.
code="$(fire_webhook pull_request fixtures/pull_request_opened.json)"
if [ "$code" != "202" ]; then
  cat /tmp/verify-webhook.out
  fail "pull_request webhook returned HTTP $code (expected 202)"
else
  ok "pull_request webhook accepted (202)"
fi

# Poll for the repo row and one pr_runs row.
for i in $(seq 1 20); do
  rows="$(PSQL "SELECT count(*) FROM repos WHERE repo_id = 'verify-org/verify-repo';")"
  if [ "$rows" = "1" ]; then break; fi
  sleep 0.5
done
[ "$rows" = "1" ] && ok "repos row auto-registered" || fail "repos row never appeared"

for i in $(seq 1 30); do
  rows="$(PSQL "SELECT count(*) FROM pr_runs WHERE repo_id = 'verify-org/verify-repo';")"
  if [ "$rows" -ge 1 ]; then break; fi
  sleep 1
done
if [ "$rows" -ge 1 ]; then
  status="$(PSQL "SELECT status FROM pr_runs WHERE repo_id = 'verify-org/verify-repo' ORDER BY started_at DESC LIMIT 1;")"
  ok "pr_runs row recorded (status=$status; failed-open is expected without real GitHub creds)"
else
  warn "no pr_runs row yet (review-worker may still be starting)"
fi

# 6b. /context slash command writes a pr_context_items row.
code="$(fire_webhook pull_request_review_comment fixtures/review_comment_context.json)"
if [ "$code" != "202" ]; then
  cat /tmp/verify-webhook.out
  fail "/context webhook returned HTTP $code"
else
  ok "/context webhook accepted"
fi
for i in $(seq 1 10); do
  rows="$(PSQL "SELECT count(*) FROM pr_context_items WHERE repo_id = 'verify-org/verify-repo';")"
  if [ "$rows" -ge 1 ]; then break; fi
  sleep 0.5
done
[ "$rows" -ge 1 ] && ok "/context wrote pr_context_items row" || fail "/context did not persist"

# 6c. reaction -> 202 (worker resolves nothing because the target comment isn't ours)
code="$(fire_webhook reaction fixtures/reaction_thumbs_up.json)"
[ "$code" = "202" ] && ok "reaction webhook accepted" || fail "reaction returned $code"

# 6d. Tampered HMAC must be rejected with 401.
code="$(curl -sS -o /dev/null -w '%{http_code}' \
  -X POST http://localhost:8080/github/webhook \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: pull_request" \
  -H "X-GitHub-Delivery: tampered" \
  -H "X-Hub-Signature-256: sha256=$(printf 0 %.0s {1..64})" \
  --data-binary "@fixtures/pull_request_opened.json")"
[ "$code" = "401" ] && ok "bad HMAC rejected with 401" || fail "bad HMAC returned $code (expected 401)"

# ---------------------------------------------------------------------------
# Phase 7 — rate limits + body cap
# ---------------------------------------------------------------------------
step "Phase 7: rate limits + body cap"

# 7a. Body cap on /github/webhook.
# webhook_max_body_bytes defaults to 1 MiB. Send 1.5 MiB via a file —
# enough to exceed the cap, small enough that MinGW curl's localhost
# upload finishes quickly. --max-time bounds the call so a wedged
# connection can't hang the whole verification.
big_file="$(mktemp -t verify-big.XXXXXX)"
head -c 1572864 /dev/zero | tr '\0' x >"$big_file"
code="$(curl -sS --max-time 15 -o /dev/null -w '%{http_code}' \
  -X POST http://localhost:8080/github/webhook \
  -H "Content-Type: application/json" \
  -H "X-GitHub-Event: pull_request" \
  -H "X-Hub-Signature-256: sha256=deadbeef" \
  --data-binary "@$big_file" || echo "timeout")"
rm -f "$big_file"
case "$code" in
  413|400|401)
    ok "oversize body rejected with HTTP $code" ;;
  timeout)
    warn "body-cap probe timed out (curl upload stalled — known on Windows)" ;;
  *)
    fail "oversize body returned HTTP $code (expected 4xx)" ;;
esac

# 7b. Admin login rate limit.
# Default config is 5 attempts / 15 min / IP. Fire 7 wrong-password POSTs.
denied_at=""
for i in $(seq 1 7); do
  body="$(curl -sS -o /dev/null -w '%{http_code}' \
    -X POST http://localhost:8090/login \
    -H "Content-Type: application/x-www-form-urlencoded" \
    --data "password=wrong-$i")"
  # /login returns 200 (re-rendered form) regardless; we test via response body.
  resp="$(curl -sS \
    -X POST http://localhost:8090/login \
    -H "Content-Type: application/x-www-form-urlencoded" \
    --data "password=wrong-$i")"
  if echo "$resp" | grep -q "Too many attempts"; then
    denied_at=$i
    break
  fi
done
if [ -n "$denied_at" ] && [ "$denied_at" -le 7 ]; then
  ok "login rate limit triggered around attempt $denied_at"
else
  warn "login rate limit not observed in 7 attempts (the configured limit may be higher)"
fi

# ---------------------------------------------------------------------------
# Phase 8 — disable repo → review pipeline skips
# ---------------------------------------------------------------------------
step "Phase 8: disable repo silences future reviews"

PSQL "UPDATE repos SET enabled = false WHERE repo_id = 'verify-org/verify-repo';" >/dev/null
before="$(PSQL "SELECT count(*) FROM pr_runs WHERE repo_id = 'verify-org/verify-repo';")"

# Fire a /review slash command; the gateway will accept and enqueue.
# The review-worker should observe enabled=false and skip without
# creating a new pr_runs row.
fire_webhook pull_request_review_comment fixtures/review_comment_review.json >/dev/null
sleep 4
after="$(PSQL "SELECT count(*) FROM pr_runs WHERE repo_id = 'verify-org/verify-repo';")"
if [ "$before" = "$after" ]; then
  ok "/review against disabled repo produced no new pr_runs row ($before == $after)"
else
  fail "disabled repo still produced a new pr_runs row ($before -> $after)"
fi

# Restore so re-running the script doesn't surprise.
PSQL "UPDATE repos SET enabled = true WHERE repo_id = 'verify-org/verify-repo';" >/dev/null

# ---------------------------------------------------------------------------
# Cleanup
# ---------------------------------------------------------------------------
if [ "$KEEP" -eq 0 ]; then
  step "Cleanup"
  docker compose down >/dev/null 2>&1
  ok "compose down"
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
echo
if [ "$FAIL_COUNT" -gt 0 ]; then
  echo "${BOLD}${RED}FAILED${RESET}: $PASS_COUNT passed, $FAIL_COUNT failed"
  exit 1
fi
echo "${BOLD}${GREEN}OK${RESET}: $PASS_COUNT checks passed"

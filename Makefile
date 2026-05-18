.PHONY: build test test-race test-integration verify verify-keep verify-no-stack typecheck lint tidy generate migrate-up dev-review dev-gateway docker-build-prod clean help

GO ?= go

help:
	@echo "Available targets:"
	@echo "  build         - Compile all packages and binaries"
	@echo "  test          - Run unit tests"
	@echo "  test-race     - Run unit tests with the race detector (requires CGO)"
	@echo "  test-integration - Run storepostgres tests against the live container"
	@echo "  verify        - One-shot local verification: stack + tests + synthetic webhooks"
	@echo "  verify-keep   - Same as verify, but leave the stack running on exit"
	@echo "  verify-no-stack - Run verification against an already-running stack"
	@echo "  typecheck     - Run go vet"
	@echo "  lint          - Run golangci-lint"
	@echo "  tidy          - Run go mod tidy"
	@echo "  generate      - Run sqlc generate (slice 1+)"
	@echo "  migrate-up    - Apply DB migrations via goose (slice 1+)"
	@echo "  dev-review    - Run review-worker with dev.toml"
	@echo "  dev-gateway   - Run webhook-gateway with dev.toml"
	@echo "  docker-build-prod - Build production distroless images for the local arch"
	@echo "  clean         - Remove build artifacts"

build:
	$(GO) build ./...

test:
	$(GO) test ./...

# test-race requires CGO (a C toolchain). Use in CI on Linux.
test-race:
	CGO_ENABLED=1 $(GO) test -race ./...

# test-integration runs adapter tests against external infrastructure
# brought up by docker compose. Skipped when TESTS_POSTGRES_URL is unset.
test-integration:
	@command -v docker >/dev/null 2>&1 || { echo "docker required for integration tests"; exit 1; }
	docker compose up -d postgres
	@TESTS_POSTGRES_URL=$${TESTS_POSTGRES_URL:-postgres://postgres:dev@localhost:5432/codereviewer?sslmode=disable} \
	  $(GO) test -count=1 ./internal/adapters/storepostgres/...

# verify runs scripts/verify-local.sh — full local verification of the
# admin UI, webhook gateway, retention, rate limits, and repo enable/
# disable using HMAC-signed synthetic webhooks. See scripts/verify-local.sh
# for the list of checks. No real GitHub or LLM credentials required.
verify:
	@bash scripts/verify-local.sh

verify-keep:
	@bash scripts/verify-local.sh --keep

verify-no-stack:
	@bash scripts/verify-local.sh --no-stack

typecheck:
	$(GO) vet ./...

lint:
	golangci-lint run

tidy:
	$(GO) mod tidy

generate:
	@command -v sqlc >/dev/null 2>&1 || { echo "sqlc not installed (slice 1+); install: https://docs.sqlc.dev/en/latest/overview/install.html"; exit 1; }
	sqlc generate -f internal/db/sqlc.yaml

migrate-up:
	@command -v goose >/dev/null 2>&1 || { echo "goose not installed (slice 1+); install: go install github.com/pressly/goose/v3/cmd/goose@latest"; exit 1; }
	goose -dir internal/db/migrations postgres "$$POSTGRES_URL" up

dev-review:
	$(GO) run ./cmd/review-worker --config=dev.toml

dev-gateway:
	$(GO) run ./cmd/webhook-gateway --config=dev.toml

# Build distroless production images for the local arch. CI builds the
# multi-arch versions via the release workflow.
IMAGE_PREFIX ?= codereviewer
IMAGE_TAG    ?= dev
STATIC_CMDS  := webhook-gateway review-worker feedback-worker admin-ui backfill-cli migrate
docker-build-prod:
	@command -v docker >/dev/null 2>&1 || { echo "docker required"; exit 1; }
	@for cmd in $(STATIC_CMDS); do \
	  echo "==> building $$cmd (final-static)"; \
	  docker build --target final-static \
	    --build-arg CMD=$$cmd \
	    -t $(IMAGE_PREFIX)-$$cmd:$(IMAGE_TAG) \
	    -f docker/Dockerfile.prod . || exit 1; \
	done
	@echo "==> building indexer-worker (final-cc)"
	docker build --target final-cc \
	  --build-arg CMD=indexer-worker \
	  -t $(IMAGE_PREFIX)-indexer-worker:$(IMAGE_TAG) \
	  -f docker/Dockerfile.prod .
	@echo "==> building rules-sync (final-git)"
	docker build --target final-git \
	  --build-arg CMD=rules-sync \
	  -t $(IMAGE_PREFIX)-rules-sync:$(IMAGE_TAG) \
	  -f docker/Dockerfile.prod .

clean:
	rm -rf bin/ dist/ coverage.out coverage.html

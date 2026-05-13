.PHONY: build test typecheck lint tidy generate migrate-up dev-review dev-gateway clean help

GO ?= go

help:
	@echo "Available targets:"
	@echo "  build         - Compile all packages and binaries"
	@echo "  test          - Run unit tests"
	@echo "  test-race     - Run unit tests with the race detector (requires CGO)"
	@echo "  typecheck     - Run go vet"
	@echo "  lint          - Run golangci-lint"
	@echo "  tidy          - Run go mod tidy"
	@echo "  generate      - Run sqlc generate (slice 1+)"
	@echo "  migrate-up    - Apply DB migrations via goose (slice 1+)"
	@echo "  dev-review    - Run review-worker with dev.toml"
	@echo "  dev-gateway   - Run webhook-gateway with dev.toml"
	@echo "  clean         - Remove build artifacts"

build:
	$(GO) build ./...

test:
	$(GO) test ./...

# test-race requires CGO (a C toolchain). Use in CI on Linux.
test-race:
	CGO_ENABLED=1 $(GO) test -race ./...

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

clean:
	rm -rf bin/ dist/ coverage.out coverage.html

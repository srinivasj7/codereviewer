# codereviewer

AI-assisted code review system. Posts inline comments, summary, and a required status check on every PR. Learns from accepted vs dismissed feedback.

See:
- [`docs/design.md`](./docs/design.md) — design spec
- [`implementation-plan.md`](./implementation-plan.md) — slice-by-slice build plan and progress
- [`CLAUDE.md`](./CLAUDE.md) — repo conventions and Claude Code guidance

## Bootstrap

```sh
go mod tidy
make test
make build
```

On Windows without `make`:

```powershell
go mod tidy
go test -race ./...
go build ./...
```

## Status

Slice 0 — skeleton, contracts, smoke test.

The current build has in-memory adapters only. Real Postgres, GitHub, LiteLLM, NATS adapters arrive in slice 1.

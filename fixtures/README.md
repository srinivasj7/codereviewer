# Sample GitHub webhook payloads

Minimal JSON files matching the field subset declared in
`internal/schemas/webhook_github.go`. Used by `scripts/verify-local.sh`
to drive the webhook gateway without a real GitHub App.

Each payload has a matching `X-GitHub-Event` header for `curl`:

| File | X-GitHub-Event |
|---|---|
| `pull_request_opened.json` | `pull_request` |
| `push_main.json` | `push` |
| `review_comment_context.json` | `pull_request_review_comment` |
| `review_comment_review.json` | `pull_request_review_comment` |
| `reaction_thumbs_up.json` | `reaction` |

The HMAC signature (`X-Hub-Signature-256: sha256=<hex>`) is computed
at request time using whatever `GITHUB_WEBHOOK_SECRET` is set in your
`.env`. The verify script does this for you.

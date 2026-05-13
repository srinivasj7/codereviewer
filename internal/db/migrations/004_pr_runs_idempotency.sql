-- +goose Up
-- +goose StatementBegin
ALTER TABLE pr_runs ADD COLUMN idempotency_key TEXT;
CREATE UNIQUE INDEX pr_runs_idempotency_key_idx ON pr_runs (idempotency_key) WHERE idempotency_key IS NOT NULL;
-- +goose StatementEnd

-- +goose Down
SELECT 1;

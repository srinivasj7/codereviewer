-- +goose Up
-- +goose StatementBegin
CREATE TABLE job_idempotency (
  idempotency_key  TEXT PRIMARY KEY,
  observed_at      TIMESTAMPTZ DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
SELECT 1;

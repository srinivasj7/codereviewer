-- +goose Up
-- +goose StatementBegin
CREATE TABLE embedding_cache (
  content_hash  TEXT PRIMARY KEY,
  embedding     vector(1024) NOT NULL,
  created_at    TIMESTAMPTZ DEFAULT now()
);
-- +goose StatementEnd

-- +goose Down
SELECT 1;

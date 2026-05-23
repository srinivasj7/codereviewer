-- +goose Up
-- +goose StatementBegin
-- Slice 6B — multi-VCS routing. A single deployment can now serve
-- both GitHub and Bitbucket Cloud repositories. Each repos row gets
-- a provider column to pin its VCS adapter; existing rows are
-- backfilled to 'github' on the assumption that pre-6B deployments
-- ran the GitHub adapter.
ALTER TABLE repos
  ADD COLUMN provider TEXT NOT NULL DEFAULT 'github'
    CHECK (provider IN ('github', 'bitbucket'));

-- Backfill is implicit via the DEFAULT clause above; no UPDATE needed
-- because all existing rows already used the GitHub adapter at the
-- application layer.
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
ALTER TABLE repos DROP COLUMN IF EXISTS provider;
-- +goose StatementEnd

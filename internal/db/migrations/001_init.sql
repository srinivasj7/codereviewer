-- +goose Up
-- +goose StatementBegin
CREATE EXTENSION IF NOT EXISTS vector;
CREATE EXTENSION IF NOT EXISTS pg_trgm;

CREATE TABLE tenants (
  tenant_id   UUID PRIMARY KEY,
  name        TEXT NOT NULL,
  created_at  TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE repos (
  repo_id              UUID PRIMARY KEY,
  tenant_id            UUID NOT NULL REFERENCES tenants,
  owner                TEXT NOT NULL,
  name                 TEXT NOT NULL,
  default_branch       TEXT NOT NULL,
  indexed_commit_sha   TEXT,
  backfill_window_days INT  DEFAULT 270,
  enabled              BOOL DEFAULT true,
  UNIQUE (owner, name)
);

CREATE TABLE code_chunks (
  chunk_id         UUID PRIMARY KEY,
  tenant_id        UUID NOT NULL,
  repo_id          UUID NOT NULL REFERENCES repos,
  file_path        TEXT NOT NULL,
  symbol_name      TEXT,
  symbol_kind      TEXT,
  start_line       INT,
  end_line         INT,
  content          TEXT NOT NULL,
  content_hash     TEXT NOT NULL,
  commit_sha       TEXT NOT NULL,
  embedding        vector(1024),
  last_indexed_at  TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX code_chunks_embedding_idx ON code_chunks USING hnsw (embedding vector_cosine_ops);
CREATE INDEX code_chunks_locator_idx   ON code_chunks (tenant_id, repo_id, file_path);

CREATE TABLE review_comments (
  comment_id       UUID PRIMARY KEY,
  tenant_id        UUID NOT NULL,
  repo_id          UUID NOT NULL,
  pr_number        INT NOT NULL,
  source           TEXT NOT NULL,
  github_id        BIGINT,
  file_path        TEXT,
  start_line       INT,
  end_line         INT,
  diff_hunk        TEXT,
  comment_text     TEXT NOT NULL,
  category         TEXT,
  outcome          TEXT,
  outcome_signal   TEXT,
  embedding        vector(1024),
  created_at       TIMESTAMPTZ DEFAULT now(),
  resolved_at      TIMESTAMPTZ
);
CREATE UNIQUE INDEX review_comments_github_id_idx ON review_comments (github_id) WHERE github_id IS NOT NULL;
CREATE INDEX review_comments_embedding_idx       ON review_comments USING hnsw (embedding vector_cosine_ops);
CREATE INDEX review_comments_outcome_idx         ON review_comments (tenant_id, outcome);

CREATE TABLE rules (
  rule_id        UUID PRIMARY KEY,
  tenant_id      UUID NOT NULL,
  scope          TEXT NOT NULL,
  title          TEXT NOT NULL,
  description    TEXT NOT NULL,
  source_commit  TEXT,
  embedding      vector(1024),
  enabled        BOOL DEFAULT true,
  updated_at     TIMESTAMPTZ DEFAULT now()
);
CREATE INDEX rules_embedding_idx ON rules USING hnsw (embedding vector_cosine_ops);

CREATE TABLE pr_runs (
  run_id         UUID PRIMARY KEY,
  tenant_id      UUID NOT NULL,
  repo_id        UUID NOT NULL,
  pr_number      INT NOT NULL,
  head_sha       TEXT NOT NULL,
  trigger        TEXT NOT NULL,
  model_used     TEXT,
  tokens_in      INT,
  tokens_out     INT,
  cost_usd       NUMERIC(10,4),
  status         TEXT NOT NULL,
  started_at     TIMESTAMPTZ DEFAULT now(),
  finished_at    TIMESTAMPTZ
);
CREATE INDEX pr_runs_lookup_idx ON pr_runs (tenant_id, repo_id, pr_number, started_at DESC);

CREATE TABLE feedback_events (
  event_id     UUID PRIMARY KEY,
  tenant_id    UUID NOT NULL,
  comment_id   UUID NOT NULL REFERENCES review_comments,
  signal       TEXT NOT NULL,
  observed_at  TIMESTAMPTZ DEFAULT now()
);

CREATE TABLE cost_caps (
  tenant_id        UUID NOT NULL,
  repo_id          UUID,
  daily_usd_cap    NUMERIC(10,2) DEFAULT 5.00,
  per_pr_token_cap INT DEFAULT 30000,
  PRIMARY KEY (tenant_id, repo_id)
);
-- +goose StatementEnd

-- +goose Down
-- forward-only migrations; revert via a new compensating migration
SELECT 1;

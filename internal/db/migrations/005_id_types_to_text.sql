-- +goose Up
-- +goose StatementBegin

-- Convert tenant_id and repo_id columns from UUID to TEXT across every
-- table that carries them. Slice 1 stored "default-tenant" and
-- "owner/name" strings through the application layer; the schema's
-- UUID columns would have rejected those at first write. Slice 2 makes
-- the system actually persist data, so the type mismatch must be
-- resolved before any production writes.

ALTER TABLE repos       DROP CONSTRAINT repos_tenant_id_fkey;
ALTER TABLE code_chunks DROP CONSTRAINT code_chunks_repo_id_fkey;

ALTER TABLE tenants         ALTER COLUMN tenant_id TYPE TEXT USING tenant_id::text;
ALTER TABLE repos           ALTER COLUMN tenant_id TYPE TEXT USING tenant_id::text;
ALTER TABLE repos           ALTER COLUMN repo_id   TYPE TEXT USING repo_id::text;
ALTER TABLE code_chunks     ALTER COLUMN tenant_id TYPE TEXT USING tenant_id::text;
ALTER TABLE code_chunks     ALTER COLUMN repo_id   TYPE TEXT USING repo_id::text;
ALTER TABLE review_comments ALTER COLUMN tenant_id TYPE TEXT USING tenant_id::text;
ALTER TABLE review_comments ALTER COLUMN repo_id   TYPE TEXT USING repo_id::text;
ALTER TABLE rules           ALTER COLUMN tenant_id TYPE TEXT USING tenant_id::text;
ALTER TABLE pr_runs         ALTER COLUMN tenant_id TYPE TEXT USING tenant_id::text;
ALTER TABLE pr_runs         ALTER COLUMN repo_id   TYPE TEXT USING repo_id::text;
ALTER TABLE feedback_events ALTER COLUMN tenant_id TYPE TEXT USING tenant_id::text;
ALTER TABLE cost_caps       ALTER COLUMN tenant_id TYPE TEXT USING tenant_id::text;
ALTER TABLE cost_caps       ALTER COLUMN repo_id   TYPE TEXT USING repo_id::text;

ALTER TABLE repos       ADD CONSTRAINT repos_tenant_id_fkey       FOREIGN KEY (tenant_id) REFERENCES tenants(tenant_id);
ALTER TABLE code_chunks ADD CONSTRAINT code_chunks_repo_id_fkey   FOREIGN KEY (repo_id)   REFERENCES repos(repo_id);

-- +goose StatementEnd

-- +goose Down
SELECT 1;

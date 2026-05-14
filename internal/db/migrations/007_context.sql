-- +goose Up
-- +goose StatementBegin

-- instruction_sets: named, reusable prompt instructions that admins
-- assign to one or more repos via repo_instruction_sets. Body is
-- markdown; the review pipeline injects it under a "Repository
-- conventions" heading. Multi-tenant by tenant_id.
CREATE TABLE instruction_sets (
    set_id      TEXT PRIMARY KEY,
    tenant_id   TEXT NOT NULL,
    name        TEXT NOT NULL,
    body        TEXT NOT NULL,
    updated_at  TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by  TEXT NOT NULL DEFAULT 'system',
    UNIQUE (tenant_id, name)
);

-- repo_instruction_sets: many-to-one (repo -> set). A repo with no row
-- here falls back to no instructions. A .codereviewer.md in the target
-- repo, if present, overrides the assigned set entirely.
CREATE TABLE repo_instruction_sets (
    repo_id  TEXT PRIMARY KEY REFERENCES repos(repo_id),
    set_id   TEXT NOT NULL REFERENCES instruction_sets(set_id)
);

-- pr_context_items: ad-hoc context attached to a specific PR via the
-- /context slash command or the admin UI form. Reviewed on the next
-- review run; not auto-cleaned (operators can prune).
--
-- source discriminates: "text" | "file:<name>" | "url:<host>" | "command".
CREATE TABLE pr_context_items (
    item_id    TEXT PRIMARY KEY,
    tenant_id  TEXT NOT NULL,
    repo_id    TEXT NOT NULL,
    pr_number  INT  NOT NULL,
    source     TEXT NOT NULL,
    title      TEXT NOT NULL DEFAULT '',
    body       TEXT NOT NULL,
    created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
    created_by TEXT NOT NULL DEFAULT 'system'
);
CREATE INDEX pr_context_items_by_pr ON pr_context_items (tenant_id, repo_id, pr_number, created_at DESC);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS pr_context_items;
DROP TABLE IF EXISTS repo_instruction_sets;
DROP TABLE IF EXISTS instruction_sets;

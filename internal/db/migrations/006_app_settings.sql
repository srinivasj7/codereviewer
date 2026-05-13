-- +goose Up
-- +goose StatementBegin

-- app_settings holds tunable operator knobs that the admin UI can edit
-- without redeploying. Each row is one key/value pair; values are TEXT
-- so the application owns type interpretation (TOML-style strings,
-- numbers, booleans all flatten to text and parse on read).
--
-- TOML stays the source of truth for bootstrap knobs (postgres URL,
-- secrets provider, bus URL, listen addr). Anything reachable here is
-- by definition runtime-tunable.
CREATE TABLE app_settings (
    setting_key   TEXT PRIMARY KEY,
    setting_value TEXT NOT NULL,
    updated_at    TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_by    TEXT NOT NULL DEFAULT 'system'
);

-- +goose StatementEnd

-- +goose Down
DROP TABLE IF EXISTS app_settings;

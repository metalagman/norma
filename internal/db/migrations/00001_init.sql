-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS schema_migrations (
    version INTEGER PRIMARY KEY,
    applied_at TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS runs (
    run_id TEXT PRIMARY KEY,
    created_at TEXT NOT NULL,
    goal TEXT NOT NULL,
    status TEXT NOT NULL,
    iteration INTEGER NOT NULL DEFAULT 0,
    current_step_index INTEGER NOT NULL DEFAULT 0,
    verdict TEXT NULL,
    run_dir TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS steps (
    run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    step_index INTEGER NOT NULL,
    role TEXT NOT NULL,
    iteration INTEGER NOT NULL,
    status TEXT NOT NULL,
    step_dir TEXT NOT NULL,
    started_at TEXT NOT NULL,
    ended_at TEXT NULL,
    summary TEXT NULL,
    PRIMARY KEY (run_id, step_index)
);

CREATE TABLE IF NOT EXISTS events (
    run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    seq INTEGER NOT NULL,
    ts TEXT NOT NULL,
    type TEXT NOT NULL,
    message TEXT NOT NULL,
    data_json TEXT NULL,
    PRIMARY KEY (run_id, seq)
);

CREATE TABLE IF NOT EXISTS kv_run (
    run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value_json TEXT NOT NULL,
    PRIMARY KEY (run_id, key)
);

INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES(1, datetime('now'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM schema_migrations WHERE version = 1;
DROP TABLE IF EXISTS kv_run;
DROP TABLE IF EXISTS events;
DROP TABLE IF EXISTS steps;
DROP TABLE IF EXISTS runs;
DROP TABLE IF EXISTS schema_migrations;
-- +goose StatementEnd

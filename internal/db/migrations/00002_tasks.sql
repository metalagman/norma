-- +goose Up
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS tasks (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    title TEXT NOT NULL,
    status TEXT NOT NULL,
    run_id TEXT NULL REFERENCES runs(run_id) ON DELETE SET NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL
);

INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES(2, datetime('now'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM schema_migrations WHERE version = 2;
DROP TABLE IF EXISTS tasks;
-- +goose StatementEnd

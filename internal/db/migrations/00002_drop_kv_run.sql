-- +goose Up
-- +goose StatementBegin
DROP TABLE IF EXISTS kv_run;

INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES(2, datetime('now'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
CREATE TABLE IF NOT EXISTS kv_run (
    run_id TEXT NOT NULL REFERENCES runs(run_id) ON DELETE CASCADE,
    key TEXT NOT NULL,
    value_json TEXT NOT NULL,
    PRIMARY KEY (run_id, key)
);

DELETE FROM schema_migrations WHERE version = 2;
-- +goose StatementEnd

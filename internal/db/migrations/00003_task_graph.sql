-- +goose Up
-- +goose StatementBegin
ALTER TABLE tasks ADD COLUMN goal TEXT NOT NULL DEFAULT '';
ALTER TABLE tasks ADD COLUMN acceptance_criteria_json TEXT NOT NULL DEFAULT '[]';

UPDATE tasks SET goal = title WHERE goal = '';

CREATE TABLE IF NOT EXISTS task_edges (
    task_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    depends_on_id INTEGER NOT NULL REFERENCES tasks(id) ON DELETE CASCADE,
    PRIMARY KEY (task_id, depends_on_id)
);

INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES(3, datetime('now'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
DELETE FROM schema_migrations WHERE version = 3;
DROP TABLE IF EXISTS task_edges;
-- SQLite cannot drop columns; leave goal/acceptance_criteria_json in place.
-- +goose StatementEnd

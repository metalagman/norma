-- +goose Up
-- +goose StatementBegin
UPDATE runs
SET verdict = lower(verdict)
WHERE verdict IN ('PASS', 'FAIL');

INSERT OR IGNORE INTO schema_migrations(version, applied_at)
VALUES(3, datetime('now'));
-- +goose StatementEnd

-- +goose Down
-- +goose StatementBegin
UPDATE runs
SET verdict = upper(verdict)
WHERE verdict IN ('pass', 'fail');

DELETE FROM schema_migrations WHERE version = 3;
-- +goose StatementEnd

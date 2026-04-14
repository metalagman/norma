package state

import (
	"context"
	"database/sql"
	"fmt"
	"strings"

	"github.com/normahq/norma/internal/apps/relay/auth"
	"github.com/tgbotkit/runtime/updatepoller"
	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

type sqliteProvider struct {
	db      *sql.DB
	appKV   *sqliteKVStore
	mcpKV   *sqliteKVStore
	session *sqliteSessionStore
	offset  *sqliteOffsetStore
	collab  *auth.CollaboratorStore
}

var _ Provider = (*sqliteProvider)(nil)

// NewSQLiteProvider initializes relay state storage in a SQLite database.
func NewSQLiteProvider(ctx context.Context, path string) (Provider, error) {
	dbPath := strings.TrimSpace(path)
	if dbPath == "" {
		return nil, fmt.Errorf("sqlite path is required")
	}

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		return nil, fmt.Errorf("open relay state sqlite db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := applySQLitePragmas(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrate(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}

	return &sqliteProvider{
		db:      db,
		appKV:   &sqliteKVStore{db: db, namespace: NamespaceApp},
		mcpKV:   &sqliteKVStore{db: db, namespace: NamespaceSessionMCP},
		session: &sqliteSessionStore{db: db},
		offset:  &sqliteOffsetStore{db: db},
		collab:  auth.NewCollaboratorStore(db),
	}, nil
}

func (p *sqliteProvider) AppKV() KVStore {
	return p.appKV
}

func (p *sqliteProvider) SessionMCPKV() KVStore {
	return p.mcpKV
}

func (p *sqliteProvider) Sessions() SessionStore {
	return p.session
}

func (p *sqliteProvider) PollingOffsetStore() updatepoller.OffsetStore {
	return p.offset
}

func (p *sqliteProvider) Collaborators() CollaboratorStore {
	return p.collab
}

func (p *sqliteProvider) Close() error {
	return p.db.Close()
}

func applySQLitePragmas(ctx context.Context, db *sql.DB) error {
	stmts := []string{
		"PRAGMA foreign_keys=ON;",
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
	}
	for _, stmt := range stmts {
		if _, err := db.ExecContext(ctx, stmt); err != nil {
			// WAL can be unsupported in some environments. Ignore only this one.
			if stmt == "PRAGMA journal_mode=WAL;" {
				continue
			}
			return fmt.Errorf("apply relay state pragma %q: %w", stmt, err)
		}
	}
	return nil
}

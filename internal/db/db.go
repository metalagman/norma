// Package db provides database connectivity and migration logic for norma.
package db

import (
	"database/sql"
	"embed"
	"fmt"
	"io"
	"log"

	"github.com/pressly/goose/v3"
	_ "modernc.org/sqlite" // pure-Go SQLite driver
)

// Open opens the SQLite database with required pragmas and migrations.
func Open(path string) (*sql.DB, error) {
	db, err := sql.Open("sqlite", path)
	if err != nil {
		return nil, fmt.Errorf("open db: %w", err)
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)

	if err := applyPragmas(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	if err := migrate(db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}

func applyPragmas(db *sql.DB) error {
	stmts := []string{
		"PRAGMA foreign_keys=ON;",
		"PRAGMA journal_mode=WAL;",
		"PRAGMA busy_timeout=5000;",
	}
	for _, stmt := range stmts {
		if _, err := db.Exec(stmt); err != nil {
			if stmt == "PRAGMA journal_mode=WAL;" {
				log.Printf("sqlite: WAL mode not enabled: %v", err)
				continue
			}
			return fmt.Errorf("apply pragma %q: %w", stmt, err)
		}
	}
	return nil
}

//go:embed migrations/*.sql
var migrationsFS embed.FS

func migrate(db *sql.DB) error {
	goose.SetBaseFS(migrationsFS)
	goose.SetLogger(log.New(io.Discard, "", 0))
	if err := goose.SetDialect("sqlite3"); err != nil {
		return fmt.Errorf("set goose dialect: %w", err)
	}
	if err := goose.Up(db, "migrations"); err != nil {
		return fmt.Errorf("apply migrations: %w", err)
	}
	return nil
}

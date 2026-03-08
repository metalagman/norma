package prunecmd

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"github.com/metalagman/norma/internal/db"
)

func openDB(ctx context.Context) (*sql.DB, string, func(), error) {
	repoRoot, err := os.Getwd()
	if err != nil {
		return nil, "", func() {}, err
	}
	normaDir := filepath.Join(repoRoot, ".norma")
	if err := os.MkdirAll(normaDir, 0o700); err != nil {
		return nil, "", func() {}, err
	}
	dbPath := filepath.Join(normaDir, "norma.db")
	storeDB, err := db.Open(ctx, dbPath)
	if err != nil {
		return nil, "", func() {}, err
	}
	return storeDB, repoRoot, func() { _ = storeDB.Close() }, nil
}

package prunecmd

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"

	"github.com/normahq/norma/v2/internal/db"
)

func openDB(ctx context.Context) (*sql.DB, string, func(), error) {
	workingDir, err := os.Getwd()
	if err != nil {
		return nil, "", func() {}, err
	}
	normaDir := filepath.Join(workingDir, ".norma")
	if err := os.MkdirAll(normaDir, 0o700); err != nil {
		return nil, "", func() {}, err
	}
	dbPath := filepath.Join(normaDir, "norma.db")
	storeDB, err := db.Open(ctx, dbPath)
	if err != nil {
		return nil, "", func() {}, err
	}
	return storeDB, workingDir, func() { _ = storeDB.Close() }, nil
}

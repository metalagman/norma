// Package run implements the orchestrator for the norma development lifecycle.
package run

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// Lock handles exclusive access to norma loop.
type Lock struct {
	f *os.File
}

// AcquireRunLock tries to acquire the run lock.
func AcquireRunLock(normaDir string) (*Lock, error) {
	if err := os.MkdirAll(filepath.Join(normaDir, "locks"), 0o755); err != nil {
		return nil, fmt.Errorf("create locks dir: %w", err)
	}
	f, err := os.OpenFile(filepath.Join(normaDir, "locks", "run.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("acquire flock: %w", err)
	}
	return &Lock{f: f}, nil
}

// TryAcquireRunLock tries to acquire the run lock without blocking.
func TryAcquireRunLock(normaDir string) (*Lock, bool, error) {
	l, err := AcquireRunLock(normaDir)
	if err != nil {
		return nil, false, nil
	}
	return l, true, nil
}

// Release releases the run lock.
func (l *Lock) Release() error {
	if l.f == nil {
		return nil
	}
	defer func() { _ = l.f.Close() }()
	return syscall.Flock(int(l.f.Fd()), syscall.LOCK_UN)
}

package run

import (
	"fmt"
	"os"
	"path/filepath"
	"syscall"
)

// RunLock provides an exclusive lock for norma run.
type RunLock struct {
	file *os.File
}

// AcquireRunLock creates and locks .norma/locks/run.lock.
func AcquireRunLock(normaDir string) (*RunLock, error) {
	locksDir := filepath.Join(normaDir, "locks")
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		return nil, fmt.Errorf("create locks dir: %w", err)
	}
	lockPath := filepath.Join(locksDir, "run.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX); err != nil {
		_ = file.Close()
		return nil, fmt.Errorf("lock run.lock: %w", err)
	}
	return &RunLock{file: file}, nil
}

// TryAcquireRunLock attempts to acquire the run lock without blocking.
func TryAcquireRunLock(normaDir string) (*RunLock, bool, error) {
	locksDir := filepath.Join(normaDir, "locks")
	if err := os.MkdirAll(locksDir, 0o755); err != nil {
		return nil, false, fmt.Errorf("create locks dir: %w", err)
	}
	lockPath := filepath.Join(locksDir, "run.lock")
	file, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, false, fmt.Errorf("open lock file: %w", err)
	}
	if err := syscall.Flock(int(file.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = file.Close()
		return nil, false, nil
	}
	return &RunLock{file: file}, true, nil
}

// Release releases the lock.
func (l *RunLock) Release() error {
	if l == nil || l.file == nil {
		return nil
	}
	if err := syscall.Flock(int(l.file.Fd()), syscall.LOCK_UN); err != nil {
		_ = l.file.Close()
		return err
	}
	return l.file.Close()
}

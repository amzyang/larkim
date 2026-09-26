package sync

import (
	"errors"
	"path/filepath"

	"github.com/gofrs/flock"
)

// ErrLocked means another process (usually the daemon) owns the sweep.
var ErrLocked = errors.New("the sweep is owned by another process")

// Lock names the one process that runs the sweep over a data directory, which
// is what owns the global cursors. It is not a write lock: a caller that fails
// to acquire it still pulls whatever its user reaches for, because those calls
// upsert ids Feishu just answered for and land the same rows either way.
type Lock struct {
	fl *flock.Flock
}

// TryLock acquires the data-dir lock without blocking.
func TryLock(dataDir string) (*Lock, error) {
	fl := flock.New(filepath.Join(dataDir, "daemon.lock"))
	ok, err := fl.TryLock()
	if err != nil {
		return nil, err
	}
	if !ok {
		return nil, ErrLocked
	}
	return &Lock{fl: fl}, nil
}

// Unlock releases the lock.
func (l *Lock) Unlock() error { return l.fl.Unlock() }

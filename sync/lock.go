package sync

import (
	"errors"
	"path/filepath"

	"github.com/gofrs/flock"
)

// ErrLocked means another process (usually the daemon) owns the sync lock.
var ErrLocked = errors.New("sync lock held by another process")

// Lock guards single-writer access to the data directory. Callers that fail to
// acquire it should read the store only and leave syncing to the lock holder.
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

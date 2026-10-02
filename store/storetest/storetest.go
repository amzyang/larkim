// Package storetest opens real SQLite stores for tests without paying for the
// migrations on every one: under -race the transpiled SQLite engine makes a
// fresh migration run cost most of a second.
package storetest

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/amzyang/larkim/store"
)

var template = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "storetest")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.db")
	st, err := store.Open(path)
	if err != nil {
		return nil, err
	}
	if _, err := st.DB().Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		st.Close()
		return nil, err
	}
	if err := st.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

// Open is store.Open for tests. A path with no database yet starts as a copy
// of an already-migrated one; an existing database is opened as it is, so a
// test can reopen what it wrote.
func Open(t testing.TB, path string) (*store.Store, error) {
	t.Helper()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		body, err := template()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return store.Open(path)
}

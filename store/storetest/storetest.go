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
	return openCopy(t, path, template)
}

// OpenSeed is Open with a fixture already in the file. seed runs once per key,
// on a migrated database, and the bytes are reused; two calls with one key
// must pass the same seed. A test's later writes stay in its own copy.
func OpenSeed(t testing.TB, path, key string, seed func(*store.Store) error) (*store.Store, error) {
	t.Helper()
	return openCopy(t, path, seeded(key, seed))
}

func openCopy(t testing.TB, path string, body func() ([]byte, error)) (*store.Store, error) {
	t.Helper()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		buf, err := body()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, buf, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return store.Open(path)
}

var (
	seedsMu sync.Mutex
	seeds   = map[string]func() ([]byte, error){}
)

func seeded(key string, seed func(*store.Store) error) func() ([]byte, error) {
	seedsMu.Lock()
	defer seedsMu.Unlock()
	if fn, ok := seeds[key]; ok {
		return fn
	}
	fn := sync.OnceValues(func() ([]byte, error) {
		dir, err := os.MkdirTemp("", "storetest-seed")
		if err != nil {
			return nil, err
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "seed.db")
		base, err := template()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, base, 0o600); err != nil {
			return nil, err
		}
		st, err := store.Open(path)
		if err != nil {
			return nil, err
		}
		if err := seed(st); err != nil {
			st.Close()
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
	seeds[key] = fn
	return fn
}

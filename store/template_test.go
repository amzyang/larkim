package store

import (
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"
)

// migratedDB is the bytes of a database every migration has run on. It is
// storetest's template again: that package imports this one, so these tests
// cannot import it.
var migratedDB = sync.OnceValues(func() ([]byte, error) {
	dir, err := os.MkdirTemp("", "storetemplate")
	if err != nil {
		return nil, err
	}
	defer os.RemoveAll(dir)
	path := filepath.Join(dir, "template.db")
	s, err := Open(path)
	if err != nil {
		return nil, err
	}
	if _, err := s.db.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		s.Close()
		return nil, err
	}
	if err := s.Close(); err != nil {
		return nil, err
	}
	return os.ReadFile(path)
})

// openAt is Open for tests: a path with no database yet starts as a copy of
// an already-migrated one, so only the migrations a test rewinds run again.
func openAt(t *testing.T, path string) (*Store, error) {
	t.Helper()
	if _, err := os.Stat(path); errors.Is(err, fs.ErrNotExist) {
		body, err := migratedDB()
		if err != nil {
			return nil, err
		}
		if err := os.WriteFile(path, body, 0o600); err != nil {
			return nil, err
		}
	} else if err != nil {
		return nil, err
	}
	return Open(path)
}

func TestOpenAt_AFreshPathHasTheSchemaAMigrationRunBuilds(t *testing.T) {
	ddl := func(s *Store) []string {
		t.Helper()
		rows, err := queryAll(t.Context(), s.db, scanOne[string],
			`SELECT type || ' ' || name || ' ' || coalesce(sql, '') FROM sqlite_master ORDER BY type, name`)
		require.NoError(t, err)
		return rows
	}
	want, err := Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	defer want.Close()
	got, err := openAt(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	defer got.Close()
	require.Equal(t, ddl(want), ddl(got))
}

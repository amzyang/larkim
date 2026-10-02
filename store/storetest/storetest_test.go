package storetest

import (
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func schema(t *testing.T, st *store.Store) []string {
	t.Helper()
	rows, err := st.DB().Query(`SELECT type || ' ' || name || ' ' || coalesce(sql, '') FROM sqlite_master ORDER BY type, name`)
	require.NoError(t, err)
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		require.NoError(t, rows.Scan(&s))
		out = append(out, s)
	}
	require.NoError(t, rows.Err())
	return out
}

func TestOpen_AFreshPathHasTheSchemaAMigrationRunBuilds(t *testing.T) {
	want, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { want.Close() })
	got, err := Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { got.Close() })

	require.Equal(t, schema(t, want), schema(t, got))
	var n int
	require.NoError(t, got.DB().QueryRow(`SELECT count(*) FROM schema_migrations`).Scan(&n))
	require.Positive(t, n, "the copy carries the record of what it ran, so Open does not run it again")
}

func TestOpen_AnExistingDatabaseKeepsWhatItHolds(t *testing.T) {
	path := filepath.Join(t.TempDir(), "t.db")
	st, err := Open(t, path)
	require.NoError(t, err)
	require.NoError(t, st.UpsertChats(t.Context(), []store.Chat{{ChatID: "oc_a", Name: "发布群"}}, 1))
	require.NoError(t, st.Close())

	st, err = Open(t, path)
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	var name string
	require.NoError(t, st.DB().QueryRow(`SELECT name FROM chats WHERE chat_id = 'oc_a'`).Scan(&name))
	require.Equal(t, "发布群", name, "a reopen is not a fresh copy")
}

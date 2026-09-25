package tui

import (
	"bytes"
	"log/slog"
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestNew_DefaultsTheLogger(t *testing.T) {
	m := New(Deps{})
	require.NotNil(t, m.deps.Log, "every log call in the TUI dereferences this")
	m.deps.Log.Warn("discarded")
}

func TestMarkChatRead_ReportsAFailureTheBadgeCannotShow(t *testing.T) {
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	require.NoError(t, st.Close())
	var buf bytes.Buffer
	log := slog.New(slog.NewTextHandler(&buf, nil))

	require.Nil(t, markChatRead(st, log, "oc_quiet")())

	require.Contains(t, buf.String(), "mark chat read")
	require.Contains(t, buf.String(), "oc_quiet")
}

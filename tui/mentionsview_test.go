package tui

import (
	"context"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// mentionModel is a model over a chat where the reader was named once, then
// talked past.
func mentionModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()
	require.NoError(t, st.EnsureChat(ctx, "oc_group", 1))
	_, err = st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_at", ChatID: "oc_group", MsgType: "text", SenderID: "ou_a",
			SenderName: "张三", CreateMs: 100, UpdateMs: 100},
		{MessageID: "om_after", ChatID: "oc_group", MsgType: "text", SenderID: "ou_a",
			SenderName: "张三", CreateMs: 200, UpdateMs: 200},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(ctx, "om_at", "@林岚 看下发布计划",
		`[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`, "", 1))
	require.NoError(t, st.UpdateRendered(ctx, "om_after", "另外一件事", "", "", 1))

	m := New(Deps{Store: st, Self: "ou_me"})
	m.width, m.height = 120, 36
	return m, st
}

func TestOpenMentions_ListsWhatNamedTheReader(t *testing.T) {
	m, _ := mentionModel(t)

	next, cmd := m.openMentions()
	m = next.(Model)
	require.True(t, m.mentions)
	loaded, ok := cmd().(mentionsLoadedMsg)
	require.True(t, ok)
	next, _ = m.update(loaded)
	m = next.(Model)

	require.Len(t, m.searchHits, 1)
	assert.Equal(t, "om_at", m.searchHits[0].msg.MessageID)
}

// Without a self id nothing can be measured, so the panel says so rather than
// opening on an empty list that looks like an answer.
func TestOpenMentions_WithoutASelfIdSaysSo(t *testing.T) {
	m, st := mentionModel(t)
	m.deps.Self = ""
	_ = st

	next, _ := m.openMentions()
	m = next.(Model)

	assert.False(t, m.mentions)
	assert.NotEmpty(t, m.notice)
}

// The list answers a fixed question, so a keystroke that would narrow a search
// does nothing rather than narrowing something the reader cannot see.
func TestOnSearchKey_MentionsListHasNoQueryToType(t *testing.T) {
	m, _ := mentionModel(t)
	next, cmd := m.openMentions()
	m = next.(Model)
	loaded := cmd().(mentionsLoadedMsg)
	next, _ = m.update(loaded)
	m = next.(Model)

	next, _ = m.onSearchKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	m = next.(Model)

	assert.Empty(t, m.searchQuery)
	assert.Len(t, m.searchHits, 1, "the list is unchanged by typing")
}

func TestCloseSearch_LeavesTheMentionsList(t *testing.T) {
	m, _ := mentionModel(t)
	next, _ := m.openMentions()
	m = next.(Model)

	m.closeSearch()

	assert.False(t, m.mentions)
	assert.False(t, m.searching)
}

package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/presence"
	"github.com/stretchr/testify/require"
)

func TestOpenFromBanner_OpensTheChatFromNormalAndInsert(t *testing.T) {
	t.Parallel()
	for name, mode := range map[string]mode{"normal": modeNormal, "insert": modeInsert, "command": modeCommand} {
		t.Run(name, func(t *testing.T) {
			m := pickerModel(t)
			m.mode = mode
			next, cmd := m.update(openFromBannerMsg{chat: "oc_elsewhere"})
			m = next.(Model)
			require.Equal(t, modeNormal, m.mode)
			require.Equal(t, "oc_elsewhere", m.pendingChat)
			require.Equal(t, paneMessages, m.focus)
			require.NotNil(t, cmd)
		})
	}
}

func TestOpenFromBanner_LeavesAnOpenOverlayAlone(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	m.config.open = true
	next, _ := m.update(openFromBannerMsg{chat: "oc_elsewhere"})
	m = next.(Model)
	require.True(t, m.config.open)
	require.Empty(t, m.pendingChat, "a panel half filled in is not torn down from outside")
	require.Contains(t, m.notice, "oc_elsewhere")
}

func TestUpdate_KeepsPresenceInStepWithFocusAndTheOpenChat(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	live := &presence.Live{}
	m.deps.Presence = live
	next, _ := m.Update(tea.FocusMsg{})
	m = next.(Model)
	require.True(t, live.State().Focused)
	require.Equal(t, "oc_team", live.State().Chat)

	next, _ = m.Update(tea.BlurMsg{})
	m = next.(Model)
	require.False(t, live.State().Focused)
}

package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func messageClickY(m Model, line int) int {
	return line - m.msgTop + 1 + msgHeaderHeight + m.msgPad()
}

func firstMessageLine(m Model) int {
	for i, r := range m.msgRows {
		if r.idx >= 0 {
			return i
		}
	}
	return -1
}

func TestContextMenu_RowKeysMatchStatusLineStyle(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	line := firstMessageLine(m)
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: chatsWidth + 4,
		Y: messageClickY(m, line)})
	m = next.(Model)
	var reply string
	for _, it := range m.contextMenu.items {
		if it.hint.Desc == "Reply" {
			reply = renderMenuKeyDesc(it.hint)
			break
		}
	}
	require.NotEmpty(t, reply)
	require.Equal(t, renderMenuKeyDesc(KeyBinding{Keys: "r", Desc: "Reply"}), reply)
	require.Equal(t, ansi.Strip(renderKeyDesc(KeyBinding{Keys: "r", Desc: "reply"})),
		ansi.Strip(packKeyHints([]KeyBinding{{Keys: "r", Desc: "reply"}}, 80, "  ")))
}

func TestContextMenu_RightClickMessageOpensMenu(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	line := firstMessageLine(m)
	require.NotEqual(t, -1, line)
	x := chatsWidth + m.messagesWidth()/2
	y := messageClickY(m, line)

	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: x, Y: y})
	m = next.(Model)

	require.Equal(t, modeContextMenu, m.mode)
	labels := contextMenuLabels(m)
	require.Contains(t, labels, "Reply")
	require.Contains(t, labels, "React...")
	require.Contains(t, labels, "Forward...")
	require.Contains(t, labels, "Copy Text")
	require.Contains(t, labels, "Copy Context")
	require.Contains(t, labels, "Open in Feishu")
}

func TestContextMenu_RightClickChatOpensMenu(t *testing.T) {
	t.Parallel()
	m := cursorModel(t)
	m.focus = paneChats
	m.chatIdx = 1 // first chat after the Unread row

	y := 1 + headerHeight + rowOf(0)*chatRowStride
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: 4, Y: y})
	m = next.(Model)

	require.Equal(t, modeContextMenu, m.mode)
	require.Equal(t, menuChat, m.contextMenu.kind)
	labels := contextMenuLabels(m)
	require.Contains(t, labels, "Open Chat")
	require.Contains(t, labels, "Copy Chat ID")
	require.Contains(t, labels, "Open in Feishu")
}

func TestContextMenu_MouseHoverUpdatesCursor(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	line := firstMessageLine(m)
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: chatsWidth + 4,
		Y: messageClickY(m, line)})
	m = next.(Model)
	f, ok := m.contextMenuFloater()
	require.True(t, ok)
	require.Equal(t, 0, m.contextMenu.cursor)

	next, _ = m.onContextMenuMotion(tea.Mouse{X: f.x + 2, Y: f.y + 2})
	m = next.(Model)
	require.Equal(t, 1, m.contextMenu.cursor, "the row under the pointer is highlighted")

	// Outside the menu leaves the highlight where it was, like Herdr's menu.
	next, _ = m.onContextMenuMotion(tea.Mouse{X: 1, Y: 1})
	m = next.(Model)
	require.Equal(t, 1, m.contextMenu.cursor)
}

func TestContextMenu_KeyboardNavigation(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	line := firstMessageLine(m)
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: chatsWidth + 4,
		Y: messageClickY(m, line)})
	m = next.(Model)
	require.Equal(t, 0, m.contextMenu.cursor)

	m = press(t, m, "j")
	require.Equal(t, 1, m.contextMenu.cursor)
	m = press(t, m, "k")
	require.Equal(t, 0, m.contextMenu.cursor)
}

func TestContextMenu_MnemonicKeys(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	line := firstMessageLine(m)
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: chatsWidth + 4,
		Y: messageClickY(m, line)})
	m = next.(Model)

	next, _ = m.onContextMenuKey(keyMsg("e"))
	m = next.(Model)
	require.Equal(t, modeEmoji, m.mode, "React... opens the picker")
}

func TestContextMenu_ReplyMnemonic(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	line := firstMessageLine(m)
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: chatsWidth + 4,
		Y: messageClickY(m, line)})
	m = next.(Model)

	next, _ = m.onContextMenuKey(keyMsg("r"))
	m = next.(Model)
	require.Equal(t, modeInsert, m.mode)
}

func TestContextMenu_EscDismisses(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	line := firstMessageLine(m)
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: chatsWidth + 4,
		Y: messageClickY(m, line)})
	m = next.(Model)

	m = press(t, m, "esc")
	require.Equal(t, modeNormal, m.mode)
	require.False(t, m.contextMenu.open)
}

func TestContextMenu_OutsideClickDismisses(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	line := firstMessageLine(m)
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: chatsWidth + 4,
		Y: messageClickY(m, line)})
	m = next.(Model)

	next, _ = m.contextMenuClick(tea.Mouse{Button: tea.MouseLeft, X: 1, Y: 1})
	m = next.(Model)
	require.Equal(t, modeNormal, m.mode)
}

func TestContextMenu_ItemClickRunsAction(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	line := firstMessageLine(m)
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: chatsWidth + 4,
		Y: messageClickY(m, line)})
	m = next.(Model)

	f, ok := m.contextMenuFloater()
	require.True(t, ok)
	next, _ = m.contextMenuClick(tea.Mouse{Button: tea.MouseLeft, X: f.x + 2, Y: f.y + 2})
	m = next.(Model)
	require.Equal(t, modeInsert, m.mode)
}

func TestContextMenu_ClampingNearBottomRight(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	m.width, m.height = 80, 24
	line := firstMessageLine(m)
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: m.width - 2,
		Y: messageClickY(m, line)})
	m = next.(Model)

	f, ok := m.contextMenuFloater()
	require.True(t, ok)
	require.LessOrEqual(t, f.x+f.w, m.width)
	require.LessOrEqual(t, f.y+f.h, m.height)
	require.GreaterOrEqual(t, f.x, 0)
	require.GreaterOrEqual(t, f.y, 0)
}

func TestContextMenu_KeyboardOpensAtCursor(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	m.focus = paneMessages
	m.msgIdx = 0

	next, _ := m.openContextMenuAtCursor()
	m = next.(Model)
	require.Equal(t, modeContextMenu, m.mode)
}

func TestContextMenu_SpaceOpensAtCursor(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	m = press(t, m, " ")
	require.Equal(t, modeContextMenu, m.mode)
}

func contextMenuLabels(m Model) []string {
	var out []string
	for _, it := range m.contextMenu.items {
		out = append(out, it.hint.Desc)
	}
	return out
}

func TestForward_LeavesContextMenuAlone(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	line := firstMessageLine(m)
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: chatsWidth + 4,
		Y: messageClickY(m, line)})
	m = next.(Model)

	mm, _ := m.Update(tea.PasteMsg{Content: "paste"})
	m = mm.(Model)
	require.Equal(t, modeContextMenu, m.mode)
	require.Empty(t, m.input.Value())
}

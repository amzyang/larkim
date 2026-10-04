package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/larkcli"
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

func TestContextMenu_IconsUseFullSizeNerdGlyphs(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	line := firstMessageLine(m)
	require.NotEqual(t, -1, line)
	x := chatsWidth + m.messagesWidth()/2
	y := messageClickY(m, line)

	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: x, Y: y})
	m = next.(Model)
	require.NotEmpty(t, m.contextMenu.items)
	for i, it := range m.contextMenu.items {
		require.True(t, strings.HasSuffix(it.icon, enSpace), "item %d (%s)", i, it.hint.Desc)
		require.Equal(t, pickerIconCols, lipgloss.Width(it.icon), "item %d (%s)", i, it.hint.Desc)
	}
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
	require.Contains(t, labels, "Mute")
	require.Contains(t, labels, "Copy Chat ID")
	require.Contains(t, labels, "Open in Feishu")
}

func TestContextMenu_MutedChatShowsUnmute(t *testing.T) {
	t.Parallel()
	m := cursorModel(t)
	m.focus = paneChats
	m.chatIdx = 1
	m.chats[0].Muted = true // chatIdx 1 is the first chat after Unread (oc_0)
	m.rows = newRowsCache() // interleave caches chat fields; rebuild after muting

	y := 1 + headerHeight + rowOf(0)*chatRowStride
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: 4, Y: y})
	m = next.(Model)

	labels := contextMenuLabels(m)
	require.Contains(t, labels, "Unmute")
	require.NotContains(t, labels, "Mute")
	for _, it := range m.contextMenu.items {
		if it.hint.Desc == "Unmute" {
			require.Equal(t, bellUnmuteGlyph, it.icon)
			return
		}
	}
	t.Fatal("Unmute item missing")
}

func TestContextMenu_MuteMnemonic(t *testing.T) {
	t.Parallel()
	m := cursorModel(t)
	m.focus = paneChats
	m.chatIdx = 1

	y := 1 + headerHeight + rowOf(0)*chatRowStride
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: 4, Y: y})
	m = next.(Model)
	for _, it := range m.contextMenu.items {
		if it.hint.Desc == "Mute" {
			require.Equal(t, muteGlyph, it.icon)
			break
		}
	}

	f := larkcli.NewFake()
	f.Chats = []larkcli.RawChat{{ChatID: m.chats[0].ChatID, Name: m.chats[0].Name}}
	m.deps.Client = f

	next, cmd := m.onContextMenuKey(keyMsg("M"))
	m = next.(Model)
	require.Equal(t, modeNormal, m.mode)
	require.Contains(t, m.notice, "muting")
	require.NotNil(t, cmd)
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

func TestContextMenu_RenderedBlockHasNormalBorderAndStructuredColumns(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	line := firstMessageLine(m)
	next, _ := m.onRightClick(tea.Mouse{Button: tea.MouseRight, X: chatsWidth + 4,
		Y: messageClickY(m, line)})
	m = next.(Model)

	f, ok := m.contextMenuFloater()
	require.True(t, ok)
	lines := strings.Split(f.block, "\n")
	require.Greater(t, len(lines), 2)

	topLine := ansi.Strip(lines[0])
	require.True(t, strings.HasPrefix(topLine, "┌"), "top-left corner is ┌: %q", topLine)
	require.True(t, strings.HasSuffix(topLine, "┐"), "top-right corner is ┐: %q", topLine)

	bottomLine := ansi.Strip(lines[len(lines)-1])
	require.True(t, strings.HasPrefix(bottomLine, "└"), "bottom-left corner is └: %q", bottomLine)
	require.True(t, strings.HasSuffix(bottomLine, "┘"), "bottom-right corner is ┘: %q", bottomLine)

	firstRow := ansi.Strip(lines[1])
	require.True(t, strings.HasPrefix(firstRow, "│ "), "row has left padding inside border: %q", firstRow)
	require.True(t, strings.HasSuffix(firstRow, " │"), "row has right padding inside border: %q", firstRow)
	require.Contains(t, firstRow, "Reply")
	require.Contains(t, firstRow, "r")
	require.Contains(t, lines[1], "231;238;252")
	// Selected row background extends edge-to-edge from left border to right border.
	require.Contains(t, lines[1], "│\x1b[m\x1b[38;2;31;35;41;48;2;231;238;252m ")
	require.Contains(t, lines[1], "231;238;252;38;2;31;35;41m \x1b[m\x1b[34m│\x1b[m")

	// Unselected rows style their shortcut key through keyhint (renderKey).
	secondRow := ansi.Strip(lines[2])
	require.Contains(t, secondRow, "Reply in Thread")
	require.Contains(t, lines[2], renderKey("R"))
}

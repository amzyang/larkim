package tui

import (
	"strings"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/amzyang/larkim/store"
)

type contextMenuKind int

const (
	menuMsg contextMenuKind = iota
	menuChat
)

type contextMenuAction func(Model) (tea.Model, tea.Cmd)

type contextMenuItem struct {
	hint   KeyBinding
	icon   string
	action contextMenuAction
}

type contextMenuState struct {
	open   bool
	kind   contextMenuKind
	x, y   int
	items  []contextMenuItem
	cursor int
}

func (m Model) closeContextMenu() Model {
	m.mode = modeNormal
	m.contextMenu = contextMenuState{}
	return m
}

func (m Model) openMessageContextMenu(x, y int) Model {
	sel, ok := m.selected()
	if !ok {
		return m
	}
	m.contextMenu = contextMenuState{
		open: true, kind: menuMsg, x: x, y: y,
		items:  m.messageContextItems(sel),
		cursor: 0,
	}
	if len(m.contextMenu.items) == 0 {
		return m.closeContextMenu()
	}
	m.mode = modeContextMenu
	return m
}

func (m Model) openChatContextMenu(x, y int, row listRow) Model {
	m.contextMenu = contextMenuState{
		open: true, kind: menuChat, x: x, y: y,
		items:  m.chatContextItems(row),
		cursor: 0,
	}
	if len(m.contextMenu.items) == 0 {
		return m.closeContextMenu()
	}
	m.mode = modeContextMenu
	return m
}

func (m Model) openContextMenuAtCursor() (tea.Model, tea.Cmd) {
	if m.mode != modeNormal {
		return m, nil
	}
	if m.focus == paneChats {
		vis := m.visibleRows()
		if m.chatIdx < 0 || m.chatIdx >= len(vis) || vis[m.chatIdx].isFeed() {
			return m, nil
		}
		x, y := m.contextMenuAnchorChats()
		return m.openChatContextMenu(x, y, vis[m.chatIdx]), nil
	}
	if m.focus == paneMessages || m.focus == paneThread && !m.aiOpen() {
		if _, ok := m.selected(); !ok {
			return m, nil
		}
		x, y := m.contextMenuAnchorMessage()
		return m.openMessageContextMenu(x, y), nil
	}
	return m, nil
}

func (m Model) messageContextItems(sel store.Message) []contextMenuItem {
	var items []contextMenuItem
	reply := sel
	items = append(items, contextMenuItem{
		hint: KeyBinding{Keys: "r", Desc: "Reply"}, icon: "↩",
		action: func(m Model) (tea.Model, tea.Cmd) { return m.startInsert(&reply, false) },
	})
	items = append(items, contextMenuItem{
		hint: KeyBinding{Keys: "R", Desc: "Reply in Thread"}, icon: "⤷",
		action: func(m Model) (tea.Model, tea.Cmd) { return m.startInsert(&reply, true) },
	})
	items = append(items, contextMenuItem{
		hint: KeyBinding{Keys: "e", Desc: "React..."}, icon: "☺",
		action: func(m Model) (tea.Model, tea.Cmd) { return m.openPicker() },
	})
	items = append(items, contextMenuItem{
		hint: KeyBinding{Keys: "f", Desc: "Forward..."}, icon: "➤",
		action: func(m Model) (tea.Model, tea.Cmd) { return m.openForward() },
	})
	items = append(items, contextMenuItem{
		hint: KeyBinding{Keys: "y", Desc: "Copy Text"}, icon: "⎘",
		action: func(m Model) (tea.Model, tea.Cmd) {
			out, cmd, _ := m.onYankKey("c")
			return out, cmd
		},
	})
	items = append(items, contextMenuItem{
		hint: KeyBinding{Keys: "Y", Desc: "Copy Context"}, icon: "⎘",
		action: func(m Model) (tea.Model, tea.Cmd) { return m.copySelection() },
	})
	if f, ok := m.containerAtCursor(); ok {
		label := "Open Thread"
		if f.kind == rightForward {
			label = "Open Forward"
		}
		frame := f
		p := m.focus
		items = append(items, contextMenuItem{
			hint: KeyBinding{Keys: "t", Desc: label}, icon: "▸",
			action: func(m Model) (tea.Model, tea.Cmd) { return m.openContainer(p, frame) },
		})
	}
	if m.deps.Todoist != nil {
		items = append(items, contextMenuItem{
			hint: KeyBinding{Keys: "T", Desc: "Add to Todoist"}, icon: "☑",
			action: func(m Model) (tea.Model, tea.Cmd) { return m.todoistTask() },
		})
	}
	if _, bad := m.recallable("recall", "recalled"); bad == "" {
		items = append(items, contextMenuItem{
			hint: KeyBinding{Keys: "D", Desc: "Recall"}, icon: "↶",
			action: func(m Model) (tea.Model, tea.Cmd) { return m.askRecall() },
		})
	}
	if _, bad := m.recallable("re-edit", "re-edited"); bad == "" {
		items = append(items, contextMenuItem{
			hint: KeyBinding{Keys: "E", Desc: "Edit"}, icon: "✎",
			action: func(m Model) (tea.Model, tea.Cmd) { return m.askReEdit() },
		})
	}
	items = append(items, contextMenuItem{
		hint: KeyBinding{Keys: "o", Desc: "Open in Feishu"}, icon: "↗",
		action: func(m Model) (tea.Model, tea.Cmd) { return m.contextOpenTarget() },
	})
	return items
}

func (m Model) chatContextItems(row listRow) []contextMenuItem {
	var items []contextMenuItem
	items = append(items, contextMenuItem{
		hint: KeyBinding{Desc: "Open Chat"}, icon: "▸",
		action: func(m Model) (tea.Model, tea.Cmd) { return m.activate() },
	})
	chatID := row.chatID()
	muted := row.chat.Muted
	want := !muted
	label := "Mute"
	note := "muting…"
	if muted {
		label = "Unmute"
		note = "unmuting…"
	}
	id, apply := chatID, want
	items = append(items, contextMenuItem{
		hint: KeyBinding{Keys: "M", Desc: label}, icon: "\uf1f6",
		action: func(m Model) (tea.Model, tea.Cmd) {
			return m.notify(note, false), setChatMutedCmd(m.deps, id, apply)
		},
	})
	if m.inFeed() {
		chatID := row.chatID()
		items = append(items, contextMenuItem{
			hint: KeyBinding{Keys: "m", Desc: "Mark Read"}, icon: "✓",
			action: func(m Model) (tea.Model, tea.Cmd) { return m.markSectionRead(chatID) },
		})
	}
	items = append(items, contextMenuItem{
		hint: KeyBinding{Keys: "y", Desc: "Copy Chat ID"}, icon: "⎘",
		action: func(m Model) (tea.Model, tea.Cmd) {
			out, cmd, _ := m.onYankKey("y")
			return out, cmd
		},
	})
	items = append(items, contextMenuItem{
		hint: KeyBinding{Keys: "o", Desc: "Open in Feishu"}, icon: "↗",
		action: func(m Model) (tea.Model, tea.Cmd) {
			return m, openInFeishu(m.deps, chatID, "", 0)
		},
	})
	return items
}

func (m Model) contextOpenTarget() (tea.Model, tea.Cmd) {
	switch zs := m.selectedZones(); len(zs) {
	case 0:
		if sel, ok := m.selected(); ok {
			return m, openInFeishu(m.deps, sel.ChatID, sel.MessageID, sel.MessagePosition)
		}
		if m.chatID != "" {
			return m, openInFeishu(m.deps, m.chatID, "", 0)
		}
	case 1:
		return m, openZone(m.deps, zs[0])
	default:
		return m.openTargets(zs)
	}
	return m, nil
}

func (m Model) onRightClick(ms tea.Mouse) (tea.Model, tea.Cmd) {
	if m.confirm.kind != confirmNone {
		return m, nil
	}
	p, row := m.hit(ms.X, ms.Y)
	switch p {
	case paneMessages, paneThread:
		if p == paneThread && m.aiOpen() {
			return m, nil
		}
		rows := m.msgRows
		top := m.msgTop
		if p == paneThread {
			rows, top = m.threadRows, m.threadTop
		}
		line := top + row
		if idx := rowAt(rows, line); idx >= 0 {
			if p == paneThread {
				m.threadIdx = idx
				m.clearDotsAtCursor()
				m.rebuildThread()
			} else {
				m.msgIdx = idx
				m.clearDotsAtCursor()
				m.rebuildMessages()
			}
			m.focus = p
			return m.openMessageContextMenu(ms.X, ms.Y), nil
		}
	case paneChats:
		if row < 0 {
			return m, nil
		}
		vis := m.visibleRows()
		idx := m.chatTop + row
		if idx >= 0 && idx < len(vis) && !vis[idx].isFeed() {
			m.chatIdx = idx
			m.focus = paneChats
			return m.openChatContextMenu(ms.X, ms.Y, vis[idx]), nil
		}
	}
	return m, nil
}

func (m Model) onContextMenuKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	switch s {
	case "esc", "q", "ctrl+c":
		return m.closeContextMenu(), nil
	case "j", "down", "ctrl+n":
		m.contextMenu.cursor = clamp(m.contextMenu.cursor+1, 0, len(m.contextMenu.items)-1)
		return m, nil
	case "k", "up", "ctrl+p":
		m.contextMenu.cursor = clamp(m.contextMenu.cursor-1, 0, len(m.contextMenu.items)-1)
		return m, nil
	case "enter":
		return m.runContextMenuItem(m.contextMenu.cursor)
	}
	for i, it := range m.contextMenu.items {
		if it.hint.Keys != "" && it.hint.Keys == s {
			return m.runContextMenuItem(i)
		}
	}
	return m, nil
}

func (m Model) runContextMenuItem(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.contextMenu.items) {
		return m.closeContextMenu(), nil
	}
	action := m.contextMenu.items[i].action
	m = m.closeContextMenu()
	return action(m)
}

// contextMenuItemAt is the item row under a screen cell, if the cell is on one.
// The border is one line above the first item, the way paneStyle draws it.
func (m Model) contextMenuItemAt(x, y int) (int, bool) {
	f, ok := m.contextMenuFloater()
	if !ok || !f.covers(x, y) {
		return 0, false
	}
	row := y - f.y - 1
	if row < 0 || row >= len(m.contextMenu.items) {
		return 0, false
	}
	return row, true
}

func (m Model) onContextMenuMotion(ms tea.Mouse) (tea.Model, tea.Cmd) {
	if i, ok := m.contextMenuItemAt(ms.X, ms.Y); ok && m.contextMenu.cursor != i {
		m.contextMenu.cursor = i
	}
	return m, nil
}

func (m Model) contextMenuClick(ms tea.Mouse) (tea.Model, tea.Cmd) {
	if i, ok := m.contextMenuItemAt(ms.X, ms.Y); ok {
		return m.runContextMenuItem(i)
	}
	return m.closeContextMenu(), nil
}

func (m Model) walkContextMenu(d int) Model {
	m.contextMenu.cursor = clamp(m.contextMenu.cursor+d, 0, len(m.contextMenu.items)-1)
	return m
}

func (m Model) contextMenuView() menuView {
	items := m.contextMenu.items
	rows := make([]offer, len(items))
	maxName := 0
	for i, it := range items {
		name := renderMenuKeyDesc(it.hint)
		maxName = max(maxName, lipgloss.Width(name))
		rows[i] = offer{icon: offerIcon{text: it.icon}, name: name}
	}
	maxName = min(maxName, 36)
	return menuView{
		rows: rows,
		cols: offerCols{icon: true, name: maxName},
		sel:  m.contextMenu.cursor,
	}
}

func (m Model) contextMenuFloater() (floater, bool) {
	items := m.contextMenu.items
	if len(items) == 0 {
		return floater{}, false
	}
	const (
		iconCol = 2
		gap     = 3
	)
	maxDesc := 0
	maxKey := 0
	for _, it := range items {
		maxDesc = max(maxDesc, lipgloss.Width(it.hint.Desc))
		k := it.hint.Keys
		if k == "" {
			k = "enter"
		}
		maxKey = max(maxKey, lipgloss.Width(macKeys(k)))
	}
	chrome := iconCol + 1 + gap + maxKey + 4 // +4 for borders and padding
	maxDesc = min(maxDesc, max(8, m.width-chrome))
	maxDesc = min(maxDesc, 32)

	lines := make([]string, len(items))
	for i, it := range items {
		lines[i] = m.contextMenuRow(it, maxDesc, maxKey, i == m.contextMenu.cursor)
	}
	block := popupStyle().Render(strings.Join(lines, "\n"))
	f := floater{
		block: block,
		w:     lipgloss.Width(block),
		h:     lipgloss.Height(block),
	}
	f.x = m.contextMenu.x
	f.y = m.contextMenu.y
	if f.x+f.w > m.width {
		f.x = max(0, m.contextMenu.x-f.w)
	}
	if f.y+f.h > m.height-statusHeight {
		f.y = max(0, m.contextMenu.y-f.h)
	}
	f.x = clamp(f.x, 0, max(0, m.width-f.w))
	f.y = clamp(f.y, 0, max(0, m.height-statusHeight-f.h))
	return f, true
}

func (m Model) contextMenuRow(it contextMenuItem, maxDesc, maxKey int, selected bool) string {
	icon := fit(it.icon, 2)
	desc := fit(truncate(it.hint.Desc, maxDesc), maxDesc)
	k := it.hint.Keys
	if k == "" {
		k = "enter"
	}
	key := fit(macKeys(k), maxKey)
	row := " " + icon + " " + desc + "   " + renderKey(key) + " "
	if selected {
		return paint(stChatSel, row)
	}
	return row
}
func (m Model) contextMenuAnchorChats() (int, int) {
	row := m.chatIdx - m.chatTop
	y := 1 + headerHeight + row*chatRowStride + chatRowHeight/2
	x := chatsWidth / 2
	return x, y
}

func (m Model) contextMenuAnchorMessage() (int, int) {
	var rows []msgRow
	top := m.msgTop
	idx := m.msgIdx
	if m.focus == paneThread {
		rows, top, idx = m.threadRows, m.threadTop, m.threadIdx
	} else {
		rows = m.msgRows
	}
	line := -1
	for i, r := range rows {
		if r.idx == idx {
			line = i
			break
		}
	}
	if line < 0 {
		line = top
	}
	visRow := line - top
	y := visRow + 1 + msgHeaderHeight + m.msgPad()
	x := chatsWidth + m.messagesWidth()/2
	if m.focus == paneThread {
		x = m.width - m.rightWidth()/2
		y = visRow + 1 + headerHeight
		if m.aiOpen() {
			y = visRow + 1 + aiHeadLines
		}
	}
	return x, y
}

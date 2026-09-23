// Package tui is the interactive client: chats, messages, threads and a
// composer, driven by vim-style keys and the mouse, fed by the local store.
package tui

import (
	"context"
	"fmt"
	"image/color"
	"os"
	"slices"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/agentctx"
	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
)

type pane int

const (
	paneChats pane = iota
	paneMessages
	paneThread
	paneInput
)

type mode int

const (
	modeNormal mode = iota
	modeInsert
	modeCommand
	modeFilter
	modeVisual
)

// Model is the Bubble Tea model.
type Model struct {
	deps Deps

	width, height int
	focus         pane
	mode          mode
	focused       bool
	showHelp      bool
	th            theme

	chats      []store.Chat
	unread     map[string]int64
	avatars    avatars
	chatFilter string
	chatIdx    int
	chatTop    int

	chatID   string
	msgs     []store.Message
	msgIdx   int
	msgTop   int // first visible line of the message pane
	msgRows  []msgRow
	msgSince int64 // when set, the page starts here instead of at the newest messages

	// visualAnchor is the message the VISUAL selection started from, held by
	// id rather than index because a sync tick can replace the whole list
	// while the selection is open.
	visualAnchor string

	threadOpen bool
	threadID   string
	thread     []store.Message
	threadIdx  int
	threadTop  int
	threadRows []msgRow

	// Search mode: the messages pane lists cross-chat hits.
	searching     bool
	searchQuery   string
	searchResults []store.Message
	pendingSelect string // message to select once its chat loads

	// Assistant pane (replaces the thread pane while open).
	aiOpen  bool
	aiBusy  bool
	aiDraft bool
	aiTitle string
	aiText  string
	aiTop   int
	aiChan  <-chan ai.Chunk

	input   textarea.Model
	cmdline textinput.Model
	replyTo *store.Message
	inThrd  bool
	sending bool

	notice     string
	noticeErr  bool
	syncStatus string
	syncErr    string
	pendingG   bool
	lastClick  time.Time
	lastClickY int
	changes    <-chan []store.Message
	cancel     context.CancelFunc
}

// New builds the model.
func New(d Deps) Model {
	ta := textarea.New()
	ta.Prompt = ""
	ta.ShowLineNumbers = false
	ta.Placeholder = "i to write · Enter sends · Shift+Enter newline"
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"))
	ta.SetHeight(3)
	ti := textinput.New()
	ti.Prompt = ":"
	m := Model{deps: d, input: ta, cmdline: ti, focus: paneChats, focused: true,
		avatars: newAvatars(d.DataDir, os.Getenv)}
	m.setBackground(color.Black, true)
	return m
}

// setBackground derives every shaded style from the terminal background.
func (m *Model) setBackground(bg color.Color, dark bool) {
	m.th = themeFor(bg, dark)
	m.input.SetStyles(composerStyles(dark))
	m.cmdline.SetStyles(textinput.DefaultStyles(dark))
}

// Run starts the program until quit or ctx is done.
func Run(ctx context.Context, d Deps) error {
	ctx, cancel := context.WithCancel(ctx)
	m := New(d)
	m.cancel = cancel
	m.changes = d.Store.Watch(ctx, watchEvery, "")
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	cancel()
	return err
}

func (m Model) Init() tea.Cmd {
	// Asking for the cell size lets avatars be drawn at the exact pixels they
	// will occupy; resampling is what makes small glyphs mushy.
	return tea.Batch(tea.RequestBackgroundColor, tea.Raw(ansi.WindowOp(ansi.RequestCellSizeWinOp)),
		loadChats(m.deps.Store), readSyncStatus(m.deps.Store), pollSyncStatus(m.deps.Store), waitForChange(m.changes))
}

// Update runs the handler, then hands the terminal any avatar the newly
// visible chats need. Going through tea.Raw puts the sequence in the
// renderer's own output buffer, under its lock, so it lands between frames
// and after the alternate screen is up — which is the only screen a virtual
// placement made earlier would not reach.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	if seq := nm.avatarPrepare(); seq != "" {
		return nm, tea.Batch(cmd, tea.Raw(seq))
	}
	return nm, cmd
}

// avatarPrepare asks the renderer for what the chats around the viewport
// need. It reaches a screen beyond each edge so a scroll shows its pictures
// on the frame it arrives, not the one after. The renderer caches through a
// pointer, so the work survives this value copy.
func (m Model) avatarPrepare() string {
	vis := m.visibleChats()
	h := m.chatListHeight()
	top := clamp(m.chatTop-h, 0, len(vis))
	end := clamp(m.chatTop+2*h, 0, len(vis))
	return m.avatars.prepare(vis[top:end], m.unread)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		return m, nil
	case tea.BackgroundColorMsg:
		m.setBackground(msg, msg.IsDark())
		return m, nil
	case uv.CellSizeEvent:
		if k, ok := m.avatars.(*kittyAvatars); ok {
			k.setCellSize(msg.Width, msg.Height)
		}
		return m, nil
	case tea.FocusMsg:
		m.focused = true
		return m, nil
	case tea.BlurMsg:
		m.focused = false
		return m, nil
	case chatsLoadedMsg:
		m.chats, m.unread = msg.chats, msg.unread
		if m.chatID == "" && len(m.chats) > 0 {
			return m, m.openChat(m.chats[0].ChatID)
		}
		m.clampChat()
		return m, nil
	case messagesLoadedMsg:
		if msg.chatID != m.chatID {
			return m, nil
		}
		wasOn := idAt(m.msgs, m.msgIdx)
		m.msgs = msg.msgs
		m.msgIdx = len(m.msgs) - 1
		if m.pendingSelect != "" {
			for i, x := range m.msgs {
				if x.MessageID == m.pendingSelect {
					m.msgIdx = i
				}
			}
			m.pendingSelect = ""
		}
		m.repinSelection(wasOn)
		m.rebuildMessages()
		m.scrollMessagesToSelection()
		return m, markConsumed(m.deps.Store, m.msgs)
	case searchMsg:
		m.searching, m.searchQuery, m.searchResults = true, msg.query, msg.msgs
		m.msgIdx, m.msgTop = 0, 0
		m = m.focusMessages()
		m.rebuildMessages()
		if len(msg.msgs) == 0 {
			return m.notify("no messages match "+msg.query, true), nil
		}
		return m.notify(fmt.Sprintf("%d hits · Enter opens · Esc leaves search", len(msg.msgs)), false), nil
	case aiChunkMsg:
		return m.onAIChunk(msg.chunk)
	case threadLoadedMsg:
		if msg.threadID != m.threadID {
			return m, nil
		}
		wasOn := idAt(m.thread, m.threadIdx)
		m.thread = msg.msgs
		if m.threadIdx >= len(m.thread) {
			m.threadIdx = max(0, len(m.thread)-1)
		}
		m.repinSelection(wasOn)
		m.rebuildThread()
		return m, nil
	case changeMsg:
		return m, m.onChange(msg.msgs)
	case syncStatusMsg:
		m.syncStatus, m.syncErr = msg.status, msg.lastError
		return m, pollSyncStatus(m.deps.Store)
	case sentMsg:
		m.sending = false
		if msg.err != nil {
			return m.notify("send failed: "+msg.err.Error(), true), nil
		}
		m.input.Reset()
		m.replyTo, m.inThrd = nil, false
		return m.notify("sent", false), m.reloadCurrent()
	case errMsg:
		return m.notify(msg.err.Error(), true), nil
	case noticeMsg:
		return m.notify(msg.text, false), nil
	case contextMsg:
		return m.notify(fmt.Sprintf("copied %s · %s · %s", plural(msg.n, "msg", "msgs"), humanBytes(len(msg.text)), msg.chat), false),
			tea.SetClipboard(msg.text)
	case tea.MouseClickMsg:
		return m.onClick(tea.Mouse(msg))
	case tea.MouseWheelMsg:
		return m.onWheel(tea.Mouse(msg))
	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	return m.forward(msg)
}

// forward passes a message to the focused text component.
func (m Model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.mode {
	case modeInsert:
		m.input, cmd = m.input.Update(msg)
	case modeCommand, modeFilter:
		m.cmdline, cmd = m.cmdline.Update(msg)
	}
	return m, cmd
}

func (m Model) notify(text string, isErr bool) Model {
	m.notice, m.noticeErr = text, isErr
	return m
}

func (m *Model) openChat(chatID string) tea.Cmd { return m.openChatFrom(chatID, 0) }

// openChatFrom opens a chat; sinceMs > 0 anchors the message page at that
// time so a search hit older than the newest page can be shown.
func (m *Model) openChatFrom(chatID string, sinceMs int64) tea.Cmd {
	m.searching, m.searchResults, m.searchQuery = false, nil, ""
	m.chatID, m.msgSince = chatID, sinceMs
	m.msgs, m.msgRows, m.msgIdx, m.msgTop = nil, nil, 0, 0
	m.threadOpen, m.threadID, m.thread, m.threadRows = false, "", nil, nil
	m.replyTo, m.inThrd = nil, false
	m.selectCurrentChat()
	return loadMessages(m.deps.Store, chatID, sinceMs)
}

// selectCurrentChat puts the cursor on the open chat within the visible list,
// dropping a filter that would hide it.
func (m *Model) selectCurrentChat() {
	isOpen := func(c store.Chat) bool { return c.ChatID == m.chatID }
	idx := slices.IndexFunc(m.visibleChats(), isOpen)
	if idx < 0 && m.chatFilter != "" {
		m.chatFilter = ""
		idx = slices.IndexFunc(m.visibleChats(), isOpen)
	}
	if idx >= 0 {
		m.chatIdx = idx
	}
	m.clampChat()
}

// openHighlighted loads the chat under the cursor unless it is already open.
func (m *Model) openHighlighted() tea.Cmd {
	vis := m.visibleChats()
	if len(vis) == 0 || vis[m.chatIdx].ChatID == m.chatID {
		return nil
	}
	return m.openChat(vis[m.chatIdx].ChatID)
}

func (m *Model) openThread(threadID string) tea.Cmd {
	m.threadOpen = true
	m.threadID = threadID
	m.threadIdx, m.threadTop = 0, 0
	m.layout()
	return loadThread(m.deps.Store, threadID)
}

func (m Model) reloadCurrent() tea.Cmd {
	cmds := []tea.Cmd{loadChats(m.deps.Store)}
	if m.chatID != "" {
		cmds = append(cmds, loadMessages(m.deps.Store, m.chatID, m.msgSince))
	}
	if m.threadOpen {
		cmds = append(cmds, loadThread(m.deps.Store, m.threadID))
	}
	return tea.Batch(cmds...)
}

// onChange refreshes panes touched by newly synced messages and keeps the
// watch subscription alive.
func (m *Model) onChange(msgs []store.Message) tea.Cmd {
	cmds := []tea.Cmd{waitForChange(m.changes), loadChats(m.deps.Store)}
	touchedChat, touchedThread := false, false
	for _, x := range msgs {
		if x.ChatID == m.chatID {
			touchedChat = true
		}
		if m.threadOpen && x.ThreadID == m.threadID {
			touchedThread = true
		}
	}
	if touchedChat {
		cmds = append(cmds, loadMessages(m.deps.Store, m.chatID, m.msgSince))
	}
	if touchedThread {
		cmds = append(cmds, loadThread(m.deps.Store, m.threadID))
	}
	return tea.Batch(cmds...)
}

// idAt names the message at idx, or "" when the list does not reach it.
func idAt(msgs []store.Message, idx int) string {
	if idx < 0 || idx >= len(msgs) {
		return ""
	}
	return msgs[idx].MessageID
}

// selected returns the message under the cursor in the focused list.
func (m Model) selected() (store.Message, bool) {
	switch m.focus {
	case paneThread:
		if m.threadIdx < len(m.thread) {
			return m.thread[m.threadIdx], true
		}
	default:
		list := m.msgs
		if m.searching {
			list = m.searchResults
		}
		if m.msgIdx >= 0 && m.msgIdx < len(list) {
			return list[m.msgIdx], true
		}
	}
	return store.Message{}, false
}

// rightOpen reports whether the third pane (thread or assistant) is shown.
func (m Model) rightOpen() bool { return m.threadOpen || m.aiOpen }

func (m Model) currentChat() (store.Chat, bool) {
	for _, c := range m.chats {
		if c.ChatID == m.chatID {
			return c, true
		}
	}
	return store.Chat{}, false
}

func (m Model) visibleChats() []store.Chat {
	if m.chatFilter == "" {
		return m.chats
	}
	f := strings.ToLower(m.chatFilter)
	var out []store.Chat
	for _, c := range m.chats {
		if strings.Contains(strings.ToLower(c.Name), f) || strings.Contains(c.ChatID, f) {
			out = append(out, c)
		}
	}
	return out
}

func (m *Model) clampChat() {
	n := len(m.visibleChats())
	if m.chatIdx >= n {
		m.chatIdx = n - 1
	}
	if m.chatIdx < 0 {
		m.chatIdx = 0
	}
	h := m.chatListHeight()
	if h <= 0 {
		return
	}
	if m.chatIdx < m.chatTop {
		m.chatTop = m.chatIdx
	}
	if m.chatIdx >= m.chatTop+h {
		m.chatTop = m.chatIdx - h + 1
	}
}

// --- key handling ---------------------------------------------------------

func (m Model) onKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if s == "ctrl+c" {
		return m, tea.Quit
	}
	switch m.mode {
	case modeInsert:
		return m.onInsertKey(k)
	case modeCommand:
		return m.onCommandKey(k)
	case modeFilter:
		return m.onFilterKey(k)
	case modeVisual:
		return m.onVisualKey(s)
	}
	if m.showHelp {
		m.showHelp = false
		return m, nil
	}
	return m.onNormalKey(s)
}

func (m Model) onInsertKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		return m.focusMessages(), nil
	case "enter":
		return m.submit()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	return m, cmd
}

func (m Model) onCommandKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeNormal
		m.cmdline.Blur()
		m.cmdline.Reset()
		return m, nil
	case "enter":
		line := strings.TrimSpace(m.cmdline.Value())
		m.mode = modeNormal
		m.cmdline.Blur()
		m.cmdline.Reset()
		return m.runCommand(line)
	}
	var cmd tea.Cmd
	m.cmdline, cmd = m.cmdline.Update(k)
	return m, cmd
}

func (m Model) onFilterKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeNormal
		m.cmdline.Blur()
		m.cmdline.Reset()
		m.chatFilter = ""
		m.selectCurrentChat()
		return m, nil
	case "enter":
		m.mode = modeNormal
		m.cmdline.Blur()
		m.clampChat()
		return m, m.openHighlighted()
	}
	var cmd tea.Cmd
	m.cmdline, cmd = m.cmdline.Update(k)
	m.chatFilter = m.cmdline.Value()
	m.chatIdx, m.chatTop = 0, 0
	return m, cmd
}

func (m Model) onNormalKey(s string) (tea.Model, tea.Cmd) {
	if s != "g" {
		defer func() { m.pendingG = false }()
	}
	switch s {
	case "q":
		return m, tea.Quit
	case "?":
		m.showHelp = !m.showHelp
		return m, nil
	case "tab":
		m.focus = m.nextPane(1)
		return m, nil
	case "shift+tab":
		m.focus = m.nextPane(-1)
		return m, nil
	case "h", "left":
		m.focus = m.stepPane(-1)
		return m, nil
	case "l", "right":
		m.focus = m.stepPane(1)
		return m, nil
	case "j", "down":
		return m.move(1)
	case "k", "up":
		return m.move(-1)
	case "ctrl+d", "pgdown":
		return m.move(m.pageStep())
	case "ctrl+u", "pgup":
		return m.move(-m.pageStep())
	case "g":
		if m.pendingG {
			m.pendingG = false
			return m.move(-1 << 30)
		}
		m.pendingG = true
		return m, nil
	case "G", "end":
		return m.move(1 << 30)
	case "enter":
		return m.activate()
	case "i":
		return m.startInsert(nil, false)
	case "r":
		if sel, ok := m.selected(); ok {
			return m.startInsert(&sel, false)
		}
		return m.startInsert(nil, false)
	case "R":
		if sel, ok := m.selected(); ok {
			return m.startInsert(&sel, true)
		}
	case "t":
		return m.toggleThread()
	case "y":
		return m.copySelection()
	case "v":
		return m.startVisual()
	case "o":
		if sel, ok := m.selected(); ok {
			return m, openInFeishu(sel.ChatID, sel.MessagePosition)
		}
		if m.chatID != "" {
			return m, openInFeishu(m.chatID, 0)
		}
	case "/":
		m.mode = modeFilter
		m.focus = paneChats
		m.cmdline.Prompt = "/"
		m.cmdline.SetValue(m.chatFilter)
		return m, m.cmdline.Focus()
	case ":", ";":
		m.mode = modeCommand
		m.cmdline.Prompt = ":"
		m.cmdline.Reset()
		return m, m.cmdline.Focus()
	case "a":
		m.mode = modeCommand
		m.cmdline.Prompt = ":"
		m.cmdline.SetValue("ai ")
		m.cmdline.CursorEnd()
		return m, m.cmdline.Focus()
	case "esc":
		switch {
		case m.aiOpen:
			return m.closeAI(), nil
		case m.searching:
			m.searching, m.searchResults = false, nil
			m.msgIdx = len(m.msgs) - 1
			m.rebuildMessages()
			m.scrollMessagesToSelection()
			return m.notify("", false), nil
		case m.threadOpen && m.focus == paneThread:
			return m.toggleThread()
		case m.chatFilter != "":
			m.chatFilter = ""
			m.selectCurrentChat()
			return m.notify("", false), nil
		}
		m.replyTo, m.inThrd = nil, false
		return m.notify("", false), nil
	}
	return m, nil
}

// --- selection and copying ------------------------------------------------

// onVisualKey handles the VISUAL range selection: only extending it, copying
// it and leaving it. Everything else would have to decide what happens to a
// half-made selection.
func (m Model) onVisualKey(s string) (tea.Model, tea.Cmd) {
	switch s {
	case "j", "down":
		return m.move(1)
	case "k", "up":
		return m.move(-1)
	case "y":
		out, cmd := m.copySelection()
		out.mode = modeNormal
		return out, cmd
	case "esc":
		m.mode = modeNormal
		return m.notify("", false), nil
	}
	return m, nil
}

// startVisual anchors a range selection at the cursor of the focused list.
func (m Model) startVisual() (tea.Model, tea.Cmd) {
	if m.searching {
		return m.notify("press Enter to open the hit; v selects inside a chat", true), nil
	}
	selectable := m.focus == paneMessages && len(m.msgs) > 0 ||
		m.focus == paneThread && !m.aiOpen && len(m.thread) > 0
	if !selectable {
		return m.notify("v selects in the messages or thread pane", true), nil
	}
	m.visualAnchor = m.focusedList()[m.cursor(m.focus)].MessageID
	m.mode = modeVisual
	return m.notify("j/k extend · y copies · Esc cancels", false), nil
}

// cursor is the selected row of a message list pane.
func (m Model) cursor(p pane) int {
	if p == paneThread {
		return m.threadIdx
	}
	return m.msgIdx
}

// focusedList is the message slice the selection keys act on.
func (m Model) focusedList() []store.Message {
	if m.focus == paneThread {
		return m.thread
	}
	return m.msgs
}

// selectionRange is the inclusive index span y acts on: the cursor alone in
// NORMAL, the whole anchored range in VISUAL. An anchor whose message is no
// longer listed degrades to the cursor, so the span always indexes the list.
func (m Model) selectionRange() (lo, hi int) {
	idx := m.cursor(m.focus)
	anchor := indexOfID(m.focusedList(), m.visualAnchor)
	if m.mode != modeVisual || anchor < 0 {
		return idx, idx
	}
	return min(anchor, idx), max(anchor, idx)
}

// repinSelection keeps an open VISUAL range on the same two messages after a
// reload replaced the list. A selection that lost either end ends, rather
// than being silently redrawn around whatever now sits at those indices.
func (m *Model) repinSelection(cursorID string) {
	if m.mode != modeVisual {
		return
	}
	list := m.focusedList()
	idx := indexOfID(list, cursorID)
	if idx < 0 || indexOfID(list, m.visualAnchor) < 0 {
		m.mode = modeNormal
		return
	}
	if m.focus == paneThread {
		m.threadIdx = idx
		return
	}
	m.msgIdx = idx
}

func indexOfID(msgs []store.Message, id string) int {
	if id == "" {
		return -1
	}
	return slices.IndexFunc(msgs, func(m store.Message) bool { return m.MessageID == id })
}

// inSelection reports whether row idx of pane p is painted as selected.
func (m Model) inSelection(p pane, idx int) bool {
	if m.mode != modeVisual || m.focus != p {
		return idx == m.cursor(p)
	}
	lo, hi := m.selectionRange()
	return idx >= lo && idx <= hi
}

// copySelection puts the agent context of the current focus on the clipboard:
// the cursor's message or the VISUAL range from a message list, the last day
// of the highlighted chat from the chats pane.
func (m Model) copySelection() (Model, tea.Cmd) {
	switch {
	case m.searching:
		return m.notify("press Enter to open the hit; y copies from inside a chat", true), nil
	case m.focus == paneChats:
		vis := m.visibleChats()
		if len(vis) == 0 {
			return m.notify("nothing to copy", true), nil
		}
		spec := copySpec{chatID: vis[m.chatIdx].ChatID, rng: agentctx.Range{Since: chatsCopyAge, Limit: chatsCopyLimit}}
		return m.notify("copying…", false), copyContext(m.deps, spec)
	case m.focus == paneMessages, m.focus == paneThread && !m.aiOpen:
		list := m.msgs
		if m.focus == paneThread {
			list = m.thread
		}
		if len(list) == 0 {
			return m.notify("nothing to copy", true), nil
		}
		lo, hi := m.selectionRange()
		return m.notify("copying…", false), copyContext(m.deps, copySpec{chatID: m.chatID, msgs: list[lo : hi+1]})
	}
	return m.notify("nothing to copy here", true), nil
}

// runCopy is :copy, which always covers the open chat, whatever has focus.
func (m Model) runCopy(arg string) (tea.Model, tea.Cmd) {
	if m.chatID == "" {
		return m.notify("nothing to copy", true), nil
	}
	r, err := agentctx.ParseRange(arg)
	if err != nil {
		return m.notify(err.Error(), true), nil
	}
	return m.notify("copying…", false), copyContext(m.deps, copySpec{chatID: m.chatID, rng: r})
}

// visiblePanes lists the list panes on screen from left to right; the
// composer is entered with i, r or Enter rather than by cycling focus.
func (m Model) visiblePanes() []pane {
	order := []pane{paneChats}
	if !m.foldRight() {
		order = append(order, paneMessages)
	}
	if m.rightOpen() {
		order = append(order, paneThread)
	}
	return order
}

// nextPane cycles focus through the visible panes.
func (m Model) nextPane(dir int) pane {
	order := m.visiblePanes()
	i := slices.Index(order, m.focus)
	if i < 0 {
		return paneChats
	}
	return order[(i+dir+len(order))%len(order)]
}

// stepPane moves focus one visible pane sideways, stopping at the edges.
func (m Model) stepPane(dir int) pane {
	order := m.visiblePanes()
	i := slices.Index(order, m.focus)
	if i < 0 {
		return paneChats
	}
	return order[clamp(i+dir, 0, len(order)-1)]
}

func (m Model) pageStep() int {
	switch m.focus {
	case paneChats:
		return max(1, m.chatListHeight()/2)
	default:
		return max(1, m.bodyHeight()/4)
	}
}

// move shifts the selection in the focused list by n rows.
func (m Model) move(n int) (tea.Model, tea.Cmd) {
	switch m.focus {
	case paneChats:
		m.chatIdx = clamp(m.chatIdx+n, 0, len(m.visibleChats())-1)
		m.clampChat()
		return m, m.openHighlighted()
	case paneMessages:
		count := len(m.msgs)
		if m.searching {
			count = len(m.searchResults)
		}
		m.msgIdx = clamp(m.msgIdx+n, 0, count-1)
		m.scrollMessagesToSelection()
	case paneThread:
		if m.aiOpen {
			m.aiTop = clamp(m.aiTop+n, 0, max(0, len(m.aiLines())-m.listHeight()))
			return m, nil
		}
		m.threadIdx = clamp(m.threadIdx+n, 0, len(m.thread)-1)
		m.scrollThreadToSelection()
	}
	return m, nil
}

func (m Model) activate() (tea.Model, tea.Cmd) {
	switch m.focus {
	case paneChats:
		return m.focusMessages(), nil
	case paneMessages:
		if m.searching {
			if sel, ok := m.selected(); ok {
				m.pendingSelect = sel.MessageID
				m.notice = ""
				return m, m.openChatFrom(sel.ChatID, sel.CreateMs)
			}
			return m, nil
		}
		sel, ok := m.selected()
		if !ok {
			return m, nil
		}
		if sel.ThreadID != "" {
			return m.toggleThread()
		}
		return m.startInsert(&sel, false)
	case paneThread:
		if sel, ok := m.selected(); ok {
			return m.startInsert(&sel, true)
		}
	}
	return m, nil
}

func (m Model) toggleThread() (tea.Model, tea.Cmd) {
	if m.threadOpen {
		m.threadOpen = false
		m.threadID = ""
		if m.focus == paneThread {
			m.focus = paneMessages
		}
		m.layout()
		return m, nil
	}
	sel, ok := m.selected()
	if !ok || sel.ThreadID == "" {
		return m.notify("selected message has no thread", true), nil
	}
	m = m.closeAI()
	m.focus = paneThread
	return m, m.openThread(sel.ThreadID)
}

func (m Model) startInsert(replyTo *store.Message, inThread bool) (tea.Model, tea.Cmd) {
	if m.chatID == "" {
		return m.notify("open a chat first", true), nil
	}
	m.mode = modeInsert
	m.focus = paneInput
	m.replyTo, m.inThrd = replyTo, inThread
	return m, m.input.Focus()
}

func (m Model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" || m.sending {
		return m, nil
	}
	m.sending = true
	if m.replyTo != nil {
		return m.notify("replying…", false), replyText(m.deps, m.replyTo.MessageID, text, m.inThrd)
	}
	return m.notify("sending…", false), sendText(m.deps, larkcli.Target{ChatID: m.chatID}, text)
}

func (m Model) runCommand(line string) (tea.Model, tea.Cmd) {
	if line == "" {
		return m, nil
	}
	name, rest, _ := strings.Cut(line, " ")
	rest = strings.TrimSpace(rest)
	switch name {
	case "q", "quit":
		return m, tea.Quit
	case "goto", "chat":
		want := store.FoldName(rest)
		for _, c := range m.chats {
			if c.ChatID == rest || store.FoldName(c.Name) == want {
				m = m.focusMessages()
				return m, m.openChat(c.ChatID)
			}
		}
		return m.notify("no chat "+rest, true), nil
	case "send":
		ref, text, ok := strings.Cut(rest, " ")
		if !ok || strings.TrimSpace(text) == "" {
			return m.notify("usage: :send <chat|oc_|ou_> <text>", true), nil
		}
		if strings.HasPrefix(ref, "ou_") {
			m.sending = true
			return m, sendText(m.deps, larkcli.Target{UserID: ref}, strings.TrimSpace(text))
		}
		for _, c := range m.chats {
			if c.ChatID == ref || c.Name == ref {
				m.sending = true
				return m, sendText(m.deps, larkcli.Target{ChatID: c.ChatID}, strings.TrimSpace(text))
			}
		}
		return m.notify("unknown chat "+ref, true), nil
	case "search", "s":
		if len(strings.TrimSpace(rest)) == 0 {
			return m.notify("usage: :search <text>", true), nil
		}
		return m.notify("searching…", false), searchMessages(m.deps.Store, rest)
	case "copy":
		return m.runCopy(rest)
	case "ai":
		return m.startAI(rest)
	case "sync":
		if m.deps.Syncer == nil {
			return m.notify("sync is handled by the daemon", false), nil
		}
		s := m.deps.Syncer
		return m.notify("syncing…", false), func() tea.Msg {
			if _, err := s.Tick(context.Background()); err != nil {
				return errMsg{err}
			}
			return noticeMsg{"synced"}
		}
	}
	return m.notify("unknown command :"+name, true), nil
}

// --- assistant ------------------------------------------------------------

func (m Model) startAI(input string) (tea.Model, tea.Cmd) {
	if m.deps.AI == nil {
		return m.notify("assistant off: set ANTHROPIC_API_KEY (config ai.api_key_env)", true), nil
	}
	if m.chatID == "" || len(m.msgs) == 0 {
		return m.notify("open a chat with messages first", true), nil
	}
	if m.aiBusy {
		return m.notify("assistant is still answering", true), nil
	}
	prompt, draft := ai.Prompt(input)
	name := m.chatID
	if c, ok := m.currentChat(); ok && c.Name != "" {
		name = c.Name
	}
	n := m.deps.AIContext
	if n <= 0 || n > len(m.msgs) {
		n = len(m.msgs)
	}
	transcript := ai.Transcript(name, m.msgs[len(m.msgs)-n:], m.deps.Self)
	m.threadOpen = false
	m.aiOpen, m.aiBusy, m.aiDraft = true, true, draft
	m.aiTitle = strings.TrimSpace(input)
	if m.aiTitle == "" {
		m.aiTitle = "summary"
	}
	m.aiText, m.aiTop = "", 0
	m.focus = paneThread
	m.layout()
	m.aiChan = m.deps.AI.Stream(context.Background(), transcript, prompt)
	return m.notify("asking Claude…", false), waitForAI(m.aiChan)
}

func (m Model) onAIChunk(c ai.Chunk) (tea.Model, tea.Cmd) {
	if c.Err != nil {
		m.aiBusy = false
		m.aiText += "\n\n" + c.Err.Error()
		return m.notify("assistant failed", true), nil
	}
	m.aiText += c.Text
	if !c.Done {
		// Follow the stream unless the user scrolled up.
		bottom := max(0, len(m.aiLines())-m.listHeight())
		if m.aiTop >= bottom-3 {
			m.aiTop = bottom
		}
		return m, waitForAI(m.aiChan)
	}
	m.aiBusy = false
	if m.aiDraft && strings.TrimSpace(m.aiText) != "" {
		m.input.SetValue(strings.TrimSpace(m.aiText))
		return m.notify("draft placed in the composer: i to edit, Enter to send", false), nil
	}
	return m.notify("", false), nil
}

// focusMessages moves focus to the messages pane; on a folded layout the
// right pane stood in for it, so that pane closes first.
func (m Model) focusMessages() Model {
	if m.foldRight() {
		m.threadOpen, m.threadID, m.thread, m.threadRows = false, "", nil, nil
		m.aiOpen, m.aiChan = false, nil
		m.layout()
	}
	m.focus = paneMessages
	return m
}

func (m Model) closeAI() Model {
	m.aiOpen, m.aiChan = false, nil
	if m.focus == paneThread {
		m.focus = paneMessages
	}
	m.layout()
	return m
}

// --- mouse ----------------------------------------------------------------

func (m Model) onClick(ms tea.Mouse) (tea.Model, tea.Cmd) {
	if ms.Button != tea.MouseLeft {
		return m, nil
	}
	double := time.Since(m.lastClick) < 400*time.Millisecond && m.lastClickY == ms.Y
	m.lastClick, m.lastClickY = time.Now(), ms.Y
	p, row := m.hit(ms.X, ms.Y)
	if p == paneChats || p == paneMessages || p == paneThread {
		m.mode = modeNormal
		m.input.Blur()
		m.focus = p
	}
	switch p {
	case paneChats:
		vis := m.visibleChats()
		idx := m.chatTop + row
		if idx >= 0 && idx < len(vis) {
			m.chatIdx = idx
			if vis[idx].ChatID != m.chatID || double {
				m = m.focusMessages()
				return m, m.openChat(vis[idx].ChatID)
			}
		}
	case paneMessages:
		if idx := rowAt(m.msgRows, m.msgTop+row); idx >= 0 {
			m.msgIdx = idx
			if double {
				return m.activate()
			}
		}
	case paneThread:
		if idx := rowAt(m.threadRows, m.threadTop+row); idx >= 0 {
			m.threadIdx = idx
			if double {
				return m.activate()
			}
		}
	case paneInput:
		return m.startInsert(m.replyTo, m.inThrd)
	}
	return m, nil
}

func (m Model) onWheel(ms tea.Mouse) (tea.Model, tea.Cmd) {
	p, _ := m.hit(ms.X, ms.Y)
	step := 3
	if ms.Button == tea.MouseWheelUp {
		step = -3
	} else if ms.Button != tea.MouseWheelDown {
		return m, nil
	}
	switch p {
	case paneChats:
		vis := m.visibleChats()
		m.chatTop = clamp(m.chatTop+step, 0, max(0, len(vis)-m.chatListHeight()))
	case paneMessages:
		m.msgTop = clamp(m.msgTop+step, 0, max(0, len(m.msgRows)-m.listHeight()))
	case paneThread:
		m.threadTop = clamp(m.threadTop+step, 0, max(0, len(m.threadRows)-m.listHeight()))
	}
	return m, nil
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}

// Help text shown by ?.
const helpText = `NORMAL      j/k move · gg/G ends · Ctrl+d/u page · Tab/Shift+Tab focus · h/l panes
            Enter open chat / thread / reply · i write · r reply · R reply in thread · t thread
            y copy agent context · v select a range · o open in Feishu · / filter chats · :/; command · q quit
VISUAL      v starts in the messages or thread pane · j/k extend · y copies and leaves · Esc cancels
INSERT      Enter send · Shift+Enter newline · Esc back
COMMAND     :copy <200|7d|all> · :goto <chat> · :send <chat|ou_> <text> · :search <text> · :sync · :q
ASSISTANT   a or :ai [summary | draft <how> | todo | <question>] · answer streams in the right pane · Esc closes
MOUSE       click focuses and selects · double-click opens · wheel scrolls`

func fmtStatus(m Model) string {
	sync := "daemon"
	if m.deps.Embedded {
		sync = "embedded"
	}
	st := m.syncStatus
	if st == "" {
		st = "never_synced"
	}
	label := modeLabel(m.mode)
	if m.mode == modeVisual {
		lo, hi := m.selectionRange()
		label += " " + plural(hi-lo+1, "msg", "msgs")
	}
	out := fmt.Sprintf("%s · sync:%s/%s", label, sync, st)
	if st == "needs_login" {
		out += " → lark-cli auth login"
	}
	return out
}

func modeLabel(md mode) string {
	switch md {
	case modeInsert:
		return "INSERT"
	case modeCommand:
		return "COMMAND"
	case modeFilter:
		return "FILTER"
	case modeVisual:
		return "VISUAL"
	}
	return "NORMAL"
}

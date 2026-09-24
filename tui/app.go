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
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	uv "github.com/charmbracelet/ultraviolet"
	"github.com/charmbracelet/x/ansi"
	"github.com/google/uuid"
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
	modeEmoji
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
	// dark is which way the terminal's background leans, kept beside the
	// theme shaded from it because a code block picks its palette by name
	// rather than by shading.
	dark bool

	chats  []store.Chat
	unread map[string]int64
	// readRefreshed is when each chat last had its read status re-asked, so
	// revisiting a chat does not spend a call every time.
	readRefreshed map[string]time.Time
	// picker is the emoji chooser, open only in modeEmoji.
	picker picker
	// emoji is the searchable emoji set the picker offers, prepared once
	// because the terms never change while the program runs.
	emoji *emoji.Index
	// reactionRefreshed is when each chat last had its reactions re-asked.
	// Nothing else keeps them current: Feishu does not move a message's
	// update_time when somebody reacts, so the rendering pass never revisits.
	reactionRefreshed map[string]time.Time
	avatars           avatars
	pics              *pictures // message images; nil on a terminal without graphics
	chatFilter        string
	chatIdx           int
	chatTop           int

	chatID string
	// msgs is what the pane indexes: the rows the store returned, held in
	// msgsBase, followed by the sends still in flight. thread pairs the same
	// way with threadBase.
	msgs     []store.Message
	msgsBase []store.Message
	meta     msgMeta // detail for whatever the messages pane shows
	msgIdx   int
	msgTop   int // first visible line of the message pane
	msgRows  []msgRow
	msgSince int64 // when set, the page starts here instead of at the newest messages
	// dots holds the messages the unread marker is drawn against, gathered as
	// pages arrive and dropped on the way into another chat. Opening a chat
	// takes its messages as read at once, so a marker read straight off the
	// store would go out under the reader's eyes; this visit keeps showing
	// what was waiting when it began, and the next one starts clean.
	dots map[string]bool
	// pendingChat is a chat whose page has been asked for but not arrived. The
	// panes stay on the chat they are showing until it does, so a cursor
	// running down the list never leaves a blank behind it.
	pendingChat  string
	pendingSince int64
	// cursorMovedAt is when the chat cursor last moved, which is what tells a
	// sweep down the list from a single keypress.
	cursorMovedAt time.Time

	// visualAnchor is the message the VISUAL selection started from, held by
	// id rather than index because a sync tick can replace the whole list
	// while the selection is open.
	visualAnchor string

	threadOpen bool
	threadID   string
	thread     []store.Message
	threadBase []store.Message
	threadMeta msgMeta
	threadIdx  int
	threadTop  int
	threadRows []msgRow

	// Search mode: the messages pane lists cross-chat hits.
	searching     bool
	searchQuery   string
	searchResults []store.Message
	searchMeta    msgMeta
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
	// outbox holds the messages the user submitted that the store does not
	// carry yet, and selfName names their sender until it does.
	outbox   []outboxItem
	selfName string

	notice     string
	noticeErr  bool
	syncStatus string
	syncErr    string
	pendingG   bool
	pendingY   bool
	lastClick  time.Time
	lastClickY int
	revs       <-chan int64
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
	if d.OpenURL == nil {
		d.OpenURL = openURL
	}
	m := Model{deps: d, input: ta, cmdline: ti, focus: paneChats, focused: true,
		readRefreshed:     map[string]time.Time{},
		reactionRefreshed: map[string]time.Time{},
		emoji:             emoji.NewReactionIndex(),
		avatars:           newAvatars(d.DataDir, os.Getenv), pics: newPictures(d.DataDir, os.Getenv)}
	m.emoji.LoadRecent(d.DataDir)
	m.setBackground(color.Black, true)
	return m
}

// setBackground derives every shaded style from the terminal background.
func (m *Model) setBackground(bg color.Color, dark bool) {
	m.dark = dark
	m.th = themeFor(bg, dark)
	m.input.SetStyles(composerStyles(dark))
	m.cmdline.SetStyles(textinput.DefaultStyles(dark))
}

// Run starts the program until quit or ctx is done.
func Run(ctx context.Context, d Deps) error {
	ctx, cancel := context.WithCancel(ctx)
	m := New(d)
	m.cancel = cancel
	m.revs = d.Store.WatchRev(ctx, watchEvery, d.Nudge)
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	cancel()
	return err
}

func (m Model) Init() tea.Cmd {
	// Asking for the cell size lets avatars be drawn at the exact pixels they
	// will occupy; resampling is what makes small glyphs mushy.
	return tea.Batch(tea.RequestBackgroundColor, tea.Raw(ansi.WindowOp(ansi.RequestCellSizeWinOp)),
		loadChats(m.deps.Store), readSyncStatus(m.deps.Store), pollSyncStatus(m.deps.Store), waitForRev(m.revs),
		loadSelfName(m.deps.Store, m.deps.Self))
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
	if _, raw := msg.(tea.RawMsg); raw {
		// The sequence the last round handed the terminal arrives back here.
		// Preparing again in answer to it would let Update feed itself.
		return nm, cmd
	}
	if seq := nm.avatarPrepare() + nm.picturePrepare(); seq != "" {
		return nm, tea.Batch(cmd, tea.Raw(seq))
	}
	return nm, cmd
}

// picturePrepare hands the terminal the images the message panes are about to
// draw, reaching a screen beyond each edge so a scroll shows a picture on the
// frame it arrives rather than the one after. It never asks for more than the
// id space holds: a wider reach evicts a picture it is about to draw, and the
// pass that redraws that one evicts another, so the panes never settle.
func (m Model) picturePrepare() string {
	h := m.listHeight()
	var pics []picture
	seen := map[string]bool{}
	take := func(p picture) {
		if p.cols == 0 || seen[p.key()] || len(pics) >= picIDs {
			return
		}
		seen[p.key()] = true
		pics = append(pics, p)
	}
	collect := func(rows []msgRow, lo, hi int) {
		for i := clamp(lo, 0, len(rows)); i < clamp(hi, 0, len(rows)) && len(pics) < picIDs; i++ {
			take(rows[i].pic)
			for _, s := range rows[i].segs {
				take(s.pic)
			}
		}
	}
	// The picker is what the reader is looking at while it is open, so the
	// emoji it offers are claimed before anything behind it.
	if m.mode == modeEmoji {
		for _, hit := range m.pickerVisible() {
			_, pic := m.pickerIcon(hit.Emoji)
			take(pic)
		}
	}
	// The chat list is claimed next. Its reactions are a handful of icons
	// that many rows draw from the same ids, and unlike the message bands
	// below it reaches for nothing off screen.
	vis := m.visibleChats()
	pcs := m.chatPics()
	for i := m.chatTop; i < len(vis) && i < m.chatTop+m.chatListHeight(); i++ {
		for _, s := range chatChips(vis[i], pcs) {
			take(s.pic)
		}
	}
	// Then the rows on screen, so a pane crowded with pictures spends what is
	// left on what the reader is looking at.
	for _, band := range [][2]int{{0, h}, {h, 2 * h}, {-h, 0}} {
		collect(m.msgRows, m.msgTop+band[0], m.msgTop+band[1])
		collect(m.threadRows, m.threadTop+band[0], m.threadTop+band[1])
	}
	return m.pics.prepare(pics)
}

// avatarPrepare asks the renderer for what the chats around the viewport
// need, reaching beyond each edge so a scroll shows its pictures on the frame
// it arrives rather than the one after. The window never outgrows the id
// space: a wider one evicts a picture it is about to draw, and the pass that
// redraws that one evicts another, so the list never stops being redrawn. The
// renderer caches through a pointer, so the work survives this value copy.
func (m Model) avatarPrepare() string {
	vis := m.visibleChats()
	n := min(len(vis), 3*m.chatListHeight(), kittyIDs)
	// A viewport taller than the id space cannot be covered whole, and the
	// margin then has to go to the rows above the fold rather than below it.
	h := min(m.chatListHeight(), n)
	top := clamp(m.chatTop-(n-h)/2, 0, len(vis)-n)
	return m.avatars.prepare(vis[top:top+n], m.unread)
}

func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		// The terminal reports its cell size only when asked, and changing the
		// font is what resizes the grid without resizing the window. Asking
		// again here is the only way a stale cell size — every picture scaled
		// for the wrong grid — gets corrected.
		return m, tea.Raw(ansi.WindowOp(ansi.RequestCellSizeWinOp))
	case tea.BackgroundColorMsg:
		m.setBackground(msg, msg.IsDark())
		return m, nil
	case uv.CellSizeEvent:
		// Both renderers are told before either answer is read: a resize asks
		// for the size again, and the usual answer repeats what they hold.
		moved := false
		if k, ok := m.avatars.(*kittyAvatars); ok {
			moved = k.setCellSize(msg.Width, msg.Height)
		}
		if m.pics.setCellSize(msg.Width, msg.Height) {
			moved = true
		}
		if !moved {
			return m, nil
		}
		m.layout()
		return m, nil
	case tea.FocusMsg:
		m.focused = true
		return m, nil
	case tea.BlurMsg:
		m.focused = false
		return m, nil
	case chatsLoadedMsg:
		was := chatIDAt(m.visibleChats(), m.chatIdx)
		m.chats, m.unread = msg.chats, msg.unread
		if m.openingChat() == "" && len(m.chats) > 0 {
			return m, m.openChat(m.chats[0].ChatID)
		}
		m.repinChat(was)
		return m, nil
	case messagesLoadedMsg:
		// A reload is not a cursor move. The reader's place is held by message
		// id and the screen row it sits on, because a page that slid a message
		// off its head renumbers every row under the cursor; only a cursor
		// already on the newest message follows the one that arrives.
		wasOn, atEnd := idAt(m.msgs, m.msgIdx), m.msgIdx >= len(m.msgs)-1
		row := firstRow(m.msgRows, m.msgIdx) - m.msgTop
		switch msg.chatID {
		case m.pendingChat:
			m.enterChat()
			wasOn, atEnd = "", true
		case m.chatID:
		default:
			return m, nil
		}
		m.markDots(msg.msgs)
		m.msgsBase, m.meta = msg.msgs, msg.meta
		m.applyOutbox()
		if m.searching {
			// The cursor and viewport index m.searchResults, not m.msgs.
			return m, m.takeRead(msg.chatID, msg.msgs)
		}
		m.msgIdx = len(m.msgs) - 1
		if i := indexOfID(m.msgs, wasOn); !atEnd && i >= 0 {
			m.msgIdx = i
		}
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
		if !atEnd {
			m.msgTop = clamp(firstRow(m.msgRows, m.msgIdx)-row, 0, max(0, len(m.msgRows)-m.listHeight()))
		}
		m.scrollMessagesToSelection()
		return m, m.takeRead(msg.chatID, msg.msgs)
	case searchMsg:
		m.markDots(msg.msgs)
		m.searching, m.searchQuery, m.searchResults, m.searchMeta = true, msg.query, msg.msgs, msg.meta
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
		m.markDots(msg.msgs)
		m.threadBase, m.threadMeta = msg.msgs, msg.meta
		m.applyOutbox()
		if m.threadIdx >= len(m.thread) {
			m.threadIdx = max(0, len(m.thread)-1)
		}
		m.repinSelection(wasOn)
		m.rebuildThread()
		return m, nil
	case revMsg:
		return m, tea.Batch(waitForRev(m.revs), m.reloadCurrent())
	case syncStatusMsg:
		m.syncStatus, m.syncErr = msg.status, msg.lastError
		return m, pollSyncStatus(m.deps.Store)
	case sentMsg:
		it := m.outboxAt(msg.localID)
		if msg.err != nil {
			note := "send failed: " + msg.err.Error()
			if it != nil {
				it.state = outFailed
				m.refreshPanes()
				note += " · . retries · x discards"
			}
			return m.notify(note, true), nil
		}
		if it != nil {
			it.state, it.messageID = outSent, msg.messageID
		}
		return m, ingestCmd(m.deps, msg.localID, msg.messageID)
	case ingestedMsg:
		// The message is on Feishu either way. A fetch that failed only means
		// the row is not local yet, so the bubble stays up and the next sync
		// to bring the row in is what retires it.
		if msg.err != nil {
			return m.notify("sent · not stored locally yet: "+msg.err.Error(), true), m.reloadCurrent()
		}
		m.dropOutbox(msg.localID)
		m.refreshPanes()
		return m.notify("sent", false), m.reloadCurrent()
	case selfNameMsg:
		m.selfName = msg.name
		m.refreshPanes()
		return m, nil
	case errMsg:
		return m.notify(msg.err.Error(), true), nil
	case noticeMsg:
		return m.notify(msg.text, false), nil
	case chatRestMsg:
		if !m.claimChatOpen(msg.chatID) {
			return m, nil
		}
		return m, m.openChat(msg.chatID)
	case readRefreshDueMsg:
		if !m.claimReadRefresh(msg.chatID, time.Now()) {
			return m, nil
		}
		return m, refreshReadStatus(m.deps, msg.chatID)
	case reactionRefreshDueMsg:
		if !m.claimReactionRefresh(msg.chatID, time.Now()) {
			return m, nil
		}
		return m, refreshReactions(m.deps, msg.chatID)
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
// time so a search hit older than the newest page can be shown. The panes only
// change over once the page arrives — see enterChat.
func (m *Model) openChatFrom(chatID string, sinceMs int64) tea.Cmd {
	m.pendingChat, m.pendingSince = chatID, sinceMs
	m.selectCurrentChat()
	return tea.Batch(loadMessages(m.deps.Store, chatID, sinceMs),
		scheduleReadRefresh(chatID), scheduleReactionRefresh(chatID))
}

// enterChat swaps the panes over to the chat whose page has just arrived.
// Everything the previous chat owned — its thread, the message being quoted,
// the search it was reached from — goes at that same moment, so no pane is
// ever left showing one chat under another's name.
func (m *Model) enterChat() {
	m.searching, m.searchResults, m.searchQuery = false, nil, ""
	m.dots = nil
	m.chatID, m.msgSince = m.pendingChat, m.pendingSince
	m.pendingChat, m.pendingSince = "", 0
	m.msgs, m.msgsBase, m.msgRows, m.msgIdx, m.msgTop = nil, nil, nil, 0, 0
	m.threadOpen, m.threadID, m.thread, m.threadBase, m.threadRows = false, "", nil, nil, nil
	m.replyTo, m.inThrd = nil, false
	m.selectCurrentChat()
}

// markDots keeps the unread markers of a page that has just arrived. A page
// is read against the state it was queried with, before takeRead clears it,
// so a marker lit on one page stays lit through the reload that clearing
// causes.
func (m *Model) markDots(msgs []store.Message) {
	for _, x := range msgs {
		if isUnread(x) {
			if m.dots == nil {
				m.dots = map[string]bool{}
			}
			m.dots[x.MessageID] = true
		}
	}
}

// takeRead records that the reader has had a chat's page in front of them:
// consumed for the CLI cursor, read for the badge. Both run on every page,
// reloads included, so the chat being watched does not light up again as
// messages land in it.
//
// The Feishu client keeps a red dot of its own, which only the client itself
// can drop. A page that arrived with something waiting therefore also walks
// the client onto the chat, so reading here settles both badges rather than
// leaving one lit for a later trip to Feishu. That covers the chat under the
// reader's eyes as well as the one just opened: a message landing in it
// relights the client's dot, and the page it arrives on drops it again.
func (m Model) takeRead(chatID string, msgs []store.Message) tea.Cmd {
	cmds := []tea.Cmd{markConsumed(m.deps.Store, msgs), markChatRead(m.deps.Store, chatID)}
	if unreadWaiting(msgs) {
		cmds = append(cmds, clearFeishuBadge(m.deps, chatID))
	}
	return tea.Batch(cmds...)
}

// selectCurrentChat puts the cursor on the chat being opened within the
// visible list, dropping a filter that would hide it.
func (m *Model) selectCurrentChat() {
	idx := indexOfChat(m.visibleChats(), m.openingChat())
	if idx < 0 && m.chatFilter != "" {
		m.chatFilter = ""
		idx = indexOfChat(m.visibleChats(), m.openingChat())
	}
	if idx >= 0 {
		m.chatIdx = idx
	}
	m.clampChat()
}

// repinChat puts the cursor back on was — the chat it was pointing at before
// the reload — and keeps that chat on the screen row it was already on. Chats
// sort unread first and then by newest message, so one read status flipping
// carries a chat tens of rows; an index kept across the swap would follow the
// row rather than the chat, and following the chat without moving the viewport
// with it throws the cursor to the edge of the pane and reads as the list
// jumping. A filter being typed owns the cursor instead.
func (m *Model) repinChat(was string) {
	vis := m.visibleChats()
	if idx := indexOfChat(vis, was); idx >= 0 && m.mode != modeFilter {
		row := clamp(m.chatIdx-m.chatTop, 0, max(0, m.chatListHeight()-1))
		m.chatIdx = idx
		m.chatTop = idx - row
	}
	// renderChats indexes the list straight from chatTop, so it has to land
	// inside a list that may also have grown shorter.
	m.chatTop = clamp(m.chatTop, 0, max(0, len(vis)-m.chatListHeight()))
	m.clampChat()
}

func indexOfChat(chats []store.Chat, chatID string) int {
	if chatID == "" {
		return -1
	}
	return slices.IndexFunc(chats, func(c store.Chat) bool { return c.ChatID == chatID })
}

// openHighlighted loads the chat under the cursor unless it is already open.
func (m *Model) openHighlighted() tea.Cmd {
	chatID := m.highlightedChat()
	if chatID == "" {
		return nil
	}
	return m.openChat(chatID)
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
	case modeEmoji:
		return m.onEmojiKey(k)
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
	case "ctrl+r":
		m.setReply(nil, false)
		return m, nil
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
	if m.pendingY {
		out, cmd, _ := m.onYankKey(s)
		return out, cmd
	}
	// A prefix lives only as long as the key that follows it: g then anything
	// but g is a cancelled g, not a gg still waiting for its second half.
	armedG := m.pendingG
	m.pendingG = false
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
		if armedG {
			return m.move(-1 << 30)
		}
		m.pendingG = true
		return m, nil
	case "G", "end":
		return m.move(1 << 30)
	case "enter":
		return m.activate()
	case "e":
		return m.openPicker()
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
	case ".":
		return m.retryFailed()
	case "x":
		return m.discardFailed()
	case "t":
		return m.toggleThread()
	case "Y":
		return m.copySelection()
	case "y":
		return m.startYank()
	case "v":
		return m.startVisual()
	case "o":
		if sel, ok := m.selected(); ok {
			// While a call is running, opening it in Feishu means joining
			// it; once it has ended, the message is all there is to open.
			if link := joinLink(sel); link != "" {
				return m, joinMeeting(m.deps, link)
			}
			return m, openInFeishu(m.deps, sel.ChatID, sel.MessagePosition)
		}
		if m.chatID != "" {
			return m, openInFeishu(m.deps, m.chatID, 0)
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
		m.setReply(nil, false)
		return m.notify("", false), nil
	}
	return m, nil
}

// --- selection and copying ------------------------------------------------

// onVisualKey handles the VISUAL range selection: only extending it, copying
// it and leaving it. Everything else would have to decide what happens to a
// half-made selection.
func (m Model) onVisualKey(s string) (tea.Model, tea.Cmd) {
	if m.pendingY {
		out, cmd, copied := m.onYankKey(s)
		if copied {
			out.mode = modeNormal
		}
		return out, cmd
	}
	switch s {
	case "j", "down":
		return m.move(1)
	case "k", "up":
		return m.move(-1)
	case "Y":
		out, cmd := m.copySelection()
		out.mode = modeNormal
		return out, cmd
	case "y":
		return m.startYank()
	case "esc":
		m.mode = modeNormal
		return m.notify("", false), nil
	}
	return m, nil
}

// startYank arms the y prefix. The candidates go on the status bar because
// three of them is more than a key worth guessing at.
func (m Model) startYank() (tea.Model, tea.Cmd) {
	m.pendingY = true
	return m.notify("y… y id · r json · c content", false), nil
}

// onYankKey resolves the second key of the y family and reports whether it
// copied. A key outside the set only drops the prefix: the clipboard and the
// cursor stay put, and in VISUAL so does the selection.
func (m Model) onYankKey(s string) (Model, tea.Cmd, bool) {
	m.pendingY = false
	var k yankKind
	switch s {
	case "y":
		k = yankID
	case "r":
		k = yankRaw
	case "c":
		k = yankContent
	default:
		return m.notify("", false), nil, false
	}
	src, here := m.yankSources()
	switch {
	case !here:
		return m.notify("nothing to copy here", true), nil, false
	case len(src) == 0:
		return m.notify("nothing to copy", true), nil, false
	}
	text, notice := yank(k, src)
	if text == "" {
		return m.notify("nothing to copy", true), nil, false
	}
	return m.notify(notice, false), tea.SetClipboard(text), true
}

// yankSources flattens what the y family acts on: the highlighted chat, or
// the cursor's message and, in VISUAL, the whole range. Search hits are as
// good a source as a chat's own messages, since every field the family
// copies sits on the row itself. The second result is false where there is
// no object under the cursor at all.
func (m Model) yankSources() ([]yankSource, bool) {
	switch {
	case m.focus == paneChats:
		vis := m.visibleChats()
		if len(vis) == 0 {
			return nil, true
		}
		return []yankSource{chatYank(vis[m.chatIdx])}, true
	case m.focus == paneMessages, m.focus == paneThread && !m.aiOpen:
		list := m.focusedList()
		if m.searching && m.focus == paneMessages {
			list = m.searchResults
		}
		if len(list) == 0 {
			return nil, true
		}
		lo, hi := m.selectionRange()
		src := make([]yankSource, 0, hi-lo+1)
		for _, msg := range list[lo : hi+1] {
			src = append(src, messageYank(msg))
		}
		return src, true
	}
	return nil, false
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
	return m.notify("j/k extend · Y copies · y id/json/content · Esc cancels", false), nil
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
		return m.notify("press Enter to open the hit; Y copies from inside a chat", true), nil
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
		return m, m.moveToChat(time.Now())
	case paneMessages:
		count := len(m.msgs)
		if m.searching {
			count = len(m.searchResults)
		}
		m.msgIdx = clamp(m.msgIdx+n, 0, count-1)
		m.rebuildMessages()
		m.scrollMessagesToSelection()
	case paneThread:
		if m.aiOpen {
			m.aiTop = clamp(m.aiTop+n, 0, max(0, len(m.aiLines())-m.listHeight()))
			return m, nil
		}
		m.threadIdx = clamp(m.threadIdx+n, 0, len(m.thread)-1)
		m.rebuildThread()
		m.scrollThreadToSelection()
	}
	return m, nil
}

func (m Model) activate() (tea.Model, tea.Cmd) {
	switch m.focus {
	case paneChats:
		// Enter says the user means this chat, so it skips the rest delay the
		// cursor keys go through.
		cmd := m.openHighlighted()
		return m.focusMessages(), cmd
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

// setReply points the composer at the message a draft answers, nil for none.
// The quote takes a row of its own, so every pane above it is re-laid out.
func (m *Model) setReply(replyTo *store.Message, inThread bool) {
	m.replyTo, m.inThrd = replyTo, inThread
	m.layout()
}

func (m Model) startInsert(replyTo *store.Message, inThread bool) (tea.Model, tea.Cmd) {
	if m.chatID == "" {
		return m.notify("open a chat first", true), nil
	}
	m.mode = modeInsert
	m.focus = paneInput
	m.setReply(replyTo, inThread)
	return m, m.input.Focus()
}

// submit puts the draft on screen before it puts it on the wire: the bubble
// is what says the message went, so nothing holds a second one back either.
func (m Model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	it := outboxItem{localID: uuid.NewString(), chatID: m.chatID, text: text, createMs: time.Now().UnixMilli()}
	if m.replyTo != nil {
		it.chatID, it.replyTo, it.inThread = m.replyTo.ChatID, m.replyTo.MessageID, m.inThrd
		if m.inThrd {
			it.threadID = m.replyTo.ThreadID
		}
	}
	cmd := m.sendItem(it)
	m.enqueue(it)
	m.input.Reset()
	m.setReply(nil, false)
	m.refreshPanes()
	if i := indexOfID(m.msgs, it.localID); i >= 0 {
		m.msgIdx = i
		m.rebuildMessages()
		m.scrollMessagesToSelection()
	}
	return m.notify("", false), cmd
}

// sendItem is the command that puts one outbox item on the wire.
func (m Model) sendItem(it outboxItem) tea.Cmd {
	if it.replyTo != "" {
		return replyText(m.deps, it.localID, it.replyTo, it.text, it.inThread)
	}
	return sendText(m.deps, it.localID, larkcli.Target{ChatID: it.chatID}, it.text)
}

// refreshPanes redraws both message lists after the outbox changed, keeping
// the cursor inside a list a retired bubble just shortened.
func (m *Model) refreshPanes() {
	m.applyOutbox()
	m.msgIdx = max(0, min(m.msgIdx, len(m.msgs)-1))
	m.threadIdx = max(0, min(m.threadIdx, len(m.thread)-1))
	m.rebuildMessages()
	m.rebuildThread()
}

// retryFailed sends a failed message again under its original idempotency
// key, so one that in fact reached Feishu is not delivered twice.
func (m Model) retryFailed() (tea.Model, tea.Cmd) {
	it := m.failedUnderCursor()
	if it == nil {
		return m.notify(". sends a failed message again", true), nil
	}
	it.state = outSending
	m.refreshPanes()
	return m.notify("", false), m.sendItem(*it)
}

func (m Model) discardFailed() (tea.Model, tea.Cmd) {
	it := m.failedUnderCursor()
	if it == nil {
		return m.notify("x drops a failed message", true), nil
	}
	m.dropOutbox(it.localID)
	m.refreshPanes()
	return m.notify("", false), nil
}

// failedUnderCursor is the failed send the cursor sits on, nil when the
// cursor sits on anything else.
func (m *Model) failedUnderCursor() *outboxItem {
	sel, ok := m.selected()
	if !ok {
		return nil
	}
	it := m.outboxAt(sel.MessageID)
	if it == nil || it.state != outFailed {
		return nil
	}
	return it
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
		// No outbox item: :send can fire at a chat no pane is showing, and
		// at a user whose chat id only Feishu knows.
		if strings.HasPrefix(ref, "ou_") {
			return m.notify("sending…", false), sendText(m.deps, "", larkcli.Target{UserID: ref}, strings.TrimSpace(text))
		}
		for _, c := range m.chats {
			if c.ChatID == ref || c.Name == ref {
				return m.notify("sending…", false), sendText(m.deps, "", larkcli.Target{ChatID: c.ChatID}, strings.TrimSpace(text))
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
	case "react":
		return m.runReact(rest)
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

// runReact puts one emoji on the selected message by name, for a reader who
// already knows which one they want. The argument is matched the way the
// picker matches, so :react zan and :react 赞 reach the same emoji.
func (m Model) runReact(arg string) (tea.Model, tea.Cmd) {
	if arg == "" {
		return m.notify("usage: :react <emoji>", true), nil
	}
	if m.deps.Syncer == nil {
		return m.notify("reacting needs the sync lock; the daemon holds it", true), nil
	}
	x, ok := m.selected()
	if !ok || x.Deleted {
		return m.notify("select a message to react to", true), nil
	}
	if m.outboxAt(x.MessageID) != nil {
		return m.notify("that message has not reached Feishu yet", true), nil
	}
	hits := m.emoji.Search(arg)
	if len(hits) == 0 {
		return m.notify("no emoji matches "+arg, true), nil
	}
	e := hits[0].Emoji
	on := true
	for _, c := range emoji.Summary(x.ReactionsJSON, m.deps.Self) {
		if c.Mine && emoji.Fold(c.Key) == emoji.Fold(e.Key) {
			on = false
		}
	}
	m.emoji.Use(e.Key)
	_ = m.emoji.SaveRecent(m.deps.DataDir)
	return m, react(m.deps, x.MessageID, e.Key, on)
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
		m.threadOpen, m.threadID, m.thread, m.threadBase, m.threadRows = false, "", nil, nil, nil
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
		// A button the row draws answers first: the click asked for the
		// button, not for the message it sits on. Pane content starts one
		// column inside the border the pane is drawn with.
		if z, ok := zoneAt(m.msgRows, m.msgTop+row, ms.X-chatsWidth-1); ok {
			return m, joinMeeting(m.deps, z.url)
		}
		if idx := rowAt(m.msgRows, m.msgTop+row); idx >= 0 {
			m.msgIdx = idx
			m.rebuildMessages()
			if double {
				return m.activate()
			}
		}
	case paneThread:
		if z, ok := zoneAt(m.threadRows, m.threadTop+row, ms.X-(m.width-m.rightWidth())-1); ok {
			return m, joinMeeting(m.deps, z.url)
		}
		if idx := rowAt(m.threadRows, m.threadTop+row); idx >= 0 {
			m.threadIdx = idx
			m.rebuildThread()
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
		m.msgIdx = cursorInWindow(m.msgRows, m.msgIdx, m.msgTop, m.listHeight())
	case paneThread:
		m.threadTop = clamp(m.threadTop+step, 0, max(0, len(m.threadRows)-m.listHeight()))
		m.threadIdx = cursorInWindow(m.threadRows, m.threadIdx, m.threadTop, m.listHeight())
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
            Y copy agent context · yy id · yr raw json · yc content · v select a range
            o open in Feishu, or join the call the selected message invites to
            e react to the selected message
            / filter chats · :/; command · q quit
            . send a failed message again · x drop it
VISUAL      v starts in the messages or thread pane · j/k extend · Y or yy/yr/yc copy and leave · Esc cancels
INSERT      Enter send · Shift+Enter newline · ^r drop the quote · Esc back
EMOJI       e opens it · type to filter (Chinese, pinyin or initials) · ↑↓ move · Enter react · Esc cancel
            an emoji already yours is marked ✓, and choosing it takes the reaction back
COMMAND     :copy <200|7d|all> · :goto <chat> · :react <emoji> · :send <chat|ou_> <text> · :search <text> · :sync · :q
ASSISTANT   a or :ai [summary | draft <how> | todo | <question>] · answer streams in the right pane · Esc closes
MOUSE       click focuses and selects · double-click opens · click Join to enter a call
            wheel scrolls`

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
	stamp := ""
	if x, ok := m.selected(); ok {
		// No row spells a time out any more: a merged message has no sender
		// line of its own to put one on, so the cursor answers for all alike.
		stamp = msgTime(x.CreateMs, time.Now()) + " · "
	}
	out := fmt.Sprintf("%s · %ssync:%s/%s", label, stamp, sync, st)
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
	case modeEmoji:
		return "REACT"
	}
	return "NORMAL"
}

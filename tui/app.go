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
	"github.com/amzyang/larkim/sync"
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
	modeSearch
	modeTarget
	modeForward
)

// Model is the Bubble Tea model.
type Model struct {
	deps Deps

	width, height int
	focus         pane
	mode          mode
	focused       bool
	th            theme
	// dark is which way the terminal's background leans, kept beside the
	// theme shaded from it because a code block picks its palette by name
	// rather than by shading.
	dark bool

	chats  []store.Chat
	unread map[string]int64
	// drafts is every chat's unsent composer state, for the chat list's own
	// marker. The open chat's draft lives in the composer, not here, so this
	// map is one refresh behind for that one row — see draftForRow.
	drafts map[string]store.Draft
	// chatPollInFlight holds the open chat's poll to one call at a time, so a
	// slow one costs a skipped beat instead of a queue.
	chatPollInFlight bool
	// chatPollPausedUntil stands the poll down after the gateway refuses it.
	chatPollPausedUntil time.Time
	// chatPollBeat counts polls so the refreshes riding along take turns.
	chatPollBeat uint
	// targets is the chooser over what the selected message leads to, open
	// only in modeTarget.
	targets targets
	// picker is the emoji chooser, open only in modeEmoji.
	picker picker
	// pum is the completion popup over the writing area, open only while
	// something is being written.
	pum pum
	// confirm is the action waiting on y or n, if any. A zero value leaves
	// every key on its ordinary path.
	confirm confirmation
	// fwd is the forward chooser, open only in modeForward.
	fwd forwarder
	// help is the ? overlay, which takes every key while it is open.
	help helpPanel
	// infoOpen draws the open chat's own card in the right-hand pane; info is
	// its roster and infoTop the row it is scrolled to.
	infoOpen bool
	info     []store.Contact
	infoTop  int
	// contacts is everyone larkim knows, for the forward chooser: a colleague
	// with no chat yet is still somewhere a message can go.
	contacts []store.Contact
	// emoji is the searchable emoji set the reaction picker offers, and
	// emojiWrite the wider one a draft may carry. Both are prepared once,
	// because the terms never change while the program runs.
	emoji      *emoji.Index
	emojiWrite *emoji.Index
	// chatIx spells the chat list for the filter, so `/` reaches a Chinese
	// name through its pinyin.
	chatIx     *chatIndex
	avatars    avatars
	pics       *pictures // message images; nil on a terminal without graphics
	chatFilter string
	// filterPin is what a cancelled filter puts back. Opening the filter takes
	// the chat pane and typing into it renumbers the list, so a session that
	// ends in nothing has to restore what it borrowed rather than leave the
	// reader somewhere they never asked to be.
	filterPin filterPin
	chatIdx   int
	chatTop   int

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
	// readAt is the readKey the last takeRead answered. Holding it, rather
	// than diffing against the model this update began with, is what keeps a
	// view that leaves the tail and comes back from firing a second applink
	// for the same message while the first one's write is still in flight.
	readAt string
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
	searching   bool
	searchQuery string
	// mentions says the panel is showing what named the reader rather than a
	// search. The rows are the same kind, so only the rule over them, the
	// title and the absence of a query tell the two apart.
	mentions bool
	// searchHits are the rows the panel can open — messages, chats and
	// people share one cursor, which is msgIdx. It is the two halves below
	// in the order they are drawn, rebuilt whenever either of them lands.
	searchHits []searchHit
	// searchLocal is what the store answered, searchRemote what Feishu added
	// to it. They arrive separately and neither waits for the other.
	searchLocal  []searchHit
	searchRemote []searchHit
	// searchCancel drops the remote search in flight; searchBusy says one is
	// still out, which the panel's title line reports.
	searchCancel context.CancelFunc
	searchBusy   bool
	searchMeta   msgMeta
	// searchGen is which query the results in hand belong to. A result that
	// names an older one is dropped rather than shown under a newer query.
	searchGen     int
	pendingSelect string // message to select once its chat loads

	// Assistant pane (replaces the thread pane while open).
	aiOpen  bool
	aiBusy  bool
	aiDraft bool
	aiTitle string
	aiText  string
	aiTop   int
	aiChan  <-chan ai.Chunk

	// roster is who is in the open chat: what @ completes against, and what
	// turns the names it inserted into tags on the way out.
	roster []store.Contact
	// picked remembers which person each inserted name stood for, so two
	// colleagues sharing a display name resolve to the one chosen. Keyed by
	// name rather than by offset, because the draft goes on being edited.
	picked map[string]string

	input   textarea.Model
	cmdline textinput.Model
	replyTo *store.Message
	inThrd  bool
	// draft is what the composer holds, resolved on every keystroke so the
	// badge names the message type — and any refused path — before Enter
	// commits to it. files resolves the paths a draft names.
	draft    draftPlan
	draftErr error
	files    draftFiles
	// previewOpen shows the draft as the message list will draw it. It is on
	// by default because the preview only appears for a post or an image,
	// which is exactly when the draft does not read as what it will become.
	previewOpen bool
	previewRows []msgRow
	// previewTop is the first row of the preview on screen. The band is
	// capped at previewMaxRows, so a post renders past it and the wheel is
	// the only way to the rest.
	previewTop int
	// outbox holds the messages the user submitted that the store does not
	// carry yet, and selfName names their sender until it does.
	outbox   []outboxItem
	selfName string
	// reacts holds the reaction presses Feishu has not answered yet, laid
	// over the stored summaries so a press draws before it is sent. reactSeq
	// names each one, so a press that failed takes back itself.
	reacts   []reactPending
	reactSeq int64

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
	ta.KeyMap.InsertNewline = key.NewBinding(key.WithKeys("shift+enter", "alt+enter", "ctrl+j"))
	ta.SetHeight(3)
	// The terminal's own cursor carries the mode, so bubbles must stop drawing
	// its reverse-video stand-in: a virtual cursor has no shape to change.
	ta.SetVirtualCursor(false)
	ti := textinput.New()
	ti.Prompt = ":"
	ti.SetVirtualCursor(false)
	if d.Env == nil {
		d.Env = os.Getenv
	}
	if d.Clipboard == nil {
		d.Clipboard = readClipboard
	}
	if d.Fetch == nil {
		d.Fetch = sync.HTTPFetch
	}
	if d.Log == nil {
		d.Log = discardLog
	}
	// After the logger: the opener is the one hand-over to a subprocess the
	// TUI makes, and it logs the argv it builds.
	if d.OpenURL == nil {
		log := d.Log
		d.OpenURL = func(targets []string, background bool) error {
			return openURL(log, targets, background)
		}
	}
	prunePasted(d.DataDir, time.Now())
	m := Model{deps: d, input: ta, cmdline: ti, focus: paneChats, focused: true, previewOpen: true,
		emoji:      emoji.NewReactionIndex(),
		emojiWrite: emoji.NewComposerIndex(),
		chatIx:     newChatIndex(),
		avatars:    newAvatars(d.DataDir, d.Env), pics: newPictures(d.DataDir, d.Env),
		files: osDraftFiles()}
	m.emoji.LoadRecent(d.DataDir)
	m.emojiWrite.LoadRecent(d.DataDir)
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
	cmds := tea.Batch(tea.RequestBackgroundColor, tea.Raw(ansi.WindowOp(ansi.RequestCellSizeWinOp)),
		loadChats(m.deps), readSyncStatus(m.deps.Store), pollSyncStatus(m.deps.Store), waitForRev(m.revs),
		loadSelfName(m.deps.Store, m.deps.Self))
	return tea.Batch(cmds, m.chatPollCmd())
}

// Update runs the handler, takes as read whatever the handler left in front of
// the reader, then hands the terminal any avatar the newly visible chats need.
// Going through tea.Raw puts the sequence in the
// renderer's own output buffer, under its lock, so it lands between frames
// and after the alternate screen is up — which is the only screen a virtual
// placement made earlier would not reach.
func (m Model) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	next, cmd := m.update(msg)
	nm, ok := next.(Model)
	if !ok {
		return next, cmd
	}
	// Reading is settled here rather than where a page arrives, because
	// arriving is only one of the ways a page comes to be in front of the
	// reader: scrolling back to the tail, closing the help overlay, widening
	// the terminal out of the fold and returning to the window all reach it
	// too, and each of them is an ordinary message through this seam.
	if k := nm.readKey(atTail(nm.msgRows, nm.msgTop, nm.msgListHeight())); k != "" && k != nm.readAt {
		nm.readAt = k
		cmd = tea.Batch(cmd, nm.takeRead(nm.chatID, nm.msgsBase))
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
			take(rows[i].lead.pic)
			for _, s := range rows[i].segs {
				take(s.pic)
			}
		}
	}
	// The draft being previewed is claimed first, on the same rule: a picture
	// the reader is looking at outranks one behind it. Without this the
	// preview reserves cells the terminal was never handed.
	collect(m.previewRows, m.previewTop, m.previewTop+m.composerRows().preview)
	// The picker is what the reader is looking at while it is open, so the
	// emoji it offers are claimed before anything behind it.
	if m.mode == modeEmoji {
		for _, hit := range m.pickerVisible() {
			_, pic := m.pickerIcon(hit.Emoji)
			take(pic)
		}
	}
	// The completion popup stands over the writing area on the same rule.
	for _, hit := range m.pumVisible() {
		if hit.emoji.Key != "" {
			_, pic := m.pickerIcon(hit.emoji)
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
		// Beats taken while blurred polled nothing, so the chat on screen may
		// be a minute stale; catch it up now rather than on the next beat.
		if id := m.claimChatPoll(time.Now()); id != "" {
			m.chatPollInFlight = true
			return m, pollChat(m.deps, id, m.openThreadID())
		}
		return m, nil
	case tea.BlurMsg:
		// The reader has gone to another window; what is in the composer has
		// to survive the trip, since larkim may be quit from over there.
		m.focused = false
		return m, m.saveComposer()
	case chatsLoadedMsg:
		vis := m.visibleChats()
		wasCursor, wasTop := chatIDAt(vis, m.chatIdx), chatIDAt(vis, m.chatTop)
		m.chats, m.unread, m.drafts = msg.chats, msg.unread, msg.drafts
		if m.openingChat() == "" && len(m.chats) > 0 {
			return m, m.openChat(m.chats[0].ChatID)
		}
		m.repinChat(wasCursor, wasTop)
		return m, nil
	case messagesLoadedMsg:
		// A reload is not a cursor move. Cursor and viewport are held apart,
		// each by the message it was on, because a page that slid a message off
		// its head renumbers every row: the cursor keeps its own message, and
		// the view keeps the message on its top row. Only a view already
		// showing the last line follows the message that arrives, and only a
		// cursor already on the newest message goes with it.
		wasOn, atEnd := idAt(m.msgs, m.msgIdx), m.msgIdx >= len(m.msgs)-1
		anchor := topAnchor(m.msgRows, m.msgs, m.msgTop)
		tailed := atTail(m.msgRows, m.msgTop, m.msgListHeight())
		entering := msg.chatID == m.pendingChat
		switch msg.chatID {
		case m.pendingChat:
			m.enterChat()
			wasOn, atEnd, tailed = "", true, true
		case m.chatID:
		default:
			return m, nil
		}
		// A message landing where the reader is looking is read as it lands, so
		// it wears no marker. Everything else has something to say about what
		// was missed: the page that opens a chat, and whatever arrived while
		// the pane was not in front of the reader — scrolled up its history,
		// behind the search panel, or with the terminal in the background.
		// The viewport asked about is the one the page landed in, before
		// holdTop moves it.
		if entering || !m.pageShown(tailed) {
			m.markDots(msg.msgs)
		}
		var infoCmd tea.Cmd
		m.msgsBase, m.meta = msg.msgs, msg.meta
		m.applyOutbox()
		// Only the page that opens the chat carries its draft back into the
		// composer; a reload would stomp whatever is being typed.
		if entering {
			m.restoreDraft(msg.draft, m.msgs)
		}
		if msg.chatID == m.chatID {
			m.roster = msg.roster
		}
		// The info pane is about the chat being read, so it follows the reader
		// rather than closing behind them.
		if entering && m.infoOpen {
			m.info, m.infoTop = nil, 0
			infoCmd = loadInfo(m.deps, msg.chatID)
		}
		if m.searching {
			// The cursor and viewport index m.searchResults, not m.msgs.
			return m, infoCmd
		}
		m.msgIdx = len(m.msgs) - 1
		if i := indexOfID(m.msgs, wasOn); !atEnd && i >= 0 {
			m.msgIdx = i
		}
		jumped := m.pendingSelect != ""
		if jumped {
			for i, x := range m.msgs {
				if x.MessageID == m.pendingSelect {
					m.msgIdx = i
				}
			}
			m.pendingSelect = ""
		}
		m.repinSelection(wasOn)
		// The block a visit opens on stands in front of the reader before
		// they touch a key, and so does the hit a jump lands on: both are
		// read as they are drawn, the way moving the cursor onto a block
		// reads it. A reload is not: the cursor following a message that
		// landed while the reader was away must not erase what it says.
		if entering || jumped {
			m.clearBlockDots(m.msgs, m.msgIdx, m.msgStyleFor(m.messagesWidth()-2, m.meta))
		}
		m.rebuildMessages()
		if jumped {
			// Landing on a search hit is a cursor move, so this one scrolls.
			m.scrollMessagesToSelection()
		} else {
			m.msgTop = holdTop(m.msgRows, m.msgs, anchor, tailed, m.msgTop, m.msgListHeight())
		}
		return m, infoCmd
	case searchRestMsg:
		if !m.claimSearch(msg.gen) {
			return m, nil
		}
		return m, localSearch(m.deps, m.chats, m.searchQuery, msg.gen)
	case remoteRestMsg:
		if !m.claimSearch(msg.gen) {
			return m, nil
		}
		return m, m.startRemote()
	case searchMsg:
		if !m.claimSearch(msg.gen) {
			return m, nil
		}
		for _, h := range msg.hits {
			if h.kind == hitMessage {
				m.markDots([]store.Message{h.msg})
			}
		}
		m.searchLocal, m.searchMeta = msg.hits, msg.meta
		m.rebuildHits()
		m.msgIdx, m.msgTop = 0, 0
		m.rebuildMessages()
		return m, nil
	case infoLoadedMsg:
		if msg.chatID == m.chatID {
			m.info, m.infoTop = msg.members, 0
		}
		return m, nil
	case contactsLoadedMsg:
		m.contacts = msg.people
		// The chooser opened on the chats alone; the people it now reaches go
		// in behind them, which leaves a cursor already on a chat where it was.
		if m.mode == modeForward {
			m.fwd.hits = m.fwdSearch(m.fwd.input.Value())
			m.fwd.move(0, m.fwdRows())
		}
		return m, nil
	case forwardedMsg:
		if msg.err != nil {
			return m.notify("forward: "+msg.err.Error(), true), nil
		}
		return m.notify("forwarded", false), m.reloadCurrent()
	case recalledMsg:
		if msg.err != nil {
			return m.notify("recall: "+msg.err.Error(), true), nil
		}
		return m.notify("recalled", false), m.reloadCurrent()
	case reactedMsg:
		if msg.err == nil {
			// React brought the summary up to date in the same breath, so the
			// panes are asked for it now rather than left to the revision
			// watcher: a press Feishu answered without changing anything —
			// taking back a reaction it no longer holds — bumps no revision,
			// and waiting on one would leave the press drawn until it aged
			// out. The press stays until that reload lands, so the strip does
			// not flicker back to the summary it is about to replace.
			m.answerReact(msg.p.seq)
			return m, m.reloadCurrent()
		}
		m.dropReact(msg.p.seq)
		m.layout()
		return m.notify("react: "+msg.err.Error(), true), nil
	case mentionsLoadedMsg:
		if !m.mentions {
			return m, nil
		}
		for _, h := range msg.hits {
			m.markDots([]store.Message{h.msg})
		}
		m.searchLocal, m.searchMeta = msg.hits, msg.meta
		m.rebuildHits()
		m.msgIdx, m.msgTop = 0, 0
		m.rebuildMessages()
		return m, nil
	case remoteSearchMsg:
		if !m.claimSearch(msg.gen) {
			return m, nil
		}
		m.searchBusy, m.searchCancel = false, nil
		if msg.err != nil {
			// A remote search failing costs the cold half of the answer; the
			// store's half is already on screen, so this is a notice.
			return m.notify("Feishu search: "+msg.err.Error(), true), nil
		}
		m.searchRemote = msg.hits
		m.rebuildHits()
		m.rebuildMessages()
		return m, nil
	case openHitMsg:
		m.closeSearch()
		m.pendingSelect = msg.messageID
		m.notice = ""
		return m, m.openChatFrom(msg.chatID, msg.sinceMs)
	case aiChunkMsg:
		return m.onAIChunk(msg.chunk)
	case threadLoadedMsg:
		if msg.threadID != m.threadID {
			return m, nil
		}
		wasOn := idAt(m.thread, m.threadIdx)
		anchor := topAnchor(m.threadRows, m.thread, m.threadTop)
		tailed := atTail(m.threadRows, m.threadTop, m.listHeight())
		// The chat page carries the replies too, so the markers the reader
		// arrived to are already lit; this pane only has to speak for what
		// lands while they are away.
		if !m.focused {
			m.markDots(msg.msgs)
		}
		m.threadBase, m.threadMeta = msg.msgs, msg.meta
		m.applyOutbox()
		if m.threadIdx >= len(m.thread) {
			m.threadIdx = max(0, len(m.thread)-1)
		}
		m.repinSelection(wasOn)
		m.rebuildThread()
		m.threadTop = holdTop(m.threadRows, m.thread, anchor, tailed, m.threadTop, m.listHeight())
		return m, nil
	case revMsg:
		return m, tea.Batch(waitForRev(m.revs), m.reloadCurrent())
	case syncStatusMsg:
		// The status bar has room for the status word but not the reason, so
		// a newly reported failure only exists in the log.
		if msg.lastError != "" && msg.lastError != m.syncErr {
			m.deps.Log.Warn("sync", "status", msg.status, "err", msg.lastError)
		}
		m.syncStatus, m.syncErr = msg.status, msg.lastError
		return m, pollSyncStatus(m.deps.Store)
	case sentMsg:
		it := m.outboxAt(msg.localID)
		if it != nil && len(msg.keys) > len(it.keys) {
			it.keys = msg.keys
		}
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
	case pastedMsg:
		if msg.err != nil {
			return m.notify("paste: "+msg.err.Error(), true), nil
		}
		switch msg.clip.kind {
		case clipEmpty:
			return m.notify("the clipboard is empty", true), nil
		case clipText:
			m.input.InsertString(msg.clip.text)
		case clipImage:
			m.input.InsertString(imageRef(msg.clip.path))
		case clipFile:
			// A picture goes in as one so it draws in the list; anything else
			// goes in as an attachment, which is what a file copied in Finder
			// was meant to be.
			if isImagePath(msg.clip.path) {
				m.input.InsertString(imageRef(msg.clip.path))
			} else {
				m.input.InsertString(fileRef(msg.clip.path))
			}
		}
		m.replan()
		m.layout()
		return m.notify("", false), nil
	case editedMsg:
		if msg.path != "" {
			defer os.Remove(msg.path)
		}
		if msg.err != nil {
			// The editor was quit without saving, or could not run at all;
			// either way the draft in the composer is the one to keep.
			m.pics.forget()
			m.layout()
			return m.notify("editor: "+msg.err.Error(), true), nil
		}
		b, err := os.ReadFile(msg.path)
		if err != nil {
			m.pics.forget()
			m.layout()
			return m.notify("editor: "+err.Error(), true), nil
		}
		// Taken verbatim, empty included: that is the user clearing the draft.
		m.input.SetValue(string(b))
		m.replan()
		// The editor owned the screen, so every placement it cleared has to be
		// transmitted again before the panes are drawn.
		m.pics.forget()
		m.layout()
		return m.notify("", false), nil
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
	case chatPollDueMsg:
		// The chain re-arms whether or not it polls: a blurred or daemon-backed
		// beat still has to hand the next one on.
		cmds := []tea.Cmd{scheduleChatPoll()}
		if id := m.claimChatPoll(time.Now()); id != "" {
			m.chatPollInFlight = true
			m.chatPollBeat++
			cmds = append(cmds, pollChat(m.deps, id, m.openThreadID()), m.rideAlong(id))
		}
		return m, tea.Batch(cmds...)
	case chatPolledMsg:
		m.notePollResult(msg.err, time.Now())
		return m, nil
	case chatRefreshDueMsg:
		if !m.claimChatRefresh(msg.chatID) {
			return m, nil
		}
		return m, tea.Batch(refreshReadStatus(m.deps, msg.chatID), refreshReactions(m.deps, msg.chatID))
	case contextMsg:
		return m.notify(fmt.Sprintf("copied %s · %s · %s", plural(msg.n, "msg", "msgs"), humanBytes(int64(len(msg.text))), msg.chat), false),
			tea.SetClipboard(msg.text)
	case tea.MouseClickMsg:
		if m.help.open {
			// Nothing under the overlay is clickable, and a click is not a
			// key the reader meant as "done reading".
			return m, nil
		}
		return m.onClick(tea.Mouse(msg))
	case tea.MouseWheelMsg:
		return m.onWheel(tea.Mouse(msg))
	case tea.KeyPressMsg:
		return m.onKey(msg)
	}
	return m.forward(msg)
}

// forward passes a message to the focused text component. A paste arrives
// this way — bracketed from the terminal, or as the textarea's own reply to
// ctrl+v — and changes the draft as surely as a keystroke does, so the badge
// and the panes are brought back in step here too.
func (m Model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	switch m.mode {
	case modeInsert:
		before := m.composerRows()
		m.input, cmd = m.input.Update(msg)
		m.tookDraft(before)
	case modeCommand, modeFilter, modeSearch:
		m.cmdline, cmd = m.cmdline.Update(msg)
	case modeEmoji:
		return m.typeIntoFilter(msg)
	}
	return m, cmd
}

// tookDraft brings the badge, the preview and the panes back in step after
// the composer took something in: before is the row split as it stood
// beforehand, and the writing area grows with the draft while the preview
// grows with what the draft renders to, so either one moves everything above
// the box. The preview is redrawn first because the split is measured against
// its rows; reading the split off the previous keystroke's preview would
// leave the panes above sized for a box the composer no longer draws.
func (m *Model) tookDraft(before composerRows) {
	m.replan()
	m.rebuildPreview()
	if before != m.composerRows() {
		m.layout()
	}
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
	// The composer belongs to the chat that is still open, so its contents go
	// back to that chat before the page for the next one is asked for.
	keep := m.saveComposer()
	m.pendingChat, m.pendingSince = chatID, sinceMs
	m.selectCurrentChat()
	return tea.Batch(keep, loadMessages(m.deps, chatID, sinceMs), scheduleChatRefresh(chatID))
}

// draftForRow is the draft the chat list draws its marker from. The open chat
// answers from the composer rather than from the map: its row is beside the
// text being typed, so a marker one refresh behind would visibly disagree with
// what is on screen.
func (m Model) draftForRow(chatID string) store.Draft {
	if chatID == m.chatID {
		return store.Draft{ChatID: chatID, Text: m.input.Value()}
	}
	return m.drafts[chatID]
}

// saveComposer puts what the composer holds back under the chat it was typed
// in. One widget serves every chat, so without this a half-written message
// follows the reader into the next chat and is sent to the wrong person.
func (m Model) saveComposer() tea.Cmd {
	if m.chatID == "" {
		return nil
	}
	replyTo := ""
	if m.replyTo != nil {
		replyTo = m.replyTo.MessageID
	}
	return saveDraft(m.deps, m.chatID, m.input.Value(), replyTo, m.inThrd)
}

// quit ends the program, keeping what is in the composer. Sequence, not Batch:
// Quit ends the program, and a draft written alongside it would race the exit.
func (m Model) quit() tea.Cmd { return tea.Sequence(m.saveComposer(), tea.Quit) }

// enterChat swaps the panes over to the chat whose page has just arrived.
// Everything the previous chat owned — its thread, the message being quoted,
// the search it was reached from — goes at that same moment, so no pane is
// ever left showing one chat under another's name.
func (m *Model) enterChat() {
	m.searching, m.searchHits, m.searchQuery = false, nil, ""
	m.dots = nil
	m.chatID, m.msgSince = m.pendingChat, m.pendingSince
	m.pendingChat, m.pendingSince = "", 0
	m.msgs, m.msgsBase, m.msgRows, m.msgIdx, m.msgTop = nil, nil, nil, 0, 0
	m.threadOpen, m.threadID, m.thread, m.threadBase, m.threadRows = false, "", nil, nil, nil
	m.replyTo, m.inThrd = nil, false
	m.selectCurrentChat()
}

// restoreDraft fills the composer from the chat being entered. The quote is
// put back with the text, since a draft that answers something is only that
// draft while it still says what it answers.
func (m *Model) restoreDraft(d store.Draft, msgs []store.Message) {
	m.input.SetValue(d.Text)
	m.input.MoveToEnd()
	m.inThrd = d.InThread
	m.replyTo = nil
	if d.ReplyTo != "" {
		if i := indexOfID(msgs, d.ReplyTo); i >= 0 {
			m.replyTo = &msgs[i]
		}
	}
	m.replan()
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

// clearDotsAtCursor drops the unread marker of the block the cursor has just
// been moved onto. The Feishu client has no message cursor, so there is
// nothing to copy: moving onto a block is the closest a list with a cursor
// comes to the client's own "the reader has seen this".
//
// The search pane is left alone: its hits run across chats, none of which the
// reader has opened, so the marker is all that says a hit is still waiting.
func (m *Model) clearDotsAtCursor() {
	switch {
	case m.focus == paneMessages && !m.searching:
		m.clearBlockDots(m.msgs, m.msgIdx, m.msgStyleFor(m.messagesWidth()-2, m.meta))
	case m.focus == paneThread && !m.aiOpen:
		m.clearBlockDots(m.thread, m.threadIdx, m.msgStyleFor(m.rightWidth()-2, m.threadMeta))
	}
}

// clearBlockDots drops the markers of every message under one sender line.
// The dot is the block's, and a block splits where its messages disagree
// about it, so clearing one message alone would open a second sender line
// under the reader's eyes.
func (m *Model) clearBlockDots(msgs []store.Message, idx int, st msgStyle) {
	if len(m.dots) == 0 || idx < 0 || idx >= len(msgs) {
		return
	}
	heads := blockHeads(msgs, st)
	head := heads[idx]
	for i, h := range heads {
		if h == head {
			delete(m.dots, msgs[i].MessageID)
		}
	}
}

// takeRead records that the reader has had a chat's page in front of them,
// which is what drops the badge. readKey decides when that is true; this only
// settles it.
//
// The Feishu client keeps a red dot of its own, which only the client itself
// can drop. A page carrying something the badge counts therefore also walks
// the client onto the chat, so reading here settles both badges rather than
// leaving one lit for a later trip to Feishu. That covers the chat under the
// reader's eyes as well as the one just opened: a message landing in it
// relights the client's dot, and reaching it drops the dot again.
//
// unreadWaiting is the narrower of the two gates. readKey fires for anything
// markChatRead would settle, thread replies included; only what the chat badge
// counts is worth an applink, because the client will not drop its dot for a
// reply the chat's message flow does not show.
func (m Model) takeRead(chatID string, msgs []store.Message) tea.Cmd {
	cmds := []tea.Cmd{markChatRead(m.deps.Store, m.deps.Log, chatID)}
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
	m.scrollChatToCursor()
}

// repinChat carries the cursor and the viewport across a reload, each by the
// chat it was on. Chats sort by newest message, so a message landing in a
// quiet chat carries it tens of rows; an index kept across the swap would
// follow the row rather than the chat.
//
// The two are pinned apart: the viewport holds the chat on its top row and the
// cursor holds its own. Pulling the viewport onto the cursor is what throws a
// wheel scroll away a second after the reader spent it, and the message pane's
// head already names the chat that is open. A filter being typed owns both.
func (m *Model) repinChat(wasCursor, wasTop string) {
	if m.mode == modeFilter {
		m.clampChat()
		return
	}
	vis := m.visibleChats()
	if idx := indexOfChat(vis, wasCursor); idx >= 0 {
		m.chatIdx = idx
	}
	if top := indexOfChat(vis, wasTop); top >= 0 {
		m.chatTop = top
	}
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
	return loadThread(m.deps, threadID)
}

func (m Model) reloadCurrent() tea.Cmd {
	cmds := []tea.Cmd{loadChats(m.deps)}
	if m.chatID != "" {
		cmds = append(cmds, loadMessages(m.deps, m.chatID, m.msgSince))
	}
	if m.threadOpen {
		cmds = append(cmds, loadThread(m.deps, m.threadID))
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
		if m.searching {
			// Only a message hit is a message; a chat or a person row has
			// nothing for the callers that want one.
			if h, ok := m.selectedHit(); ok && h.kind == hitMessage {
				return h.msg, true
			}
			return store.Message{}, false
		}
		if m.msgIdx >= 0 && m.msgIdx < len(m.msgs) {
			return m.msgs[m.msgIdx], true
		}
	}
	return store.Message{}, false
}

// messageAt is the message a row of a message pane belongs to. The messages
// pane lists search hits while a query is open, and those index a different
// slice than the open chat's.
func (m Model) messageAt(p pane, idx int) (store.Message, bool) {
	list := m.msgs
	switch {
	case p == paneThread:
		list = m.thread
	case m.searching:
		list = m.searchMessages()
	}
	if idx < 0 || idx >= len(list) {
		return store.Message{}, false
	}
	return list[idx], true
}

// searchMessages is the message hits alone, for the callers that can only do
// something with a message.
func (m Model) searchMessages() []store.Message {
	out := make([]store.Message, 0, len(m.searchHits))
	for _, h := range m.searchHits {
		if h.kind == hitMessage {
			out = append(out, h.msg)
		}
	}
	return out
}

// selectedZones are the targets the selected message draws, in the order it
// draws them, so the keyboard reaches everything the mouse can press. The
// mouse resolves by where it was pressed and never needs this.
//
// A target wrapped across rows is one target, not one per row, and a card
// that repeats a link is listed once: what is listed is what can be opened,
// and the same place twice reads as two different ones.
func (m Model) selectedZones() []clickZone {
	rows, idx := m.msgRows, m.msgIdx
	if m.focus == paneThread {
		rows, idx = m.threadRows, m.threadIdx
	}
	var out []clickZone
	seen := map[string]bool{}
	for _, r := range rows {
		if r.idx != idx {
			continue
		}
		for _, z := range r.zones {
			// A reaction chip is not a place to open, and o is the open key;
			// the keyboard reaches the chips through e instead.
			if len(z.urls) == 0 || seen[z.urls[0]] {
				continue
			}
			seen[z.urls[0]] = true
			out = append(out, z)
		}
	}
	return out
}

// rightOpen reports whether the third pane (thread or assistant) is shown.
func (m Model) rightOpen() bool { return m.threadOpen || m.aiOpen || m.infoOpen }

func (m Model) currentChat() (store.Chat, bool) {
	for _, c := range m.chats {
		if c.ChatID == m.chatID {
			return c, true
		}
	}
	return store.Chat{}, false
}

// visibleChats narrows the list to what the filter answers. The filter only
// decides who comes in, never who comes first: the order is the reader's own
// recency, and a list that reranks under their hand is one they cannot learn.
func (m Model) visibleChats() []store.Chat {
	if m.chatFilter == "" {
		return m.chats
	}
	var out []store.Chat
	for _, c := range m.chats {
		if _, ok := m.chatIx.match(c, m.chatFilter); ok {
			out = append(out, c)
		}
	}
	return out
}

// clampChat keeps the cursor and the viewport inside the list. Neither is
// pulled to the other: a wheel scroll is allowed to park the cursor off
// screen, and only a cursor move scrolls the list back to it.
func (m *Model) clampChat() {
	n := len(m.visibleChats())
	m.chatIdx = clamp(m.chatIdx, 0, max(0, n-1))
	m.chatTop = clamp(m.chatTop, 0, max(0, n-m.chatListHeight()))
}

// scrollChatToCursor brings the chat under the cursor onto the screen. Only a
// cursor move spends it, so a wheel scroll that parked the cursor off screen
// survives until the reader moves it again.
func (m *Model) scrollChatToCursor() {
	h := m.chatListHeight()
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
		return m, m.quit()
	}
	if m.help.open {
		return m.onHelpKey(k)
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
	case modeForward:
		return m.onForwardKey(k)
	case modeTarget:
		return m.onTargetKey(k)
	case modeSearch:
		return m.onSearchKey(k)
	case modeVisual:
		return m.onVisualKey(s)
	}
	return m.onNormalKey(s)
}

func (m Model) onInsertKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	before := m.composerRows()
	// The popup answers first, because the keys it owns are ones the composer
	// otherwise has: Enter would send, Tab and the arrows would reach the
	// writing area.
	if m.pumShowing() {
		if next, cmd, took := m.onPumKey(k); took {
			return next, cmd
		}
	}
	switch k.String() {
	case "esc":
		m.mode = modeNormal
		m.input.Blur()
		return m.focusMessages(), nil
	case "ctrl+r":
		m.setReply(nil, false)
		return m, nil
	case "ctrl+o":
		m.previewOpen = !m.previewOpen
		m.layout()
		return m, nil
	case "ctrl+v":
		// Intercepted before the textarea, whose own ctrl+v shells out to
		// pbpaste and so can only ever see text.
		return m, pasteClipboard(m.deps)
	case "ctrl+g":
		// Intercepted before the textarea, which binds ctrl+g to select-all.
		// A chat composer has far more use for a real editor than for that.
		return m, editExternally(m.deps.Env, m.input.Value())
	case "enter":
		return m.submit()
	}
	var cmd tea.Cmd
	m.input, cmd = m.input.Update(k)
	// The run is re-read rather than watched for: the trigger can arrive by
	// paste or be reached by moving the cursor, and neither is a keypress that
	// says so.
	m.takePum()
	m.tookDraft(before)
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

// filterPin holds a filter session's borrowings: the pane the reader was in,
// and the chats the cursor and the top row sat on. The chats are held by id
// because typing renumbers the list under both.
type filterPin struct {
	focus       pane
	cursor, top string
}

func (m Model) onFilterKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeNormal
		m.cmdline.Blur()
		m.cmdline.Reset()
		m.chatFilter = ""
		m.focus = m.filterPin.focus
		m.repinChat(m.filterPin.cursor, m.filterPin.top)
		return m, nil
	case "enter":
		m.mode = modeNormal
		m.cmdline.Blur()
		m.clampChat()
		m.scrollChatToCursor()
		return m, m.openHighlighted()
	}
	var cmd tea.Cmd
	m.cmdline, cmd = m.cmdline.Update(k)
	m.chatFilter = m.cmdline.Value()
	m.chatIdx, m.chatTop = 0, 0
	return m, cmd
}

func (m Model) onNormalKey(s string) (tea.Model, tea.Cmd) {
	// A pending confirmation owns the next key, whatever it is: leaving the
	// ordinary bindings live under a "recall this? y/n" would let one press
	// both answer the question and do something else.
	if next, cmd, answered := m.answerConfirm(s); answered {
		return next, cmd
	}
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
		return m, m.quit()
	case "?":
		return m.openHelp(), nil
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
	case "n":
		return m.jumpUnread(1)
	case "N":
		return m.jumpUnread(-1)
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
	case "D":
		return m.askRecall()
	case "f":
		return m.openForward()
	case "I":
		return m.toggleInfo()
	case "t":
		return m.toggleThread()
	case "Y":
		return m.copySelection()
	case "y":
		return m.startYank()
	case "v":
		return m.startVisual()
	case "o":
		// What the selected message draws answers first: a running call is
		// opened by joining it, an attachment by the file it brought down, a
		// link by where it leads. A message carrying none is opened where it
		// was said.
		switch zs := m.selectedZones(); len(zs) {
		case 0:
			if sel, ok := m.selected(); ok {
				return m, openInFeishu(m.deps, sel.ChatID, sel.MessagePosition)
			}
			if m.chatID != "" {
				return m, openInFeishu(m.deps, m.chatID, 0)
			}
		case 1:
			return m, openZone(m.deps, zs[0])
		default:
			return m.openTargets(zs)
		}
	case "/":
		vis := m.visibleChats()
		m.filterPin = filterPin{m.focus, chatIDAt(vis, m.chatIdx), chatIDAt(vis, m.chatTop)}
		m.mode = modeFilter
		m.focus = paneChats
		m.cmdline.Prompt = "/"
		m.cmdline.SetValue(m.chatFilter)
		return m, m.cmdline.Focus()
	case "ctrl+f":
		return m.openSearch("")
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
			m.closeSearch()
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
			list = m.searchMessages()
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

// scrollRight scrolls whichever of the assistant and the info pane holds the
// shared right-hand column, and reports whether one did. Both are read rather
// than walked, so they answer a movement key by scrolling where the thread
// moves a cursor.
func (m *Model) scrollRight(n int) bool {
	switch {
	case m.aiOpen:
		m.aiTop = clamp(m.aiTop+n, 0, max(0, len(m.aiLines())-m.listHeight()))
	case m.infoOpen:
		m.infoTop = clamp(m.infoTop+n, 0, max(0, len(m.infoLines(m.rightWidth()-2))-m.listHeight()))
	default:
		return false
	}
	return true
}

// move shifts the selection in the focused list by n rows.
func (m Model) move(n int) (tea.Model, tea.Cmd) {
	switch m.focus {
	case paneChats:
		m.chatIdx = clamp(m.chatIdx+n, 0, len(m.visibleChats())-1)
		m.clampChat()
		m.scrollChatToCursor()
		return m, m.moveToChat(time.Now())
	case paneMessages:
		count := len(m.msgs)
		if m.searching {
			count = len(m.searchHits)
		}
		m.msgIdx = clamp(m.msgIdx+n, 0, count-1)
		m.clearDotsAtCursor()
		m.rebuildMessages()
		m.scrollMessagesToSelection()
	case paneThread:
		if m.scrollRight(n) {
			return m, nil
		}
		m.threadIdx = clamp(m.threadIdx+n, 0, len(m.thread)-1)
		m.clearDotsAtCursor()
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
			return m.openHit()
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
	// Planned before setReply lays the panes out, so the session's first
	// frame previews the draft the composer actually holds.
	m.replan()
	m.setReply(replyTo, inThread)
	return m, m.input.Focus()
}

// replan re-resolves the draft after anything that can change it.
func (m *Model) replan() { m.draft, m.draftErr = m.files.planDraft(m.input.Value()) }

// submit puts the draft on screen before it puts it on the wire: the bubble
// is what says the message went, so nothing holds a second one back either.
func (m Model) submit() (tea.Model, tea.Cmd) {
	text := strings.TrimSpace(m.input.Value())
	if text == "" {
		return m, nil
	}
	p, err := m.files.planDraft(text)
	if err != nil {
		// A path the user mistyped: the draft stays put so it can be fixed.
		return m.notify(err.Error(), true), nil
	}
	// Names become tags only on the way out. The draft stays the ordinary text
	// the reader typed, so it can go on being edited, and the bubble draws what
	// they wrote until Feishu's own rendering comes back with the @ resolved.
	p.send = m.tagMentions(p.send)
	it := outboxItem{localID: uuid.NewString(), chatID: m.chatID, msgType: p.kind.msgType(),
		send: p.send, body: p.body, images: p.uploads(), file: p.file, createMs: time.Now().UnixMilli()}
	if m.replyTo != nil {
		it.chatID, it.replyTo, it.inThread = m.replyTo.ChatID, m.replyTo.MessageID, m.inThrd
		if m.inThrd {
			it.threadID = m.replyTo.ThreadID
		}
	}
	cmd := m.sendItem(it)
	m.enqueue(it)
	m.input.Reset()
	m.replan()
	m.setReply(nil, false)
	m.refreshPanes()
	if i := indexOfID(m.msgs, it.localID); i >= 0 {
		m.msgIdx = i
		m.rebuildMessages()
		m.scrollMessagesToSelection()
	}
	return m.notify("", false), cmd
}

// tagMentions rewrites the @ runs in a body into the tags Feishu notifies on.
// Both the text and the post paths go through it: lark-cli normalizes <at> for
// each, and a mention is worth as much in one as in the other.
func (m Model) tagMentions(o larkcli.Outgoing) larkcli.Outgoing {
	o.Text = resolveMentions(o.Text, m.picked, m.roster)
	o.Markdown = resolveMentions(o.Markdown, m.picked, m.roster)
	return o
}

// sendItem is the command that puts one outbox item on the wire.
func (m Model) sendItem(it outboxItem) tea.Cmd {
	if it.replyTo != "" {
		return replyMsg(m.deps, it.localID, it.replyTo, it.send, it.inThread, it.images, it.file, it.keys)
	}
	return sendMsg(m.deps, it.localID, larkcli.Target{ChatID: it.chatID}, it.send, it.images, it.file, it.keys)
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
		return m, m.quit()
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
		p, err := m.files.planDraft(text)
		if err != nil {
			return m.notify(err.Error(), true), nil
		}
		// No outbox item: :send can fire at a chat no pane is showing, and
		// at a user whose chat id only Feishu knows.
		if strings.HasPrefix(ref, "ou_") {
			return m.notify("sending…", false), sendMsg(m.deps, "", larkcli.Target{UserID: ref}, p.send, p.uploads(), p.file, nil)
		}
		for _, c := range m.chats {
			if c.ChatID == ref || c.Name == ref {
				return m.notify("sending…", false), sendMsg(m.deps, "", larkcli.Target{ChatID: c.ChatID}, p.send, p.uploads(), p.file, nil)
			}
		}
		return m.notify("unknown chat "+ref, true), nil
	case "preview":
		m.previewOpen = !m.previewOpen
		m.layout()
		note := "preview off"
		if m.previewOpen {
			note = "preview on"
		}
		return m.notify(note, false), nil
	case "mentions", "at":
		return m.openMentions()
	case "search", "s":
		// The same panel ctrl+f opens, with the argument already in it: one
		// implementation, two ways in.
		return m.openSearch(strings.TrimSpace(rest))
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
	x, ok := m.selected()
	if !ok {
		return m.notify("select a message to react to", true), nil
	}
	hits := m.emoji.Search(arg)
	if len(hits) == 0 {
		return m.notify("no emoji matches "+arg, true), nil
	}
	return m.toggleReaction(x, hits[0].Emoji.Key)
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
		m.replan()
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
		// The pane's head takes the focus and nothing else: there is no row
		// under the click to put the cursor on.
		if row < 0 {
			return m, nil
		}
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
			return m.pressZone(paneMessages, m.msgRows, m.msgTop+row, z)
		}
		if idx := rowAt(m.msgRows, m.msgTop+row); idx >= 0 {
			m.msgIdx = idx
			m.clearDotsAtCursor()
			m.rebuildMessages()
			if double {
				return m.activate()
			}
		}
	case paneThread:
		if z, ok := zoneAt(m.threadRows, m.threadTop+row, ms.X-(m.width-m.rightWidth())-1); ok {
			return m.pressZone(paneThread, m.threadRows, m.threadTop+row, z)
		}
		if idx := rowAt(m.threadRows, m.threadTop+row); idx >= 0 {
			m.threadIdx = idx
			m.clearDotsAtCursor()
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

// pressZone answers a click on a target a row drew: a reaction chip toggles
// the reader's own reaction, anything else is handed over to be opened.
//
// Pressing twice in a row is deliberately not deduplicated. The direction is
// read off the strip on screen, which the first press has already changed, so
// a double click adds and then takes back — which is what the client does.
func (m Model) pressZone(p pane, rows []msgRow, line int, z clickZone) (tea.Model, tea.Cmd) {
	if z.react == "" {
		return m, openZone(m.deps, z)
	}
	x, ok := m.messageAt(p, rowAt(rows, line))
	if !ok {
		return m, nil
	}
	return m.toggleReaction(x, z.react)
}

// wheelStep is how far one notch scrolls: rows for the lists, lines for the
// message panes, and lines of the draft for the writing area.
const wheelStep = 3

func (m Model) onWheel(ms tea.Mouse) (tea.Model, tea.Cmd) {
	p, row := m.hit(ms.X, ms.Y)
	step := wheelStep
	if ms.Button == tea.MouseWheelUp {
		step = -wheelStep
	} else if ms.Button != tea.MouseWheelDown {
		return m, nil
	}
	// The help overlay covers the panes, so the wheel scrolls what is on
	// screen rather than what the pointer would have been over.
	if m.help.open {
		m.helpScroll(step)
		return m, nil
	}
	switch p {
	case paneChats:
		vis := m.visibleChats()
		m.chatTop = clamp(m.chatTop+step, 0, max(0, len(vis)-m.chatListHeight()))
	case paneMessages:
		m.msgTop = clamp(m.msgTop+step, 0, max(0, len(m.msgRows)-m.msgListHeight()))
	case paneThread:
		// The column is shared, and the wheel scrolls whichever of the three
		// is drawn in it. The thread alone is scrolled by its own top, since
		// the cursor walking it is what move() shifts instead.
		if !m.scrollRight(step) {
			m.threadTop = clamp(m.threadTop+step, 0, max(0, len(m.threadRows)-m.listHeight()))
		}
	case paneInput:
		switch m.composerBand(row) {
		case bandPreview:
			m.previewTop = clamp(m.previewTop+step, 0, m.previewBottom())
		case bandInput:
			// Only the writing area answers: in every other mode the box is
			// drawn by a chooser that owns those rows, and the textarea behind
			// it is blurred.
			if m.mode != modeInsert {
				return m, nil
			}
			// textarea drags its viewport back to the caret on every update,
			// so moving the caret is the only scroll its API reaches. The
			// Feishu client leaves the caret where it was; there is no way to
			// do that here without reimplementing the widget.
			for range wheelStep {
				if step < 0 {
					m.input.CursorUp()
				} else {
					m.input.CursorDown()
				}
			}
		}
	}
	return m, nil
}

func clamp(v, lo, hi int) int {
	if hi < lo {
		return lo
	}
	return min(max(v, lo), hi)
}

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
	case modeForward:
		return "FORWARD"
	case modeSearch:
		return "SEARCH"
	case modeTarget:
		return "OPEN"
	}
	return "NORMAL"
}

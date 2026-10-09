// Package tui is the interactive client: chats, messages, threads and a
// composer, driven by vim-style keys and the mouse, fed by the local store.
package tui

import (
	"cmp"
	"context"
	"fmt"
	"image/color"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
	"uuid"

	"charm.land/bubbles/v2/textarea"
	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/agentctx"
	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/larkmd"
	"github.com/amzyang/larkim/larkweb"
	"github.com/amzyang/larkim/markread"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/amzyang/larkim/todoist"
	"github.com/amzyang/larkim/tui/component/spinner"
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
	modeEmoji
	modeSearch
	modeTarget
	modeForward
	modeCandidates
	modeContextMenu
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

	// cfg is the configuration this session is running, seeded from Deps and
	// rewritten by :set and :config. It lives on the Model rather than on the
	// pieces seeded from it so a change reaches a chain already running — the
	// clear queue re-arms its tick from clearPace per chat.
	cfg config.Config

	chats []store.Chat
	// threads are the reader's own conversations inside those chats. They
	// stand in the list beside the chats rather than marking them: a reply
	// does not move the chat it lands in, which is what a thread is for.
	threads []store.ThreadFeed
	// rows is the two of them interleaved, which is what the pane draws and
	// what the cursor walks, held across the several calls one pass makes for
	// it.
	rows   *rowsCache
	unread map[string]int64
	// drafts is every chat's unsent composer state, for the chat list's own
	// marker. The open chat's draft lives in the composer, not here, so this
	// map is one refresh behind for that one row — see draftForRow.
	drafts map[string]store.Draft
	// frameDrafts is the same for the thread rows, keyed by thread.
	frameDrafts map[string]store.Draft
	// cands is how many pending lark-watch drafts each chat holds, for the
	// chat list's marker. lark-watch mirrors them in; the revision bump a
	// put or clear costs is what refreshes this map.
	cands map[string]int
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
	// cand is the chooser over lark-watch's pending drafts for the open
	// chat, open only in modeCandidates.
	cand menu[store.Candidate]
	// candFilled names the mid whose candidate the composer was seeded with,
	// so the send that leaves the composer can drop the mirrored row. It does
	// not follow the reader into another chat.
	candFilled string
	// chatCands are the mirrored drafts for the open chat's current page.
	chatCands []store.Candidate
	// candIgnored hides individual drafts for this session (see candidateKey).
	candIgnored map[string]struct{}
	// picker is the emoji chooser, open only in modeEmoji.
	picker picker
	// pum is the completion popup over the writing area, open only while
	// something is being written.
	pum pum
	// cmdcomp is the completion list over the : line, open only in
	// modeCommand.
	cmdcomp cmdComp
	// confirm is the action waiting on y or n, if any. A zero value leaves
	// every key on its ordinary path.
	confirm confirmation
	// reEdit is the text a recall now under way owes the composer, nil for a
	// plain recall. It is captured at the confirmation because the recall is
	// what destroys it.
	reEdit *reEditPending
	// fwd is the forward chooser, open only in modeForward.
	fwd forwarder
	// contextMenu is the right-click action list, open only in modeContextMenu.
	contextMenu contextMenuState
	// help is the ? overlay, which takes every key while it is open.
	help helpPanel
	// config is the :config overlay, the editor of the configuration file.
	// It takes every key too, and stands over the help panel's own.
	config configPanel
	// todoistProjects is the account's projects as last listed, Inbox first.
	// It outlives the chooser so the todoist.project row names its project
	// rather than its id, and a new token empties it: the list was another
	// account's.
	todoistProjects []todoist.Project
	// ai is the assistant, seeded from Deps and built again when :config
	// changes the model or the variable the key is read from. It lives on the
	// Model rather than on Deps so that change reaches the next question.
	ai AIStreamer
	// suggester fills the reaction picker's contextual row, seeded from Deps
	// and built again when :config changes the key variable or the endpoint.
	// It lives beside ai for the same reason: so a change reaches the next
	// press of e.
	suggester ReactSuggester
	// suggestGen numbers the questions asked of it, so an answer that arrives
	// after its picker closed is dropped rather than drawn over the next one.
	suggestGen int64
	// suggestCache is the answers already had, by message id. See suggest.go.
	suggestCache map[string]suggestion
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
	chatIx  *chatIndex
	avatars avatars
	// gists memoises one frame of chat-list lines, so picturePrepare and the
	// pane that draws them do not each summarise every visible row.
	gists *gistCache
	// pics draws message images. It stays nil, and the avatars colour blocks,
	// until the handshake said this terminal draws pictures.
	pics *pictures
	// cellW, cellH are the last cell size the terminal reported, kept for the
	// renderers, so a resize can hand them the new grid.
	cellW, cellH int
	// disp is the display scale as last reported, kept for the same reason.
	disp       display
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
	// metaGen is meta's revision. Laid-out assistant turns quote senders and
	// preview cards through meta, so a page that lands new names restyles
	// them.
	metaGen  int
	msgIdx   int
	msgTop   int // first visible line of the message pane
	msgRows  []msgRow
	msgSince int64 // when set, the page starts here instead of at the newest messages
	// feed is the Unread panel: one page holding every chat that still owes
	// the reader an answer, parted into a section each. Non-nil is the whole
	// discriminator — see unreadfeed.go.
	feed *unreadFeed
	// msgLimit is how many of the chat's messages the page asks the store
	// for. Scrolling to the top of it raises it by another page.
	msgLimit int
	// msgPullInFlight marks a fetch of history from behind the store's floor,
	// which answers with a database write rather than with a page.
	msgPullInFlight bool
	// dots holds the messages the unread marker is drawn against, gathered as
	// pages arrive and dropped on the way into another chat. Opening a chat
	// takes its messages as read at once, so a marker read straight off the
	// store would go out under the reader's eyes; this visit keeps showing
	// what was waiting when it began, and the next one starts clean.
	dots map[string]bool
	// readAt is the readKey the last takeRead answered. Holding it, rather
	// than diffing against the model this update began with, is what keeps a
	// view that leaves the tail and comes back from firing a second clear
	// for the same message while the first one's write is still in flight.
	readAt string
	// clears paces badge clears, which the read gate and a mark-all both feed.
	// See clearq.go.
	clears clearQueue
	// pendingChat is a chat whose page has been asked for but not arrived. The
	// panes stay on the chat they are showing until it does, so a cursor
	// running down the list never leaves a blank behind it.
	pendingChat  string
	pendingSince int64
	pendingLimit int
	// cursorMovedAt is when the chat cursor last moved, which is what tells a
	// sweep down the list from a single keypress.
	cursorMovedAt time.Time

	// visualAnchor is the message the VISUAL selection started from, held by
	// id rather than index because a sync tick can replace the whole list
	// while the selection is open.
	visualAnchor string

	// The right column shows one message-list frame at a time, unpacked into
	// these fields; rightKind says which kind it is and rightNone that the
	// column is closed. threadID is the frame's own id — a thread's, or for a
	// forward the message whose children are listed — and rightRoot the
	// bundle those children are stored under.
	rightKind  rightKind
	rightRoot  string
	threadID   string
	thread     []store.Message
	threadBase []store.Message
	threadMeta msgMeta
	threadIdx  int
	threadTop  int
	threadRows []msgRow
	// rightStack holds the frames underneath the one on screen. Only the
	// covered ones are packed, so every pane, rebuild and scroll goes on
	// reading the fields it always read.
	rightStack []rightFrame
	// rightDraftPin says the column's box is still waiting for its frame's
	// draft, and is spent on the first list to land under it. Without it a
	// reload would put back a draft the reader has just cleared.
	rightDraftPin bool
	// rightPin puts a popped frame back where the reader left it, spent on
	// the first list to land under it.
	rightPin rightFrame
	// rightName titles the visible frame, taken from the summary that opened
	// it and refreshed by the level that lands.
	rightName string
	// rightNote is what a frame says in place of messages: a bundle being
	// expanded, or one Feishu will not expand at all.
	rightNote string

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
	searchGen int
	// pendingSelect is the message a jump is bound for. thread names the
	// frame it lives in, empty for the chat's own flow: a folded reply is not
	// in m.msgs at all, and landing on the chat with nothing selected is the
	// silent failure this field exists to make impossible.
	pendingSelect pendingJump

	// aiP is the assistant column: sessions per chat, its own box, and the
	// answers streaming into them. Held by pointer so an answer finishing
	// while the column is hidden lands in the model still on screen; nil
	// until the panel is first opened. See assistant.go.
	aiP *aiPanel

	// spin is the one loading/streaming indicator every busy row draws; see
	// spin.go for what keeps it running.
	spin spinner.Model

	// roster is who is in the open chat: what @ completes against, and what
	// turns the names it inserted into tags on the way out.
	roster []store.Contact
	// picked remembers which person each inserted name stood for, so two
	// colleagues sharing a display name resolve to the one chosen. Keyed by
	// name rather than by offset, because the draft goes on being edited.
	picked map[string]string

	// input is the box under the message panes and rightInput the one the
	// right column carries; side says which of the two the keys go to. See
	// sidebox.go.
	input      textarea.Model
	rightInput textarea.Model
	side       composerSide
	cmdline    textinput.Model
	replyTo    *store.Message
	inThrd     bool
	// rightReply is what the right box answers, nil for the frame itself.
	// Whether that lands inside a thread is the frame's business, not a flag
	// of its own, so there is no inThrd beside it.
	rightReply *store.Message
	// draft is what the composer holds, resolved on every keystroke so the
	// badge names the message type — and any refused path — before Enter
	// commits to it. files resolves the paths a draft names.
	draft    draftPlan
	draftErr error
	// draftLint is what the body loses on the way to Feishu — an @ that
	// notifies nobody, an emoji that arrives as its own name. It rides beside
	// draftErr rather than inside it: a refused path stops the send, while
	// these are the reader's call.
	draftLint []larkmd.Finding
	files     draftFiles
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
	statusWarn string
	syncStatus string
	syncErr    string
	pendingG   bool
	pendingY   bool
	lastClick  time.Time
	lastClickY int
	revs       <-chan int64
	cancel     context.CancelFunc
}

// openApplink and newClearBadge are what New gives a Deps that names
// neither, and clearPace is the gap between two badge clears. The tests swap
// all three: the real opener moves the desktop of the machine running them,
// the real lever reads its browser's cookies and posts to Feishu, and the
// real gap costs 100ms per chat drained.
var (
	openApplink   = applink.Open
	newClearBadge = markread.New
	clearPace     = larkweb.Pace
)

// New builds the model.
func New(d Deps) Model {
	ti := textinput.New()
	ti.Prompt = ":"
	ti.SetVirtualCursor(false)
	d.Env = d.env()
	if d.Clipboard == nil {
		d.Clipboard = readClipboard
	}
	if d.Fetch == nil {
		d.Fetch = sync.HTTPFetch
	}
	if d.Log == nil {
		d.Log = discardLog
	}
	// A puller is what every reach for something Feishu holds goes through,
	// so it is never absent: the injected one carries the sweep's options
	// when the sweep runs here, and this one stands in when it does not.
	if d.Syncer == nil {
		s := &sync.Syncer{Client: d.Client, Store: d.Store, Clock: sync.RealClock{}, Log: d.Log}
		if d.Config.SilenceSync {
			s.SetSettleSilenced(newClearBadge(d.Config.MarkRead, d.Log, d.Store))
		}
		d.Syncer = s
	}
	// After the logger: the opener is the one hand-over to a subprocess the
	// TUI makes, and it logs the argv it builds.
	if d.OpenURL == nil {
		log := d.Log
		d.OpenURL = func(targets []string) error { return openApplink(log, targets) }
	}
	if d.ClearBadge == nil && d.NewClearBadge == nil {
		log, st := d.Log, d.Store
		d.NewClearBadge = func(cfg config.MarkRead) markread.Clear { return newClearBadge(cfg, log, st) }
	}
	if d.ClearBadge == nil {
		d.ClearBadge = d.NewClearBadge(d.Config.MarkRead)
	}
	prunePasted(d.DataDir, time.Now())
	m := Model{deps: d, input: newComposer(true), rightInput: newComposer(true), cmdline: ti, spin: newSpin(),
		focus: paneChats, focused: true, previewOpen: true,
		cfg:        d.Config,
		ai:         d.AI,
		suggester:  d.Suggest,
		msgLimit:   messagePageSize,
		emoji:      emoji.NewReactionIndex().WithCustom(d.DataDir),
		emojiWrite: emoji.NewComposerIndex(),
		chatIx:     newChatIndex(),
		gists:      newGistCache(),
		rows:       newRowsCache(),
		avatars:    textAvatars{},
		files:      osDraftFiles()}
	m.emoji.LoadUsed(d.DataDir)
	m.emojiWrite.LoadUsed(d.DataDir)
	// The handshake settles the renderers before the first frame rather than
	// after it: no colour blocks turning into avatars a blink later, no dark
	// palette turning light.
	if d.Term.CellW > 0 {
		m.cellW, m.cellH = d.Term.CellW, d.Term.CellH
	}
	m.disp = d.Term.Display
	if d.Term.Graphics && d.DataDir != "" {
		k := newKittyAvatars(d.DataDir)
		k.setCellSize(m.cellW, m.cellH)
		m.avatars = k
		m.pics = newPictures(d.DataDir, true)
		m.pics.setCellSize(m.cellW, m.cellH)
		m.pics.setDisplay(m.disp)
	}
	bg, dark := color.Color(color.Black), true
	if d.Term.BG != nil {
		bg, dark = d.Term.BG, d.Term.Dark
	}
	m.setBackground(bg, dark)
	return m
}

// setBackground derives every shaded style from the terminal background.
func (m *Model) setBackground(bg color.Color, dark bool) {
	m.dark = dark
	m.th = themeFor(bg, dark)
	m.input.SetStyles(composerStyles(dark))
	m.rightInput.SetStyles(composerStyles(dark))
	m.cmdline.SetStyles(textinput.DefaultStyles(dark))
	if m.aiP != nil {
		m.aiP.input.SetStyles(composerStyles(dark))
	}
}

// Run starts the program until quit or ctx is done.
func Run(ctx context.Context, d Deps) error {
	ctx, cancel := context.WithCancel(ctx)
	// The handshake reads the terminal before tea owns its input, so the
	// first frame is drawn the way the whole session keeps looking.
	d.Term = Probe(os.Stdin, os.Stdout)
	m := New(d)
	m.cancel = cancel
	m.revs = d.Store.WatchRev(ctx, watchEvery, d.Nudge)
	_, err := tea.NewProgram(m, tea.WithContext(ctx)).Run()
	cancel()
	return err
}

func (m Model) Init() tea.Cmd {
	cmds := tea.Batch(loadChats(m.deps), readSyncStatus(m.deps.Store), pollSyncStatus(m.deps.Store), waitForRev(m.revs),
		loadSelfName(m.deps), keychainStartup(m.deps))
	if claim := kittyClaimPaste(); claim != nil {
		cmds = tea.Batch(cmds, claim)
	}
	return tea.Batch(cmds, scheduleChatPoll())
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
	// After the handler, so the interleave compared is the one this frame
	// will draw. The cache is held through a pointer, so what it drops here
	// is gone for the picturePrepare below and the View that follows.
	nm.gists.hold(nm.rows.all(nm.chats, nm.threads))
	// On the Unread page a key that lands the cursor on another message, or
	// the focus on the pane from the list or the frame beside it, reads that
	// message's chat. Clicks read in onClick, which knows what was hit;
	// stepping back out of the composer or a chooser lands nowhere new.
	if _, key := msg.(tea.KeyPressMsg); key && m.inFeed() && nm.inFeed() && nm.focus == paneMessages &&
		(idAt(m.msgs, m.msgIdx) != idAt(nm.msgs, nm.msgIdx) || m.focus == paneChats || m.focus == paneThread) {
		read := nm.readFeedCursor()
		cmd = tea.Batch(cmd, read)
	}
	// Reading is settled here rather than where a page arrives, because
	// arriving is only one of the ways a page comes to be in front of the
	// reader: scrolling back to the tail, closing the help overlay, widening
	// the terminal out of the fold and returning to the window all reach it
	// too, and each of them is an ordinary message through this seam.
	if k := nm.readKey(atTail(nm.msgRows, nm.msgTop, nm.msgListHeight())); k != "" && k != nm.readAt {
		nm.readAt = k
		var read tea.Cmd
		nm, read = nm.takeRead(nm.chatID, nm.msgsBase)
		cmd = tea.Batch(cmd, read)
	}
	if spin := nm.paceSpin(msg); spin != nil {
		cmd = tea.Batch(cmd, spin)
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
	var claimed picSet
	collect := func(rows []msgRow, lo, hi int) {
		claimed.takeRows(rows[clamp(lo, 0, len(rows)):clamp(hi, 0, len(rows))])
	}
	// The draft being previewed is claimed first, on the same rule: a picture
	// the reader is looking at outranks one behind it. Without this the
	// preview reserves cells the terminal was never handed.
	collect(m.previewRows, m.previewTop, m.previewTop+m.composerRows().preview)
	// The list standing over the panes is claimed on the same rule, from the
	// very pieces it draws, so no list can show a picture it never asked for.
	for _, row := range m.floatSegs() {
		for _, seg := range row {
			claimed.take(seg.pic)
		}
	}
	// The lines standing over the composer are claimed with it: the message
	// a reply quotes and the one being forwarded are what the reader is
	// working on, and each spends at most a couple of ids.
	for _, side := range []composerSide{sideMain, sideRight} {
		x, ok := m.quotedOn(side)
		if !ok {
			continue
		}
		head, gist, room := m.replyBarParts(side, x, m.bandWidth(side)-2)
		for _, s := range gistSegs(head, gist, room, spellOf(x.MsgType), stDim, m.chatPics().gist) {
			claimed.take(s.pic)
		}
	}
	if m.mode == modeForward {
		for _, s := range m.fwdGistSegs(m.bandWidth(m.side) - 2) {
			claimed.take(s.pic)
		}
	}
	// The chat list is claimed next. Its reactions and the emoji on its
	// summaries are a handful of icons that many rows draw from the same ids,
	// and unlike the message bands below it reaches for nothing off screen.
	vis := m.visibleRows()
	for i := m.chatTop; i < len(vis) && i < m.chatTop+m.chatListHeight(); i++ {
		g := m.gistFor(vis[i])
		for _, s := range g.chips {
			claimed.take(s.pic)
		}
		for _, s := range g.summary {
			claimed.take(s.pic)
		}
	}
	// Then the rows on screen, so a pane crowded with pictures spends what is
	// left on what the reader is looking at.
	for _, band := range [][2]int{{0, h}, {h, 2 * h}, {-h, 0}} {
		collect(m.msgRows, m.msgTop+band[0], m.msgTop+band[1])
		collect(m.threadRows, m.threadTop+band[0], m.threadTop+band[1])
	}
	return m.pics.prepare(claimed.pics)
}

// avatarPrepare asks the renderer for what the chats around the viewport
// need, reaching beyond each edge so a scroll shows its pictures on the frame
// it arrives rather than the one after. The window never outgrows the id
// space: a wider one evicts a picture it is about to draw, and the pass that
// redraws that one evicts another, so the list never stops being redrawn. The
// renderer caches through a pointer, so the work survives this value copy.
func (m Model) avatarPrepare() string {
	vis := m.visibleRows()
	n := min(len(vis), 3*m.chatListHeight(), kittyIDs)
	// A viewport taller than the id space cannot be covered whole, and the
	// margin then has to go to the rows above the fold rather than below it.
	h := min(m.chatListHeight(), n)
	top := clamp(m.chatTop-(n-h)/2, 0, len(vis)-n)
	return m.avatars.prepare(vis[top:top+n], m.unread)
}

// update is the handler Update wraps. Its arms take the model by value and
// hand a new one back, while the helpers they call take it by pointer, so a
// command is always drawn into a variable of its own before the return:
//
//	cmd := m.openChat(id)
//	return m, cmd
//
// Written as one statement, Go leaves it unsaid whether the m being returned
// is read before or after the call writes to it, and where the other operand
// is itself a call — notify, say — the spec's left-to-right order guarantees
// the copy wins and the write is lost.
func (m Model) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = msg.Width, msg.Height
		m.layout()
		// The terminal reports its cell size only when asked, and changing the
		// font is what resizes the grid without resizing the window. Asking
		// again here is the only way a stale cell size — every picture scaled
		// for the wrong grid — gets corrected. Moving the window to another
		// screen changes the cell size and the scale together, so the scale is
		// asked again with it.
		return m, tea.Raw(ansi.WindowOp(ansi.RequestCellSizeWinOp) + displayQuery)
	case tea.CapabilityMsg:
		if !m.disp.read(msg.Content) || !m.pics.setDisplay(m.disp) {
			return m, nil
		}
		clear(m.gists.rows)
		m.layout()
		return m, nil
	case uv.CellSizeEvent:
		m.cellW, m.cellH = msg.Width, msg.Height
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
		// The summaries carry pictures sized for the old grid, and the lists
		// they were taken from have not moved, so nothing else drops them.
		clear(m.gists.rows)
		m.layout()
		return m, nil
	case tea.FocusMsg:
		m.focused = true
		// Discovery runs flat out while somebody is looking, starting now.
		m.deps.Syncer.SetAttended(true)
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
		m.deps.Syncer.SetAttended(false)
		return m, m.saveComposer()
	case chatsLoadedMsg:
		vis := m.visibleRows()
		wasCursor, wasTop := rowKeyAt(vis, m.chatIdx), rowKeyAt(vis, m.chatTop)
		m.chats, m.threads, m.unread = msg.chats, msg.threads, msg.unread
		m.drafts, m.frameDrafts, m.cands = msg.drafts, msg.frameDrafts, msg.cands
		// The list opens on the Unread row, which is the one row that answers
		// what is waiting without taking any of it as read. Opening a chat
		// instead would put its page in front of a reader who has not looked
		// at anything yet, and the read gate would settle it and send the
		// client after it.
		var open tea.Cmd
		if m.openingChat() == "" && m.feed == nil && len(m.chats) > 0 {
			open = m.startUnread(false)
		}
		m.repinChat(wasCursor, wasTop)
		// The panel's closing note counts the chats its page leaves out, and
		// this listing is what it counts over. Rows built against the previous
		// one would state a number the list beside them contradicts.
		if m.inFeed() {
			m.rebuildMessages()
		}
		return m, open
	case candidatesLoadedMsg:
		rows := m.visibleCandidates(msg.rows)
		if len(rows) == 0 {
			return m.notify("no pending reply drafts in this chat", true), nil
		}
		m.mode = modeCandidates
		m.cand = fillMenu(rows, candSpec)
		m.layout()
		return m, nil
	case candidateClearedMsg:
		return m, nil
	case draftSavedMsg:
		// The drafts table sits outside data_rev on purpose, so no reload is
		// owed to the write; without landing it here the row a switch just
		// left holds its old gist until an unrelated sync bumps the revision.
		if msg.err != nil {
			return m, nil // logged by the write; the maps stay a refresh behind
		}
		m.landDraft(msg.draft)
		return m, nil
	case messagesLoadedMsg:
		// A page for the chat the panel's cursor happens to rest in is not the
		// panel's page: it would put that chat's whole history under the
		// frozen section rules. Only the chat the panel is leaving for counts,
		// and enterChat takes the panel down as it lands.
		if m.feed != nil && msg.chatID != m.pendingChat {
			return m, nil
		}
		// A reload is not a cursor move. Cursor and viewport are held apart,
		// each by the message it was on, because a page that slid a message off
		// its head renumbers every row: the cursor keeps its own message, and
		// the view keeps the message on its top row. Only a view already
		// showing the last line follows the message that arrives, and only a
		// cursor already on the newest message goes with it.
		wasOn, atEnd := idAt(m.msgs, m.msgIdx), m.msgIdx >= newestSelectable(m.msgs)
		anchor := topAnchor(m.msgRows, m.msgs, m.msgTop)
		tailed := atTail(m.msgRows, m.msgTop, m.msgListHeight())
		entering := msg.chatID == m.pendingChat
		var entered tea.Cmd
		switch msg.chatID {
		case m.pendingChat:
			entered = m.enterChat()
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
		// entered carries the write the closing right column owed, since the
		// chat being left is the chat its frame belonged to.
		infoCmd := entered
		m.msgsBase, m.meta = msg.msgs, msg.meta
		m.chatCands = msg.cands
		m.metaGen++
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
		m.msgIdx = newestSelectable(m.msgs)
		if i := indexOfID(m.msgs, wasOn); !atEnd && i >= 0 {
			m.msgIdx = i
		}
		want := m.pendingSelect
		jumped := want.id != "" && want.thread == ""
		if jumped {
			if i := indexOfID(m.msgs, want.id); i >= 0 {
				m.msgIdx = i
			}
		}
		m.pendingSelect = pendingJump{}
		// A folded reply is not on this page at all, so the thread opens over
		// the chat with its own cursor on the reply. Which pane comes away
		// focused is the landing's business: a hit the reader asked for hands
		// them the reply, a thread row the chats cursor walked onto only shows
		// it.
		var open tea.Cmd
		if want.thread != "" {
			at := m.focus
			if want.takeFocus {
				at = paneThread
			}
			m, open = m.openRightIn(rightFrame{kind: rightThread, id: want.thread, sel: want.id}, at)
			infoCmd = tea.Batch(infoCmd, open)
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
	case unreadFeedLoadedMsg:
		// The panel may have come down, or gone up again as another visit,
		// while the page was out.
		if msg.visit != m.feed {
			return m, nil
		}
		// A page read before a chat was read here does not know to hold it,
		// and once that read lands it takes away the section the reader is in.
		if msg.gen != m.feed.gen {
			cmd := m.reloadCurrent()
			return m, cmd
		}
		// A reload is not a cursor move: cursor and viewport are each held by
		// the message they were on, the way a chat's own reload holds them. A
		// section dropping out renumbers every row behind it.
		entering := len(m.msgs) == 0
		wasOn := idAt(m.msgs, m.msgIdx)
		anchor := topAnchor(m.msgRows, m.msgs, m.msgTop)
		m.feed.sections = msg.sections
		m.msgsBase, m.meta = msg.msgs, msg.meta
		m.metaGen++
		m.applyOutbox()
		// A marker goes out under the cursor, as on a chat's own page, not
		// with the read: the ones the page arrives with stand until then.
		m.markDots(msg.msgs)
		m.rebuildMessages()
		// With nothing waiting there is no section for the composer to answer,
		// and the chat that was open before is not what the pane is showing.
		if len(msg.msgs) == 0 {
			m.chatID = ""
		}
		if entering {
			m.msgIdx, m.msgTop = 0, 0
		} else {
			if i := indexOfID(m.msgs, wasOn); i >= 0 {
				m.msgIdx = i
			}
			m.msgIdx = clamp(m.msgIdx, 0, max(0, len(m.msgs)-1))
			m.msgTop = holdTop(m.msgRows, m.msgs, anchor, false, m.msgTop, m.msgListHeight())
		}
		// Taken before the return: feedRetarget writes to the receiver, and
		// Go does not say whether the bare m beside it is read first.
		cmd := m.feedRetarget()
		return m, cmd
	case chatSideMsg:
		// The cursor may have walked on to another section while this was out.
		if msg.chatID != m.chatID {
			return m, nil
		}
		m.roster = msg.roster
		// A composer already carrying something is the reader bringing it
		// here; the chat's own draft would take it away, and restoreDraft
		// would drop the quote r just put up along with it.
		if m.composerHeld() {
			return m, nil
		}
		m.restoreDraft(msg.draft, m.msgs)
		if m.feed != nil {
			m.feed.loaded = msg.chatID
		}
		return m, nil
	case searchRestMsg:
		if !m.claimSearch(msg.gen) {
			return m, nil
		}
		return m, localSearch(m.deps, m.chats, m.searchQuery, msg.gen)
	case remoteRestMsg:
		if !m.claimSearch(msg.gen) {
			return m, nil
		}
		cmd := m.startRemote()
		return m, cmd
	case searchMsg:
		if !m.claimSearch(msg.gen) {
			return m, nil
		}
		m.landHits(msg.hits, msg.meta)
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
		m.refreshSilencePick()
		return m, nil
	case silenceMatchesMsg:
		return m.onSilenceMatches(msg), nil
	case silenceRosterLoadedMsg:
		return m.onSilenceRoster(msg), nil
	case todoistProjectsMsg:
		return m.onTodoistProjects(msg), nil
	case forwardedMsg:
		if msg.err != nil {
			return m.notify("forward: "+msg.err.Error(), true), nil
		}
		return m.notify("forwarded", false), m.reloadCurrent()
	case recalledMsg:
		if msg.err != nil {
			m.reEdit = nil
			return m.notify("recall: "+msg.err.Error(), true), nil
		}
		if p := m.reEdit; p != nil && p.messageID == msg.messageID {
			m.reEdit = nil
			return m.fillReEdit(*p)
		}
		return m.notify("recalled", false), m.reloadCurrent()
	case chatMutedMsg:
		if msg.err != nil {
			verb := "unmute"
			if msg.muted {
				verb = "mute"
			}
			return m.notify(verb+": "+msg.err.Error(), true), nil
		}
		note := "unmuted"
		if msg.muted {
			note = "muted"
		}
		return m.notify(note, false), m.reloadCurrent()
	case markAllSetMsg:
		return m.onMarkAllSet(msg)
	case sectionDotMsg:
		return m.pushClears(msg.chats)
	case feedReadMsg:
		return m.onFeedRead(msg)
	case clearDueMsg:
		return m.onClearDue(msg)
	case clearFiredMsg:
		return m.onClearFired(msg)
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
		m.landHits(msg.hits, msg.meta)
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
		m.pendingSelect = pendingJump{id: msg.messageID, thread: msg.threadID}
		m.notice = ""
		cmd := m.openChatFrom(msg.chatID, msg.sinceMs)
		return m, cmd
	case suggestedMsg:
		return m.onSuggested(msg)
	case streamWrittenMsg:
		return m.onStreamWritten(msg)
	case aiChunkMsg:
		return m.onAIChunk(msg)
	case aiStartedMsg:
		return m.onAIStarted(msg)
	case threadLoadedMsg:
		if m.rightKind != rightThread || msg.threadID != m.threadID {
			return m, nil
		}
		land := m.rightLanded(msg.msgs)
		// The chat page carries no replies, so this pane is the only place
		// their markers can be lit — and it runs before the settle below,
		// which reads the flags this has just copied.
		m.markDots(msg.msgs)
		m.threadBase, m.threadMeta = msg.msgs, msg.meta
		m.applyOutbox()
		m.takeRightDraft(msg.draft, m.thread)
		m.placeRightCursor(land)
		m.repinSelection(land.cursor)
		m.rebuildThread()
		m.settleRight(land)
		return m, nil
	case replyLoadedMsg:
		return m.onReplyLoaded(msg)
	case forwardLoadedMsg:
		return m.onForwardLoaded(msg)
	case forwardExpandedMsg:
		return m.onForwardExpanded(msg)
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
		if m.aiP != nil {
			m.markAISent(msg.localID)
		}
		// The message is on Feishu either way. A store write that failed only
		// means the row is not local yet, so the bubble stays up and the next
		// sync to bring the row in is what retires it.
		if msg.ingestErr != nil {
			return m.notify("sent · not stored locally yet: "+msg.ingestErr.Error(), true), m.reloadCurrent()
		}
		// A bubble on screen waits for the reload below: dropped now, it would
		// leave a gap until the page carrying its row lands, and applyOutbox
		// retires it then. One no pane shows has nothing to hand over to.
		if it != nil && !m.onScreen(it.localID) && !m.onScreen(it.messageID) {
			m.dropOutbox(msg.localID)
		}
		m.refreshPanes()
		var clear tea.Cmd
		if m.candFilled != "" {
			clear = clearCandidate(m.deps, m.candFilled)
			m.candFilled = ""
		}
		cmds := tea.Batch(m.reloadCurrent(), clear)
		// A send that put a bubble up says nothing more: the bubble already
		// said it went. :send has no bubble, so the notice is its answer.
		if it == nil {
			return m.notify("sent", false), cmds
		}
		return m, cmds
	case pastedMsg:
		if msg.err != nil {
			return m.notify("paste: "+msg.err.Error(), true), nil
		}
		text, empty := pasteTextFromClip(msg.clip)
		if empty {
			return m.notify("the clipboard is empty", true), nil
		}
		if m.pasteIntoFocusedInput() {
			next, cmd := m.forward(tea.PasteMsg{Content: text})
			return next.(Model).notify("", false), cmd
		}
		// The composer may have lost focus while the clipboard was read, and a
		// blurred textarea drops what Update hands it; the paste still lands.
		before := m.composerRows()
		m.areap().InsertString(text)
		m.tookDraft(before)
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
		m.areap().SetValue(string(b))
		m.replan()
		// The editor owned the screen, so every placement it cleared has to be
		// transmitted again before the panes are drawn.
		m.pics.forget()
		m.layout()
		return m.notify("", false), nil
	case selfNameMsg:
		m.selfName = msg.name
		m.refreshPanes()
		return m, nil
	case errMsg:
		return m.notify(msg.err.Error(), true), nil
	case noticeMsg:
		return m.notify(msg.text, false), nil
	case statusWarnMsg:
		m.statusWarn = msg.text
		return m, nil
	case chatRestMsg:
		r, ok := m.claimRowOpen(msg.key)
		if !ok {
			return m, nil
		}
		// The command is taken first: opening a thread pins where it lands on
		// the model, and a model read beside the call may be read before it.
		cmd := m.openRow(r, false)
		return m, cmd
	case chatPollDueMsg:
		// The chain re-arms whether or not it polls: a blurred beat, or one
		// standing down after a refusal, still has to hand the next one on.
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
	case olderPulledMsg:
		cmd := m.noteOlderPull(msg)
		return m, cmd
	case chatRefreshDueMsg:
		if !m.claimChatRefresh(msg.chatID) {
			return m, nil
		}
		return m, tea.Batch(refreshReadStatus(m.deps, msg.chatID), refreshReactions(m.deps, msg.chatID))
	case contextMsg:
		return m.notify(fmt.Sprintf("copied %s · %s · %s", plural(msg.n, "msg", "msgs"), humanBytes(int64(len(msg.text))), msg.chat), false),
			tea.SetClipboard(msg.text)
	case tea.MouseClickMsg:
		if m.mode == modeContextMenu {
			return m.onClick(tea.Mouse(msg))
		}
		if m.help.open || m.floatAt(msg.X, msg.Y) {
			// Nothing under the overlay is clickable, and a click is not a
			// key the reader meant as "done reading".
			return m, nil
		}
		if msg.Button == tea.MouseRight {
			return m.onRightClick(tea.Mouse(msg))
		}
		return m.onClick(tea.Mouse(msg))
	case tea.MouseWheelMsg:
		return m.onWheel(tea.Mouse(msg))
	case tea.MouseMotionMsg:
		if m.mode == modeContextMenu {
			return m.onContextMenuMotion(tea.Mouse(msg))
		}
		return m, nil
	case tea.KeyPressMsg:
		return m.onKey(msg)
	case tea.PasteMsg:
		if m.composerSmartPaste() {
			return m, pasteClipboard(m.deps)
		}
	}
	return m.forward(msg)
}

// forward passes a message to the focused text component. A paste arrives
// this way — bracketed from the terminal into filters and command lines, or as
// an input's own reply to ctrl+v — so it picks the input in the order onKey
// picks the handler: the
// overlays take the keys whatever the mode, and so the paste too. Each input
// goes through the same typeInto step its keys do, so what its value drives
// (the draft's badge and panes, a search, a completion list, a narrowed list)
// moves with a paste as surely as with a keystroke.
func (m Model) forward(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch {
	case m.config.open:
		return m.forwardConfig(msg)
	case m.help.open:
		if m.help.filtering {
			return m.typeIntoHelpFilter(msg)
		}
		return m, nil
	}
	switch m.mode {
	case modeInsert:
		return m.typeIntoComposer(msg)
	case modeCommand:
		return m.typeIntoCommand(msg)
	case modeFilter:
		return m.typeIntoChatFilter(msg)
	case modeSearch:
		return m.typeIntoSearch(msg)
	case modeEmoji:
		return m.typeIntoFilter(msg)
	case modeForward:
		return m.typeIntoForward(msg)
	case modeContextMenu:
		return m, nil
	}
	return m, nil
}

// tookDraft brings the popup, the badge, the preview and the panes back in
// step after the composer took something in: before is the row split as it
// stood beforehand, and the writing area grows with the draft while the
// preview grows with what the draft renders to, so either one moves
// everything above the box. The preview is redrawn first because the split is
// measured against its rows; reading the split off the previous keystroke's
// preview would leave the panes above sized for a box the composer no longer
// draws.
//
// The popup's run is re-read rather than watched for: the trigger can arrive
// by paste or be reached by moving the cursor, and neither is a keypress that
// says so.
func (m *Model) tookDraft(before composerRows) {
	m.takePum()
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
	// A page anchored at a hit has to reach from it to the newest message, so
	// it is bounded far more loosely than one that opens at the tail.
	limit := messagePageSize
	if sinceMs > 0 {
		limit = anchoredPageSize
	}
	m.pendingChat, m.pendingSince, m.pendingLimit = chatID, sinceMs, limit
	m.candFilled = ""
	m.selectCurrentChat()
	return tea.Batch(keep, loadMessages(m.deps, chatID, sinceMs, limit), scheduleChatRefresh(chatID))
}

// draftForRow is the draft a chat's row draws its marker from. The open chat
// answers with nothing: its row sits beside the composer the draft lives in,
// and the list leaves the conversation being typed in bare of its own draft.
func (m Model) draftForRow(chatID string) store.Draft {
	if chatID == m.chatID {
		return store.Draft{}
	}
	return m.drafts[chatID]
}

// draftForThreadRow is draftForRow for the thread rows the list carries beside
// the chats: the frame standing in the right column answers with nothing for
// the same reason, every other from the map.
func (m Model) draftForThreadRow(threadID string) store.Draft {
	if m.rightHasComposer() && threadID == m.threadID {
		return store.Draft{}
	}
	return m.frameDrafts[threadID]
}

// gistFor is the gist a list row draws: the message's, or the draft the reader
// saved over it — never on the row of what is open, whose draft is already on
// screen in the composer beside it.
func (m Model) gistFor(r listRow) rowGist {
	g := m.gists.at(r, m.deps.Self, m.chatPics())
	if r.isThread() {
		if m.rightHasComposer() && r.thread.ThreadID == m.threadID {
			return g
		}
		return g.withDraft(m.frameDrafts[r.thread.ThreadID])
	}
	if r.chatID() == m.chatID {
		return g
	}
	return g.withDraft(m.drafts[r.chatID()])
}

// landDraft settles what a saved write did onto the row maps. An empty draft is
// a delete — the same reading the store takes of one — so the gist it stood in
// for falls back to the chat's last message.
func (m *Model) landDraft(d store.Draft) {
	which, key := &m.frameDrafts, d.FrameID
	if key == "" {
		which, key = &m.drafts, d.ChatID
	}
	if d.Empty() {
		delete(*which, key)
		return
	}
	if *which == nil {
		*which = map[string]store.Draft{}
	}
	(*which)[key] = d
}

// saveComposer puts both boxes back under the chat and the frame they were
// typed in. One widget serves every chat, so without this a half-written
// message follows the reader into the next chat and is sent to the wrong
// person.
func (m Model) saveComposer() tea.Cmd {
	keep := m.saveRightBox()
	return tea.Batch(keep, m.saveChatBox())
}

// saveChatBox writes the box under the message panes back under the open chat.
func (m Model) saveChatBox() tea.Cmd {
	if m.chatID == "" {
		return nil
	}
	// Under the unread panel the composer is only the chat's to write when the
	// reader put words in it or the chat's own draft is what it is showing.
	// The panel walks m.chatID between chats without waiting for a page, so a
	// chat can be the target for a beat before its draft arrives — and an
	// empty write is a delete.
	if m.feed != nil && m.input.Value() == "" && m.feed.loaded != m.chatID {
		return nil
	}
	replyTo := ""
	if m.replyTo != nil {
		replyTo = m.replyTo.MessageID
	}
	return saveDraft(m.deps, store.Draft{ChatID: m.chatID, Text: m.input.Value(),
		ReplyTo: replyTo, InThread: m.inThrd})
}

// quit ends the program, keeping what is in the composer. Sequence, not Batch:
// Quit ends the program, and a draft written alongside it would race the exit.
// The answers in flight are cancelled first, since nothing else will.
func (m Model) quit() tea.Cmd {
	cmds := []tea.Cmd{m.saveComposer()}
	if m.aiP != nil {
		inner := m
		cmds = append(cmds, m.aiP.stopAll(&inner)...)
	}
	if release := kittyReleasePaste(); release != nil {
		cmds = append(cmds, release)
	}
	return tea.Sequence(append(cmds, tea.Quit)...)
}

// enterChat swaps the panes over to the chat whose page has just arrived.
// Everything the previous chat owned — its thread, the message being quoted,
// the search it was reached from — goes at that same moment, so no pane is
// ever left showing one chat under another's name.
func (m *Model) enterChat() tea.Cmd {
	m.searching, m.searchHits, m.searchQuery = false, nil, ""
	m.feed = nil
	m.clearMessagePane()
	m.chatID, m.msgSince, m.msgLimit = m.pendingChat, m.pendingSince, m.pendingLimit
	m.pendingChat, m.pendingSince, m.pendingLimit = "", 0, 0
	// A pull still out belongs to the chat being left; its answer is dropped
	// on arrival, so the next chat starts free to ask for its own history.
	m.msgPullInFlight = false
	keep := m.closeRight()
	// The assistant column follows the reader the way the info pane does: the
	// sessions on screen become the new chat's own, and an answer still
	// streaming into the old one keeps landing there, out of sight.
	if m.aiP != nil && m.aiP.open {
		m.aiP.point(m.chatID, *m)
		m.aiP.rebuild(*m)
	}
	m.replyTo, m.inThrd = nil, false
	m.candIgnored = nil
	m.selectCurrentChat()
	return keep
}

// clearMessagePane empties the pane of the page it is showing, markers and
// all. What the next page is comes from the caller: a chat, or the panel.
func (m *Model) clearMessagePane() {
	m.dots = nil
	m.msgs, m.msgsBase, m.msgRows = nil, nil, nil
	m.msgIdx, m.msgTop = 0, 0
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
// been moved onto, and reports whether it dropped one. The Feishu client has
// no message cursor, so there is nothing to copy: moving onto a block is the
// closest a list with a cursor comes to the client's own "the reader has seen
// this".
//
// The Unread panel goes the same way, landing on a block there being reading
// its chat. The search pane is left alone: its rows run across chats none of
// which the reader has opened, so the marker is all that says one is still
// waiting.
func (m *Model) clearDotsAtCursor() bool {
	switch {
	case m.focus == paneMessages && !m.searching:
		return m.clearBlockDots(m.msgs, m.msgIdx, m.msgStyleFor(m.messagesWidth()-2, m.meta))
	case m.focus == paneThread && !m.aiOpen():
		return m.clearBlockDots(m.thread, m.threadIdx, m.msgStyleFor(m.rightWidth()-2, m.threadMeta))
	}
	return false
}

// clearBlockDots drops the markers of every message under one sender line.
// The dot is the block's, and a block splits where its messages disagree
// about it, so clearing one message alone would open a second sender line
// under the reader's eyes.
func (m *Model) clearBlockDots(msgs []store.Message, idx int, st msgStyle) bool {
	if len(m.dots) == 0 || idx < 0 || idx >= len(msgs) {
		return false
	}
	heads := blockHeads(msgs, st)
	head := heads[idx]
	before := len(m.dots)
	for i, h := range heads {
		if h == head {
			delete(m.dots, msgs[i].MessageID)
		}
	}
	return before > len(m.dots)
}

// takeRead records that the reader has had a chat's page in front of them,
// which is what drops the badge. readKey decides when that is true; this only
// settles it.
//
// The Feishu client keeps a red dot of its own, which only the client itself
// can drop. A page carrying something the badge counts therefore also clears
// the Feishu dot, so reading here settles both badges rather than leaving one
// lit for a later trip to Feishu. That covers the chat under the reader's
// eyes as well as the one just opened: a message landing in it relights the
// client's dot, and reaching it drops the dot again.
//
// unreadWaiting is the narrower of the two gates. readKey fires for anything
// markChatRead would settle, thread replies included; only what the chat badge
// counts is worth a clear, because the gateway will not drop its dot for a
// reply the chat's message flow does not show.
func (m Model) takeRead(chatID string, msgs []store.Message) (Model, tea.Cmd) {
	position, waiting := unreadWaiting(msgs)
	if !waiting {
		return m, nil
	}
	return m.pushClears([]store.ChatUnread{{ChatID: chatID, Position: position}})
}

// selectCurrentChat puts the cursor on the chat being opened within the
// visible list, dropping a filter that would hide it.
//
// A row already leading into that chat keeps the cursor: the open came from
// the cursor itself, and a thread's row would otherwise hand it straight back
// to the chat the thread happens in.
func (m *Model) selectCurrentChat() {
	vis := m.visibleRows()
	if m.chatIdx < len(vis) && vis[m.chatIdx].chatID() == m.openingChat() {
		m.clampChat()
		m.scrollChatToCursor()
		return
	}
	idx := indexOfChatRow(m.visibleRows(), m.openingChat())
	if idx < 0 && m.chatFilter != "" {
		m.chatFilter = ""
		idx = indexOfChatRow(m.visibleRows(), m.openingChat())
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
	vis := m.visibleRows()
	if idx := indexOfRow(vis, wasCursor); idx >= 0 {
		m.chatIdx = idx
	}
	if top := indexOfRow(vis, wasTop); top >= 0 {
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

// openHighlighted loads the row under the cursor unless it is already open.
func (m *Model) openHighlighted(take bool) tea.Cmd {
	r, ok := m.highlightedRow()
	if !ok {
		return nil
	}
	return m.openRow(r, take)
}

func (m Model) reloadCurrent() tea.Cmd {
	cmds := []tea.Cmd{loadChats(m.deps)}
	switch {
	case m.feed != nil:
		// The anchors travel with the reload: they are what holds a section
		// still while the reader is inside it.
		cmds = append(cmds, loadUnreadFeed(m.deps, m.feed))
	case m.chatID != "":
		cmds = append(cmds, loadMessages(m.deps, m.chatID, m.msgSince, m.msgLimit))
	}
	// Only the visible frame: a covered one is reloaded when it is uncovered.
	if cmd := m.loadRight(); cmd != nil {
		cmds = append(cmds, cmd)
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

// metaFor is the detail behind a message pane, chosen the way messageAt
// chooses which slice the pane lists.
func (m Model) metaFor(p pane) msgMeta {
	switch {
	case p == paneThread:
		return m.threadMeta
	case m.searching:
		return m.searchMeta
	}
	return m.meta
}

// pendingJump is the message a landing is bound for, and the frame it lives
// in. A folded thread reply is in no chat page, so the thread opens over the
// chat and the cursor lands inside it.
type pendingJump struct {
	id, thread string
	// takeFocus says the reader asked to be at this message, so the frame it
	// lives in comes away with the focus. A thread row the chats cursor walked
	// onto does not ask: it is a row being looked at, not a place being gone
	// to, and the reader is still reading the list.
	takeFocus bool
}

// jumpTo names the frame a message is reached in: a reply folded out of the
// chat's flow is reached through its thread, anything else on the page itself.
// Every caller is a reader going to a named message, so the frame takes the
// focus.
func jumpTo(x store.Message) pendingJump {
	if x.MessagePosition < 0 && x.ThreadID != "" {
		return pendingJump{id: x.MessageID, thread: x.ThreadID, takeFocus: true}
	}
	return pendingJump{id: x.MessageID}
}

// jumpToQuoted follows a quote back to the message it names, which is what
// pressing the quote block does in the client. That message is usually on the
// page already; one older than the page's anchor needs the page cut again
// around it, which is how a search hit lands too.
func (m Model) jumpToQuoted(p pane, id string) (tea.Model, tea.Cmd) {
	if p == paneThread {
		if i := indexOfID(m.thread, id); i >= 0 {
			m.threadIdx = i
			m.clearDotsAtCursor()
			m.rebuildThread()
			m.scrollThreadToSelection()
			return m, nil
		}
	}
	// The search panel lists hits rather than a chat, and the Unread panel
	// holds only what is still waiting, so a quote drawn in either rarely
	// points at anything on screen and never at the right copy of it.
	if m.chatPage() {
		// A thread reply can answer something said in the chat itself, which
		// is in the other pane.
		if i := indexOfID(m.msgs, id); i >= 0 {
			m.focus = paneMessages
			m.msgIdx = i
			m.clearDotsAtCursor()
			m.rebuildMessages()
			m.scrollMessagesToFoot()
			return m, nil
		}
	}
	parent, ok := m.metaFor(p).parents[id]
	if !ok {
		// The message was never stored here, so it is fetched by the id the
		// quote names before the page that holds it opens.
		return m.notify("fetching…", false), ingestThenOpen(m.deps, id)
	}
	// SinceMs is inclusive, so the page opens on the quoted message itself and
	// pendingSelect always finds it.
	m.pendingSelect = jumpTo(parent)
	m.notice = ""
	cmd := m.openChatFrom(parent.ChatID, parent.CreateMs)
	return m, cmd
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
func (m Model) rightOpen() bool { return m.threadOpen() || m.aiOpen() || m.infoOpen }

func (m Model) currentChat() (store.Chat, bool) {
	if i := indexOfChat(m.chats, m.chatID); i >= 0 {
		return m.chats[i], true
	}
	return store.Chat{}, false
}

// visibleRows narrows the list to what the filter answers. The filter only
// decides who comes in, never who comes first: the order is the reader's own
// recency, and a list that reranks under their hand is one they cannot learn.
//
// A thread answers through the chat it happens in, and through the words its
// root opened with: the row is titled by those words, so they are what the
// reader has to type at.
func (m Model) visibleRows() []listRow {
	rows := m.rows.all(m.chats, m.threads)
	if m.chatFilter == "" {
		return rows
	}
	return m.rows.narrowed(m.chatFilter, func(rows []listRow) []listRow {
		var out []listRow
		for _, r := range rows {
			// What the filter narrows is chats; the Unread row is not one and
			// has no name to type at.
			if r.isFeed() {
				continue
			}
			if _, ok := m.chatIx.match(r.chat, m.chatFilter); ok {
				out = append(out, r)
				continue
			}
			if r.isThread() && containsFold(replyGist(r.thread.Root), m.chatFilter) {
				out = append(out, r)
			}
		}
		return out
	})
}

// clampChat keeps the cursor and the viewport inside the list. Neither is
// pulled to the other: a wheel scroll is allowed to park the cursor off
// screen, and only a cursor move scrolls the list back to it.
func (m *Model) clampChat() {
	n := len(m.visibleRows())
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
	// A pending confirmation owns the next key in every mode: leaving it to
	// INSERT or command would let y land in the box or :read-all fire while
	// the question is still on screen.
	if next, cmd, answered := m.answerConfirm(s); answered {
		return next, cmd
	}
	if isClipboardPasteKey(s) && m.clipboardPasteTarget() {
		return m, pasteClipboard(m.deps)
	}
	if m.config.open {
		return m.onConfigKey(k)
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
	case modeCandidates:
		return m.onCandidatesKey(k)
	case modeSearch:
		return m.onSearchKey(k)
	case modeVisual:
		return m.onVisualKey(s)
	case modeContextMenu:
		return m.onContextMenuKey(k)
	}
	return m.onNormalKey(s)
}

func (m Model) onInsertKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
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
		m.areap().Blur()
		// The band is back at its resting height, so the textareas have to be
		// resized to it: they keep whatever sized last wrote, and the box would
		// go on drawing rows the panes above it have taken back — which fitBlock
		// makes room for by dropping the quote off the top.
		switch m.side {
		case sideAI:
			// The box belongs to the assistant column, so that is the pane
			// behind it to step back into.
			m.focus = paneThread
			m.layout()
			return m, nil
		case sideRight:
			// The box belongs to the right column, so that is the pane behind
			// it to step back into.
			m.focus = paneThread
			m.layout()
			return m, nil
		}
		next, keep := m.focusMessages()
		next.layout()
		return next, keep
	case "ctrl+r":
		if m.side == sideAI {
			m.dropAIChip()
			return m, nil
		}
		m.setQuote(nil, false)
		return m, nil
	case "ctrl+s":
		// The assistant's box alone: streaming into the chat is the one ask
		// whose answer other people watch being written.
		if m.side == sideAI {
			return m.askStreamConfirm()
		}
		return m, nil
	case "ctrl+o":
		m.previewOpen = !m.previewOpen
		m.layout()
		return m, nil
	case "ctrl+v", "super+v":
		// Intercepted before the textarea, whose own ctrl+v shells out to
		// pbpaste and so can only ever see text. super+v is Cmd+V once kitty
		// passthrough (in_larkim) delivers the key instead of paste_from_clipboard.
		return m, pasteClipboard(m.deps)
	case "ctrl+g":
		// Intercepted before the textarea, which binds ctrl+g to select-all.
		// A chat composer has far more use for a real editor than for that.
		return m, editExternally(m.deps.Env, m.area().Value())
	case "enter":
		return m.submit()
	}
	return m.typeIntoComposer(k)
}

// typeIntoComposer hands a message to the writing area and brings what the
// draft drives back in step: the popup, the badge, the preview and the panes.
func (m Model) typeIntoComposer(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m.composerRows()
	var cmd tea.Cmd
	ta := m.areap()
	*ta, cmd = ta.Update(msg)
	m.tookDraft(before)
	return m, cmd
}

func (m Model) onCommandKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// The list answers first, because the keys it owns are ones the line
	// otherwise has: Tab and the arrows would reach the text input, and Esc
	// would leave the mode outright.
	if m.cmdcomp.open() {
		if next, took := m.onCmdCompKey(k); took {
			return next, nil
		}
	}
	switch k.String() {
	case "esc":
		return m.leaveCommand(), nil
	case "enter":
		line := strings.TrimSpace(m.cmdline.Value())
		return m.leaveCommand().runCommand(line)
	}
	return m.typeIntoCommand(k)
}

// typeIntoCommand hands a message to the : line and brings the completion
// list back in step with it.
func (m Model) typeIntoCommand(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.cmdline, cmd = m.cmdline.Update(msg)
	m.takeCmdComp()
	return m, cmd
}

// leaveCommand puts the : line away. It is the one way out, so no caller can
// leave the list standing over a box that is no longer showing it.
func (m Model) leaveCommand() Model {
	m.mode = modeNormal
	m.cmdline.Blur()
	m.cmdline.Reset()
	m.cmdcomp = cmdComp{}
	m.layout()
	return m
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
		// Settling the filter is not leaving the list, so the row it settles on
		// opens where the cursor keys would have opened it.
		cmd := m.openHighlighted(false)
		return m, cmd
	}
	return m.typeIntoChatFilter(k)
}

// typeIntoChatFilter hands a message to the / line and narrows the list to
// it, the cursor going back to the top when the query came back changed.
func (m Model) typeIntoChatFilter(msg tea.Msg) (tea.Model, tea.Cmd) {
	var cmd tea.Cmd
	m.cmdline, cmd = m.cmdline.Update(msg)
	if v := m.cmdline.Value(); v != m.chatFilter {
		m.chatFilter = v
		m.chatIdx, m.chatTop = 0, 0
	}
	return m, cmd
}

func (m Model) onNormalKey(s string) (tea.Model, tea.Cmd) {
	// The assistant column takes its own keys before the switch below can
	// hand any of them to the frame hidden under it: a message nobody can see
	// is not a message to act on.
	if m.focus == paneThread && m.aiOpen() {
		if next, cmd, took := m.onAIKey(s); took {
			return next, cmd
		}
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
		// Inside the column h steps out of the frame before it steps out of
		// the pane: the stack is a containment path, so the frame beneath is
		// the one the reader came through. Esc keeps this rung and closes the
		// column on the last frame; h leaves it standing, because a column
		// beside the chat is still being read.
		if m.focus == paneThread && len(m.rightStack) > 0 {
			return m.popRight()
		}
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
		if m.inFeed() {
			return m.jumpSection(1)
		}
		return m.jumpUnread(1)
	case "N":
		if m.inFeed() {
			return m.jumpSection(-1)
		}
		return m.jumpUnread(-1)
	case "m":
		// The page's own act, and only the page's: a chat is read by being
		// gone into everywhere else, so the key is left free there.
		if m.inFeed() {
			return m.markSectionRead(m.feedChatAt(m.msgIdx))
		}
		return m.openContextMenuAtCursor()
	case " ", "space":
		return m.openContextMenuAtCursor()
	case "f10":
		return m.openContextMenuAtCursor()
	case "shift+f10":
		return m.openContextMenuAtCursor()
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
	case "E":
		return m.askReEdit()
	case "f":
		return m.openForward()
	case "C":
		return m.openCandidates()
	case "I":
		return m.toggleInfo()
	case "t":
		return m.toggleRight()
	case "Y":
		return m.copySelection()
	case "y":
		return m.startYank()
	case "T":
		// Both operands are calls, so the copy is bound to a name before the
		// return hands it back: the evaluation order there is not promised.
		out, cmd := m.todoistTask()
		return out, cmd
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
	case "/":
		vis := m.visibleRows()
		m.filterPin = filterPin{m.focus, rowKeyAt(vis, m.chatIdx), rowKeyAt(vis, m.chatTop)}
		m.mode = modeFilter
		m.focus = paneChats
		m.cmdline.Prompt = "/"
		m.cmdline.SetValue(m.chatFilter)
		focus := m.cmdline.Focus()
		return m, focus
	case "ctrl+f":
		return m.openSearch("")
	case ":", ";":
		m.mode = modeCommand
		m.cmdline.Prompt = ":"
		m.cmdline.Reset()
		m.cmdcomp = cmdComp{}
		focus := m.cmdline.Focus()
		return m, focus
	case "a", "A":
		return m.openAIKey(s == "A")
	case "esc":
		switch {
		// A mark-all sweep is the one thing here that keeps acting after the
		// key that started it, so esc is scoped to it. A read gate's own
		// a read gate's own clear is not on offer: it is the tail of a read the
		// reader already made, not something still unfolding. Dropping it costs one
		// dot until the next sweep, which does find that chat again.
		case m.clears.swept > 0:
			return m.dropClears().notify("stopped", false), nil
		case m.aiOpen():
			return m.closeAI(), nil
		case m.searching:
			m.closeSearch()
			return m.notify("", false), nil
		case m.feed != nil:
			leave := m.closeUnread()
			return m.notify("", false), leave
		case m.threadOpen() && m.focus == paneThread:
			return m.popRight()
		case m.chatFilter != "":
			m.chatFilter = ""
			m.selectCurrentChat()
			return m.notify("", false), nil
		}
		// The chat's box is what this rung is about: a quote in the right
		// column belongs to the frame, which the rung above pops.
		m.setQuoteOn(sideMain, nil, false)
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
	case "a":
		// The range names its own context: the panel opens on it with the
		// keys, and nothing is asked.
		return m.openAISelection()
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
	return m.notify("", false), nil
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
		vis := m.visibleRows()
		// The Unread row stands for no chat, so it has no id, no json and no
		// last message to hand over.
		if len(vis) == 0 || vis[m.chatIdx].isFeed() {
			return nil, true
		}
		// A thread yanks the chat it happens in: what is on offer here is a
		// link to somewhere, and the client's own link for a row like this
		// leads into the chat.
		return []yankSource{chatYank(vis[m.chatIdx].chat)}, true
	case m.focus == paneMessages, m.focus == paneThread && !m.aiOpen():
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
	if m.searching || m.feed != nil {
		return m.notify("press Enter to open the hit; v selects inside a chat", true), nil
	}
	selectable := m.focus == paneMessages && len(m.msgs) > 0 ||
		m.focus == paneThread && !m.aiOpen() && len(m.thread) > 0
	if !selectable {
		return m.notify("v selects in the messages or thread pane", true), nil
	}
	m.visualAnchor = m.focusedList()[m.cursor(m.focus)].MessageID
	m.mode = modeVisual
	return m.notify("", false), nil
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
// of the highlighted chat from the chats pane. The Unread page is a message
// list too: Y copies the message under the cursor, from the chat that message
// belongs to. Search hits stay out, because a hit is opened rather than copied.
func (m Model) copySelection() (Model, tea.Cmd) {
	switch {
	case m.searching:
		return m.notify("press Enter to open the hit; Y copies from inside a chat", true), nil
	case m.focus == paneChats:
		vis := m.visibleRows()
		if len(vis) == 0 || vis[m.chatIdx].isFeed() {
			return m.notify("nothing to copy", true), nil
		}
		spec := copySpec{chatID: vis[m.chatIdx].chatID(), rng: agentctx.Range{Since: chatsCopyAge, Limit: chatsCopyLimit}}
		return m.notify("copying…", false), copyContext(m.deps, spec)
	case m.focus == paneMessages, m.focus == paneThread && !m.aiOpen():
		list := m.focusedList()
		if len(list) == 0 {
			return m.notify("nothing to copy", true), nil
		}
		lo, hi := m.selectionRange()
		msgs := list[lo : hi+1]
		chatID := m.chatID
		// The feed draws several chats on one page, and the composer can stay
		// pointed at a chat the cursor has already left. The message names
		// its own chat.
		if m.inFeed() {
			chatID = msgs[0].ChatID
		}
		if chatID == "" {
			return m.notify("nothing to copy", true), nil
		}
		return m.notify("copying…", false), copyContext(m.deps, copySpec{chatID: chatID, msgs: msgs})
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
	case m.aiOpen():
		m.aiP.scroll(n, m.aiListHeight())
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
		m.chatIdx = clamp(m.chatIdx+n, 0, len(m.visibleRows())-1)
		m.clampChat()
		m.scrollChatToCursor()
		cmd := m.moveToChat(time.Now())
		return m, cmd
	case paneMessages:
		if m.searching {
			m.msgIdx = clamp(m.msgIdx+n, 0, len(m.searchHits)-1)
		} else {
			m.msgIdx = stepCursor(m.msgs, m.msgIdx, n)
		}
		// The rows carry no cursor — the selection is tinted where they are
		// drawn — so the only thing a move can change about them is a marker
		// it just dropped. Rebuilding regardless re-renders the whole page on
		// every press of a repeating key.
		if m.clearDotsAtCursor() {
			m.rebuildMessages()
		}
		m.scrollMessagesToSelection()
		// A cursor held under notices it cannot stop on would keep the view
		// off the top, and the top is what loads older history.
		if !m.searching && n < 0 && !slices.ContainsFunc(m.msgs[:m.msgIdx], selectable) {
			m.msgTop = 0
		}
		if m.inFeed() {
			cmd := m.feedRetarget()
			return m, cmd
		}
		cmd := m.growMessages()
		return m, cmd
	case paneThread:
		if m.scrollRight(n) {
			return m, nil
		}
		m.threadIdx = stepCursor(m.thread, m.threadIdx, n)
		if m.clearDotsAtCursor() {
			m.rebuildThread()
		}
		m.scrollThreadToSelection()
	}
	return m, nil
}

// selectable reports whether the cursor keys stop on a message. A system
// notice or a recall carries none of the actions a message does, and the
// client offers neither a toolbar.
func selectable(x store.Message) bool { return !standsAlone(x) }

// stepCursor is where a cursor on from, sent n rows along list, comes to
// rest: the first selectable message at or beyond the target, or, when only
// notices lie that way, the nearest one back towards from. Failing both it
// stays put.
func stepCursor(list []store.Message, from, n int) int {
	to := clamp(from+n, 0, len(list)-1)
	dir := cmp.Compare(n, 0)
	for i := to; i >= 0 && i < len(list); i += dir {
		if selectable(list[i]) {
			return i
		}
	}
	for i := to - dir; (i-from)*dir > 0; i -= dir {
		if selectable(list[i]) {
			return i
		}
	}
	return from
}

// newestSelectable is where a cursor sent to the end of list lands. A list of
// notices alone keeps its last row, so the cursor still has somewhere to be.
func newestSelectable(list []store.Message) int {
	for i := len(list) - 1; i >= 0; i-- {
		if selectable(list[i]) {
			return i
		}
	}
	return max(0, len(list)-1)
}

func (m Model) activate() (tea.Model, tea.Cmd) {
	switch m.focus {
	case paneChats:
		// Enter says the user means this row, so it skips the rest delay the
		// cursor keys go through and comes away in the pane the row leads to:
		// a chat's message page, a thread's own column.
		r, ok := m.rowAtCursor()
		cmd := m.openHighlighted(true)
		if ok && r.isThread() {
			// A frame still on its way takes the focus when it lands, through
			// pendingSelect; one already standing has no landing left to wait
			// for, and openHighlighted gave back nothing.
			if m.openThreadID() == r.thread.ThreadID {
				m.focus = paneThread
			}
			return m, cmd
		}
		next, keep := m.focusMessages()
		return next, tea.Batch(keep, cmd)
	case paneMessages:
		if m.searching {
			return m.openHit()
		}
		if m.feed != nil {
			return m.openFeedHit()
		}
		sel, ok := m.selected()
		if !ok {
			return m, nil
		}
		// A container opens; anything else is answered.
		if f, ok := m.containerAtCursor(); ok {
			return m.openContainer(paneMessages, f)
		}
		return m.startInsert(&sel, false)
	case paneThread:
		sel, ok := m.selected()
		if !ok {
			return m, nil
		}
		// What Enter means follows the frame: a thread is a place to answer,
		// a forward is a place to read, so there the key opens the nested
		// bundle under the cursor and nothing else.
		if m.rightKind == rightForward {
			f, ok := m.containerAtCursor()
			if !ok {
				return m.notify("only a forwarded bundle opens from here", true), nil
			}
			return m.openContainer(paneThread, f)
		}
		// A reply tree's rows are the chat's own messages, so answering one
		// lands in the flow beside it; only a thread's replies go inside a
		// thread.
		return m.startInsert(&sel, m.rightKind == rightThread)
	}
	return m, nil
}

func (m Model) startInsert(replyTo *store.Message, inThread bool) (tea.Model, tea.Cmd) {
	if m.chatID == "" {
		return m.notify("open a chat first", true), nil
	}
	if replyTo != nil && m.onForwardedChild() {
		return m.notify("a forwarded message belongs to its own chat", true), nil
	}
	// A recall leaves a notice, not a message: there is nothing left to quote.
	if replyTo != nil && replyTo.Deleted {
		return m.notify("that message was recalled", true), nil
	}
	m.pickSide()
	m.mode = modeInsert
	m.focus = paneInput
	var panel tea.Cmd
	if m.side == sideMain {
		// The Unread panel retargets the chat's box at the chat the quoted
		// message came from. The right column is always the open chat's, so
		// there is nothing there to retarget.
		panel = m.feedAnswer(replyTo)
	}
	// Answering a message on the Unread page is reading its chat.
	var read tea.Cmd
	if replyTo != nil {
		read = m.readFeedChat(replyTo.ChatID)
	}
	// Planned before setQuote lays the panes out, so the session's first
	// frame previews the draft the composer actually holds.
	m.replan()
	m.setQuote(replyTo, inThread)
	cmd := m.areap().Focus()
	return m, tea.Batch(panel, read, cmd)
}

// resumeInsert goes back to writing in the box that already has the keys,
// which is what a click on one means: the press was about the box, not about
// the message it answers.
func (m Model) resumeInsert() (tea.Model, tea.Cmd) {
	if m.chatID == "" {
		return m.notify("open a chat first", true), nil
	}
	m.mode = modeInsert
	m.focus = paneInput
	m.replan()
	m.layout()
	cmd := m.areap().Focus()
	return m, cmd
}

// replan re-resolves the draft after anything that can change it. The lint
// reads the body mentions have already been resolved in, so a name the
// composer did place draws no warning about not placing it. The assistant's
// box holds a question, not a message: there is nothing to plan, and the
// chat's own plan waits untouched for its box back.
func (m *Model) replan() {
	if m.side == sideAI {
		m.draft, m.draftErr, m.draftLint = draftPlan{}, nil, nil
		return
	}
	m.draft, m.draftErr = m.files.planDraft(m.area().Value())
	m.draftLint = larkmd.Lint(m.tagMentions(m.draft.send).Markdown)
}

// submit puts the draft on screen before it puts it on the wire: the bubble
// is what says the message went, so nothing holds a second one back either.
func (m Model) submit() (tea.Model, tea.Cmd) {
	if m.side == sideAI {
		return m.submitAI()
	}
	text := strings.TrimSpace(m.area().Value())
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
	it := outboxItem{localID: uuid.New().String(), chatID: m.chatID, msgType: p.kind.msgType(),
		send: p.send, body: p.body, images: p.uploads(), file: p.file, createMs: time.Now().UnixMilli()}
	switch {
	case m.side == sideRight:
		// A frame's answer always names a message, even when the reader only
		// meant the thread: Feishu has no way to post into one otherwise.
		x, ok := m.rightTarget()
		if !ok {
			return m.notify("nothing to answer here yet", true), nil
		}
		it.chatID, it.replyTo, it.inThread = x.ChatID, x.MessageID, m.rightKind == rightThread
		if it.inThread {
			it.threadID = m.threadID
		}
	case m.replyTo != nil:
		it.chatID, it.replyTo, it.inThread = m.replyTo.ChatID, m.replyTo.MessageID, m.inThrd
		if m.inThrd {
			it.threadID = m.replyTo.ThreadID
		}
	}
	// Sending into a chat on the Unread page is reading it, whether or not
	// the send then lands.
	cmd := tea.Batch(m.sendItem(it), m.readFeedChat(it.chatID))
	m.enqueue(it)
	m.areap().Reset()
	m.replan()
	m.setQuote(nil, false)
	wasOn, top := idAt(m.msgs, m.msgIdx), topAnchor(m.msgRows, m.msgs, m.msgTop)
	m.refreshPanes()
	if m.side == sideRight {
		if i := indexOfID(m.thread, it.localID); i >= 0 {
			m.threadIdx = i
			m.rebuildThread()
			m.scrollThreadToSelection()
		}
		return m.notify("", false), cmd
	}
	// The Unread page is a pass over many chats: the bubble waits under its
	// own section, and the cursor stays where the reader is going on from.
	if m.inFeed() {
		if i := indexOfID(m.msgs, wasOn); i >= 0 {
			m.msgIdx = i
		}
		m.msgTop = holdTop(m.msgRows, m.msgs, top, false, m.msgTop, m.msgListHeight())
		return m.notify("", false), cmd
	}
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
	run, ok := resolveCommand(name)
	if !ok {
		if near := commandsWithPrefix(name); len(near) > 1 {
			return m.notify("ambiguous :"+name+" — "+commandNames(near), true), nil
		}
		return m.notify("unknown command :"+name, true), nil
	}
	switch run.name {
	case "q":
		return m, m.quit()
	case "goto":
		want := store.FoldName(rest)
		for _, c := range m.chats {
			if c.ChatID == rest || store.FoldName(c.Name) == want {
				m, keep := m.focusMessages()
				cmd := m.openChat(c.ChatID)
				return m, tea.Batch(keep, cmd)
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
		// :send has no badge to carry the lint, so the notice does.
		sending := "sending…"
		if found := larkmd.Lint(m.tagMentions(p.send).Markdown); len(found) > 0 {
			sending += " · " + found[0].Message
		}
		// No outbox item: :send can fire at a chat no pane is showing, and
		// at a user whose chat id only Feishu knows.
		if strings.HasPrefix(ref, "ou_") {
			return m.notify(sending, false), sendMsg(m.deps, "", larkcli.Target{UserID: ref}, p.send, p.uploads(), p.file, nil)
		}
		for _, c := range m.chats {
			if c.ChatID == ref || c.Name == ref {
				return m.notify(sending, false), sendMsg(m.deps, "", larkcli.Target{ChatID: c.ChatID}, p.send, p.uploads(), p.file, nil)
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
	case "set":
		return m.runSet(rest), nil
	case "config":
		m = m.openConfig(rest)
		return m, m.configLoads()
	case "silence":
		// The rule a message under the cursor needs is its own chat and sender;
		// with none, the empty form is the one :config silence and a open.
		rule := store.SilenceRule{}
		if x, ok := m.selected(); ok {
			rule.Chat, rule.Sender = x.ChatID, x.SenderID
		}
		m = m.openConfig("silence")
		next, fcmd := m.openSilenceForm(-1, rule)
		return next.(Model), tea.Batch(m.configLoads(), fcmd)
	case "read-all":
		return m.startMarkAll()
	case "mentions":
		return m.openMentions()
	case "candidates":
		return m.openCandidates()
	case "unread":
		return m.openUnread()
	case "search":
		// The same panel ctrl+f opens, with the argument already in it: one
		// implementation, two ways in.
		return m.openSearch(strings.TrimSpace(rest))
	case "copy":
		return m.runCopy(rest)
	case "react":
		return m.runReact(rest)
	case "todoist":
		return m.todoistTask()
	case "ai":
		return m.askCommand(rest)
	case "sync":
		// A tick moves the global cursors, which belong to the sweep alone.
		if !m.deps.Embedded {
			return m.notify("sync is handled by the daemon", false), nil
		}
		s := m.deps.Syncer
		return m.notify("syncing…", false), func() tea.Msg {
			// A tick fans out over every chat that moved, so it needs room;
			// what it must not have is forever, which would hold the whole
			// background lane against a gateway that stopped answering.
			ctx, cancel := context.WithTimeout(context.Background(), syncTickTimeout)
			defer cancel()
			if _, err := s.Tick(ctx); err != nil {
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
	if x.Deleted {
		return m.notify("that message was recalled", true), nil
	}
	// A key first, because that is what the : line writes when the reader
	// completes an emoji, and it is the one spelling no search can answer.
	if e, ok := m.emoji.ByKey(arg); ok {
		return m.toggleReaction(x, e.Key)
	}
	hits := m.emoji.Search(arg)
	if len(hits) == 0 {
		return m.notify("no emoji matches "+arg, true), nil
	}
	return m.toggleReaction(x, hits[0].Emoji.Key)
}

// --- assistant ------------------------------------------------------------

// agentName is the program ai.agent starts, as the status line names it.
func (m Model) agentName() string {
	name, _, _ := strings.Cut(strings.TrimSpace(m.cfg.AI.Agent), " ")
	return filepath.Base(name)
}

// focusMessages moves focus to the messages pane; on a folded layout the
// right pane stood in for it, so that pane closes first.
func (m Model) focusMessages() (Model, tea.Cmd) {
	var keep tea.Cmd
	if m.foldRight() {
		// The reader is leaving the column, not stepping back through it, so
		// the whole stack goes rather than one frame. The assistant column
		// hides with it; its answers run on into their sessions.
		keep = m.closeRight()
		if m.aiP != nil {
			m.aiP.open = false
		}
		m.layout()
	}
	m.focus = paneMessages
	return m, keep
}

// --- mouse ----------------------------------------------------------------

func (m Model) onClick(ms tea.Mouse) (tea.Model, tea.Cmd) {
	if m.mode == modeContextMenu {
		if ms.Button == tea.MouseLeft {
			return m.contextMenuClick(ms)
		}
		return m, nil
	}
	if ms.Button != tea.MouseLeft {
		return m, nil
	}
	if m.confirm.kind != confirmNone {
		// A question on screen owns the next input, the mouse included: a
		// click is not the answer, and acting on it would move the state the
		// verdict was asked about — another chat, a regenerated answer. The
		// y/n holds the screen until a key answers it, so the verdict binds
		// to what was asked.
		return m, nil
	}
	double := time.Since(m.lastClick) < 400*time.Millisecond && m.lastClickY == ms.Y
	m.lastClick, m.lastClickY = time.Now(), ms.Y
	p, row := m.hit(ms.X, ms.Y)
	// The session picker owns the clicks while it stands open: a click on its
	// rows picks, and any other click — wherever it went — closes it.
	if m.aiOpen() && m.aiP.menu.open {
		m.aiP.menu.open = false
		if p == paneThread {
			if row >= 0 && row < m.aiP.menuRows(m.bodyHeight()) {
				return m.aiPress(aiAct{kind: actSess, sess: row})
			}
			// The head lines all collapse into row -1; which one the click
			// landed on is read off the screen, the way the messages pane
			// reads its rule's line.
			if line := ms.Y - 1; line >= 0 && line < aiHeadLines {
				if z, ok := m.aiP.headZone(line, ms.X-(m.width-m.rightWidth())-1); ok {
					return m.pressZone(paneThread, nil, 0, z)
				}
			}
		}
		return m, nil
	}
	if p == paneChats || p == paneMessages || p == paneThread {
		m.mode = modeNormal
		m.areap().Blur()
		m.focus = p
		// The pane's head takes the focus and nothing else: there is no row
		// under the click to put the cursor on. The one button it draws
		// answers first, before the head falls back to taking focus.
		if row < 0 {
			if p == paneChats && inMarkAll(ms.X-1, chatsWidth-2) {
				return m.startMarkAll()
			}
			// The pinned rule is part of the head, so its button is pressed
			// here rather than through a row: it is drawn on the line under
			// the title, which is the one hit collapses into the same -1.
			if p == paneMessages && m.inFeed() && ms.Y == 1+headerHeight {
				if w := m.messagesWidth() - 2; inMarkChat(ms.X-chatsWidth-1, w) {
					return m.markSectionRead(m.feedTopChat())
				}
			}
			// The session header's tabs, ▾ and + sit on the column's title
			// line, another head line that stands at row -1.
			if p == paneThread && m.aiOpen() {
				if z, ok := m.aiP.headZone(ms.Y-1, ms.X-(m.width-m.rightWidth())-1); ok {
					return m.pressZone(paneThread, nil, 0, z)
				}
			}
			// Focus handed to the Unread page lands on the cursor's message,
			// the way Tab does.
			if p == paneMessages {
				cmd := m.readFeedCursor()
				return m, cmd
			}
			return m, nil
		}
	}
	switch p {
	case paneChats:
		vis := m.visibleRows()
		idx := m.chatTop + row
		if idx >= 0 && idx < len(vis) {
			m.chatIdx = idx
			r := vis[idx]
			switch {
			// A thread is opened by every click: the chat under it may
			// already be the one on screen while its frame is not. The click
			// landed in the list, so the frame opens beside the reader and
			// the focus stays where the click put it.
			// A click on the Unread row asked for the panel: there is no
			// chat under it for the click to be merely selecting.
			case r.isFeed():
				m, keep := m.focusMessages()
				open := m.openRow(r, true)
				return m, tea.Batch(keep, open)
			case r.isThread():
				cmd := m.openRow(r, false)
				return m, cmd
			case r.chat.ChatID != m.pageChat(), double:
				m, keep := m.focusMessages()
				cmd := m.openRow(r, false)
				return m, tea.Batch(keep, cmd)
			}
		}
	case paneMessages:
		// A button the row draws answers first: the click asked for the
		// button, not for the message it sits on. Pane content starts one
		// column inside the border the pane is drawn with.
		line := m.msgTop + row
		// The rule's own button, which no zone carries because the pinned copy
		// is not a row to hang one on. A rule sitting at the top of the
		// viewport is that pinned copy, and renderMessages holds its line
		// blank, so pressing it here would press a button nobody drew.
		if m.inFeed() && line != m.msgTop && line < len(m.msgRows) && m.msgRows[line].rule &&
			inMarkChat(ms.X-chatsWidth-1, m.messagesWidth()-2) {
			return m.markSectionRead(m.feedChatAt(m.msgRows[line].idx))
		}
		if z, ok := zoneAt(m.msgRows, line, ms.X-chatsWidth-1); ok {
			return m.pressZone(paneMessages, m.msgRows, line, z)
		}
		if idx := rowAt(m.msgRows, line); idx >= 0 {
			// On the Unread page a click that hits no message and moves
			// nothing has landed nowhere: it reads nothing and puts no
			// marker out.
			landed := !m.inFeed() || idx != m.msgIdx || !m.msgRows[line].plain
			m.msgIdx = idx
			var read tea.Cmd
			if landed {
				m.clearDotsAtCursor()
				read = m.readFeedCursor()
			}
			m.rebuildMessages()
			if double {
				next, cmd := m.activate()
				return next, tea.Batch(read, cmd)
			}
			return m, read
		}
	case paneThread:
		// The assistant column draws over the frame: its rows are its own,
		// and nothing under them may answer a click until it is uncovered.
		if m.aiOpen() {
			line := m.aiP.top + row
			if z, ok := zoneAt(m.aiP.rows, line, ms.X-(m.width-m.rightWidth())-1); ok {
				return m.pressZone(paneThread, m.aiP.rows, line, z)
			}
			return m, nil
		}
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
		if s, ok := m.bandAt(ms.X); ok {
			m.side = s
		}
		return m.resumeInsert()
	}
	return m, nil
}

// pressZone answers a click on a target a row drew: a quote line moves the
// cursor to the message it names, a reaction chip toggles the reader's own
// reaction, anything else is handed over to be opened.
//
// Pressing twice in a row is deliberately not deduplicated. The direction is
// read off the strip on screen, which the first press has already changed, so
// a double click adds and then takes back — which is what the client does.
func (m Model) pressZone(p pane, rows []msgRow, line int, z clickZone) (tea.Model, tea.Cmd) {
	// The assistant panel's acts are things to do, not places to open.
	if z.act.kind != actNone {
		return m.aiPress(z.act)
	}
	if z.cand.c.Mid != "" {
		x, ok := m.messageAt(p, rowAt(rows, line))
		if !ok {
			return m, nil
		}
		if z.cand.send {
			return m.sendInlineCandidate(z.cand.c, x)
		}
		return m.ignoreInlineCandidate(z.cand.c)
	}
	if z.jump != "" {
		return m.jumpToQuoted(p, z.jump)
	}
	if z.open != "" {
		// Which pane the press landed in is what says whether the column
		// deepens or starts over, and it is the only thing this arm reads:
		// the x offset was already paid by the caller.
		return m.openContainer(p, rightFrame{kind: z.openKind, id: z.open, root: z.openRoot, name: z.openName})
	}
	if z.task != "" {
		return m, toggleTodo(m.deps, z.task, z.taskDone)
	}
	if z.copy != "" {
		return m.notify(z.note, false), tea.SetClipboard(z.copy)
	}
	if z.react == "" {
		return m, openZone(m.deps, z)
	}
	x, ok := m.messageAt(p, rowAt(rows, line))
	if !ok {
		return m, nil
	}
	return m.toggleReaction(x, z.react)
}

// toggleTodo flips the task a todo's checkbox carries, the way the client
// does. The box is not drawn ahead of Feishu's answer — a box that moved and
// moved back would read as a press that did not land — so the rendering that
// displays the state comes back with the write instead.
func toggleTodo(d Deps, guid string, done bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := waited(reactTimeout)
		defer cancel()
		if err := d.Syncer.ToggleTodo(ctx, guid, !done); err != nil {
			d.Log.Error("toggle todo", "guid", guid, "err", err)
			return noticeMsg{"could not toggle the task: " + err.Error()}
		}
		return nil
	}
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
	// An overlay covers the panes, so the wheel scrolls what is on screen
	// rather than what the pointer would have been over.
	if m.config.open {
		// An open chooser owns the panel's cursor: moving it would hang the
		// list under another row, and enter would write that row instead. It
		// moves by one, the way the popup over the panes does.
		if m.config.project.open {
			m.projectPickScroll(cmp.Compare(step, 0))
			return m, nil
		}
		m.configScroll(step)
		return m, nil
	}
	if m.help.open {
		m.helpScroll(step)
		return m, nil
	}
	if m.mode == modeContextMenu {
		return m.walkContextMenu(cmp.Compare(step, 0)), nil
	}
	if m.floatAt(ms.X, ms.Y) {
		// A notch is three rows everywhere else, which over a list of eight
		// offers walks past most of them; the popup moves by one, the way the
		// keys that own it do.
		return m.walkFloat(cmp.Compare(step, 0)), nil
	}
	switch p {
	case paneChats:
		m.chatTop = clamp(m.chatTop+step, 0, max(0, len(m.visibleRows())-m.chatListHeight()))
	case paneMessages:
		m.msgTop = clamp(m.msgTop+step, 0, max(0, len(m.msgRows)-m.msgListHeight()))
		cmd := m.growMessages()
		return m, cmd
	case paneThread:
		// The column is shared, and the wheel scrolls whichever of the three
		// is drawn in it. The thread alone is scrolled by its own top, since
		// the cursor walking it is what move() shifts instead.
		if !m.scrollRight(step) {
			m.threadTop = clamp(m.threadTop+step, 0, max(0, len(m.threadRows)-m.listHeight()))
		}
	case paneInput:
		// The box without the keys has nothing to scroll: what it draws is the
		// draft it holds and the message it answers, neither of which moves.
		if s, ok := m.bandAt(ms.X); !ok || s != m.side {
			return m, nil
		}
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
			ta := m.areap()
			for range wheelStep {
				if step < 0 {
					ta.CursorUp()
				} else {
					ta.CursorDown()
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
	label := modeAbbr(m.mode)
	if m.mode == modeVisual {
		lo, hi := m.selectionRange()
		label += fmt.Sprintf(" %d", hi-lo+1)
	}
	syncIcon := m.syncGlyph()
	if syncIcon != "" {
		return fmt.Sprintf("%s · %s", label, syncIcon)
	}
	return label
}

func (m Model) syncGlyph() string {
	st := cmp.Or(m.syncStatus, "never_synced")
	switch st {
	case "running", "synced":
		return "●"
	case "needs_login":
		return "! auth"
	case "error":
		return "✗"
	case "syncing", "fetching":
		return "○"
	default:
		return "○"
	}
}

func modeAbbr(md mode) string {
	switch md {
	case modeInsert:
		return "I"
	case modeCommand:
		return ":"
	case modeFilter:
		return "F"
	case modeVisual:
		return "V"
	case modeEmoji:
		return "☺"
	case modeForward:
		return ">"
	case modeSearch:
		return "/"
	case modeTarget:
		return "O"
	case modeCandidates:
		return "D"
	case modeContextMenu:
		return "M"
	}
	return "N"
}

func modeLabel(md mode) string {
	return modeAbbr(md)
}

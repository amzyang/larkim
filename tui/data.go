package tui

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/fuzzy"
	"github.com/amzyang/larkim/internal/oplog"
	"github.com/amzyang/larkim/jev"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/markread"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/amzyang/larkim/todoist"
)

// Deps are the collaborators the TUI needs. Syncer pulls whatever the reader
// reaches for and is always set; Embedded says this process also holds the
// data-dir lock and runs the sweep, which is the one thing a second process
// may not do.
type Deps struct {
	Store    *store.Store
	Client   larkcli.Client
	Syncer   *sync.Syncer
	Self     string // the user's open_id
	Version  string
	Embedded bool
	// DataDir resolves stored attachment paths to absolute ones, and
	// ConfigPath goes into the follow-up command a copy ends with. Both are
	// optional: without them a copy keeps relative paths and omits --config.
	DataDir    string
	ConfigPath string
	// Term is what the terminal answered about itself, the capability store
	// the first frame is drawn from: the avatar renderer, the cell size, the
	// palette. Run probes it before tea owns the input; a zero Term is every
	// fallback, which is what a test or a terminal that said nothing wants.
	Term Handshake
	// AI is the assistant this run starts with; nil when the agent command is
	// not installed. NewAI builds it again when :config changes ai.agent or
	// ai.model, and returns nil when the new command is not found. A nil
	// NewAI leaves the assistant as it was, which is what a test that injects
	// one wants.
	AI    AIStreamer
	NewAI func(agent, model string) AIStreamer
	// Suggest ranks emoji against the message the reaction picker is open on;
	// nil when no API key is configured, which leaves the picker without its
	// contextual row. NewSuggest builds it again when :config changes
	// ai.jev_key_env or ai.jev_endpoint, and returns nil when the new pair
	// names no key.
	Suggest    ReactSuggester
	NewSuggest func(keyEnv, endpoint string) ReactSuggester
	// Todoist files the selected message or chat as a task and lists the
	// projects :config offers for todoist.project; nil when no token is
	// configured, which leaves the T key answering a notice instead of failing
	// a request that cannot land. NewTodoist builds it again when :config
	// changes the token or the project, and returns nil for an empty token. A
	// nil NewTodoist leaves it as it was, which is what a test that injects a
	// fake wants.
	Todoist    TodoistClient
	NewTodoist func(token, project string) TodoistClient
	// Nudge signals that the store changed, so the watch checks without
	// waiting out its interval. It carries this process's own writes, the
	// sweep's too when the sweep runs here; a daemon's land on the interval.
	Nudge <-chan struct{}
	// OpenURL hands links, applinks and local files to the desktop, which comes
	// forward. New fills it when nil; tests replace it to keep the real `open`
	// out of the run. Several targets are opened together rather than one by one.
	OpenURL func(ctx context.Context, targets []string) error
	// ClearBadge drops the Feishu client's own red dot for one chat. New fills
	// it when nil; tests replace it to keep the gateway out of the run.
	//
	// OpenURL is what `o` does; ClearBadge never touches the desktop.
	ClearBadge markread.Clear
	// NewClearBadge builds ClearBadge again when :set changes mark_read.browser.
	// New
	// fills it with markread.New when both are nil; a test that injects
	// ClearBadge leaves it nil, so a :set keeps the fake it was given.
	NewClearBadge func(config.MarkRead) markread.Clear
	// Config is the configuration this run loaded. :set retunes a key of it
	// for the session and :config writes one back to the file.
	Config config.Config
	// Env reads the environment the external editor is named in. New fills it
	// when nil; tests replace it to keep a real editor out of the run.
	Env func(string) string
	// Clipboard reports what the system clipboard holds, staging an image
	// into stageDir. New fills it when nil; tests replace it to keep
	// osascript and the machine's own clipboard out of the run.
	Clipboard func(stageDir string) (clip, error)
	// Fetch downloads a remote image a draft names, returning the bytes and
	// the response content type. New fills it with sync.HTTPFetcher; tests
	// replace it to keep the network out of the run.
	Fetch func(ctx context.Context, url string) ([]byte, string, error)
	// Log records what the TUI cannot show. stderr is the alternate screen
	// here, so anything worth knowing has to reach the log file or be lost.
	// New fills it with a discard logger when nil.
	Log *slog.Logger
}

// AIStreamer is the pair of calls the assistant pane makes. They are named
// here rather than taken as *ai.Client because every chunk of them crosses
// the network, and a pane nobody can drive is a pane nobody can test.
type AIStreamer interface {
	Stream(ctx context.Context, transcript, prompt string) <-chan ai.Chunk
	// StreamChat is Stream for an answer whose whole output is the message,
	// posted to the chat as it stands.
	StreamChat(ctx context.Context, transcript, prompt string) <-chan ai.Chunk
	// StreamHistory is Stream with the agent allowed to read the chat's
	// synced history itself, gated to the read-only commands the prompt
	// teaches.
	StreamHistory(ctx context.Context, transcript, prompt string, h ai.History) <-chan ai.Chunk
}

// ReactSuggester is the one call the picker's contextual row makes. It is
// named here rather than taken as *jev.Client for the same reason: the call
// crosses the network, and a row nobody can drive is a row nobody can test.
type ReactSuggester interface {
	Rank(ctx context.Context, ask jev.Ask) (jev.Rank, error)
}

// TodoistClient is the call the T key makes and the listing the project
// chooser draws from, named here for the same reason as ReactSuggester: both
// cross the network, and a key nobody can drive is a key nobody can test.
type TodoistClient interface {
	CreateTask(ctx context.Context, task todoist.Task) (todoist.Task, error)
	Projects(ctx context.Context) ([]todoist.Project, error)
}

// discardLog stands in for a Deps built by hand — in a test — which has no
// logger of its own.
var discardLog = slog.New(slog.DiscardHandler)

// log is where a load that failed goes. A failed load degrades the pane rather
// than the page, so nothing about it reaches the screen.
func (d Deps) log() *slog.Logger { return cmp.Or(d.Log, discardLog) }

const (
	messagePageSize  = 200
	anchoredPageSize = 2000 // from a search hit onwards
	threadPageSize   = 500
	// threadTailSize is how many of a thread's newest replies stand under its
	// root in the flow, the client's own count. What is left over is named on
	// the line heading them rather than drawn.
	threadTailSize = 5
	// watchEvery is a fallback: an embedded syncer nudges the watch as soon
	// as a tick ends, and against a daemon this interval is the only signal
	// there is.
	watchEvery  = time.Second
	statusEvery = 5 * time.Second
	sendTimeout = 60 * time.Second
)

// Messages flowing back into Update.
type (
	chatsLoadedMsg struct {
		chats []store.Chat
		// threads are the reader's own conversations inside those chats,
		// which stand in the list beside them.
		threads []store.ThreadFeed
		unread  map[string]int64
		// drafts is every chat's unsent composer state, fetched with the list
		// rather than per row so the marker costs one query a refresh.
		drafts map[string]store.Draft
		// frameDrafts is the same for the thread rows, keyed by thread.
		frameDrafts map[string]store.Draft
		// cands counts each chat's pending lark-watch drafts, for the marker
		// beside the drafts one.
		cands map[string]int
	}
	messagesLoadedMsg struct {
		chatID string
		msgs   []store.Message
		meta   msgMeta
		// draft rides with the page so the composer fills at the same moment
		// the panes swap over, rather than a frame later. It is applied only
		// when this page is the one entering the chat — a reload must not
		// stomp what is being typed.
		draft store.Draft
		// roster is who is in the chat, for @ completion and for turning the
		// names it inserted into tags on the way out.
		roster []store.Contact
		// cands are the lark-watch reply drafts mirrored for this chat, drawn
		// under each message's reactions on the page.
		cands []store.Candidate
	}
	candidatesLoadedMsg struct {
		rows []store.Candidate
	}
	// candidateClearedMsg closes a fire-and-forget mirror clear. Nothing acts
	// on it; the badge goes with the revision bump.
	candidateClearedMsg struct{}
	// draftSavedMsg closes a fire-and-forget draft write and lands what was
	// written: the row beside the box just saved redraws with it rather than
	// waiting for the next listing, which no revision bump will bring — the
	// drafts table sits outside data_rev.
	draftSavedMsg struct {
		draft store.Draft
		err   error
	}
	threadLoadedMsg struct {
		threadID string
		msgs     []store.Message
		meta     msgMeta
		// draft is the box the column carries, which belongs to this frame
		// rather than to the chat. See rightDraftPin.
		draft store.Draft
	}
	// replyLoadedMsg carries a reply tree: the message the conversation
	// started from and every live answer under it.
	replyLoadedMsg struct {
		root  string
		msgs  []store.Message
		meta  msgMeta
		draft store.Draft
	}
	// revMsg says the store changed, not what changed: the revision is a
	// counter, so every pane reloads.
	revMsg struct{}
	// sentMsg answers one send. localID names the outbox item it belongs to,
	// empty for a send that never put a bubble on screen.
	sentMsg struct {
		localID   string
		messageID string
		// keys are the image keys this attempt uploaded, in the order the
		// draft names them. They come back even when the send then failed,
		// so a retry is one more send rather than one more upload.
		keys []string
		err  error
		// ingestErr is the store write that follows a send Feishu took. The
		// message is out either way; this only says the row is not local yet.
		ingestErr error
	}
	// pastedMsg answers a read of the clipboard.
	pastedMsg struct {
		clip clip
		err  error
	}
	selfNameMsg   struct{ name string }
	syncStatusMsg struct{ status, lastError string }
	errMsg        struct{ err error }
	noticeMsg     struct{ text string }
	aiChunkMsg    struct {
		turn  string
		chunk ai.Chunk
	}
	searchMsg struct {
		gen  int
		hits []searchHit
		meta msgMeta
	}
)

// msgMeta is the per-message detail the store keeps outside the messages
// table: the sender's account suffix, which tells same-named colleagues
// apart, the files a message's attachments were downloaded to, and the
// messages this page replies to, which are often older than the page.
type msgMeta struct {
	suffix map[string]string
	people map[string]string
	// avatars are the senders' downloaded pictures, by open id, relative to
	// the data dir. A contact with none is absent rather than empty.
	avatars map[string]string
	res     map[string][]store.Resource
	// imgText is the writing read out of each message's pictures, by message
	// id. Only the assistant and the reaction suggester read it; nothing on
	// screen is drawn from it.
	imgText map[string][]string
	// docs names the Feishu documents linked to from any message, by
	// store.DocRef.Key. It is not scoped to this page: a personal archive
	// holds these in the thousands at most, and working out which links the
	// rows happen to spell costs more than reading them all.
	docs    map[string]store.DocLabel
	parents map[string]store.Message
	// forwards is the collapsed line of each merged forward on the page, by
	// the bundle's message id, and threads the same for each thread rooted
	// on it, by thread id.
	forwards map[string]store.ForwardGist
	threads  map[string]store.ThreadGist
	// replies names the reply tree each message on the page belongs to, by
	// message id. A message nobody answered, in a tree nobody answered, is
	// absent.
	replies map[string]store.ReplyGist
}

// style is the part of a render context the page's own lookups answer for.
// The caller fills in the pane it is drawing to.
func (meta msgMeta) style() msgStyle {
	return msgStyle{suffix: meta.suffix, people: meta.people, avatars: meta.avatars,
		res: meta.res, docs: meta.docs, parents: meta.parents, forwards: meta.forwards,
		threads: meta.threads, replies: meta.replies}
}

func loadMeta(ctx context.Context, st *store.Store, self string, msgs []store.Message) (msgMeta, error) {
	msgIDs := make([]string, 0, len(msgs))
	ids := make([]string, 0, len(msgs))
	var parentIDs []string
	seen := map[string]bool{}
	addPerson := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	var bundles, threads []string
	for _, x := range msgs {
		msgIDs = append(msgIDs, x.MessageID)
		addPerson(x.SenderID)
		if x.MsgType == "merge_forward" {
			bundles = append(bundles, x.MessageID)
		}
		if x.ThreadID != "" && x.MessagePosition >= 0 {
			threads = append(threads, x.ThreadID)
		}
		// A reaction names its operator by open id alone, so the reactors a
		// chip lists travel with the senders to the contacts lookup.
		for _, c := range emoji.Summary(x.ReactionsJSON, "") {
			for _, id := range c.Operators {
				addPerson(id)
			}
		}
		if x.ReplyTo != "" {
			parentIDs = append(parentIDs, x.ReplyTo)
		}
	}
	parents, err := st.MessagesByIDs(ctx, parentIDs)
	if err != nil {
		return msgMeta{}, err
	}
	// A quoted message names its sender the way the list does, so its author
	// needs a suffix too even when they never spoke on this page.
	for _, p := range parents {
		addPerson(p.SenderID)
	}
	forwards, err := st.ForwardGists(ctx, bundles)
	if err != nil {
		return msgMeta{}, err
	}
	gists, err := st.ThreadGists(ctx, threads, self, threadTailSize)
	if err != nil {
		return msgMeta{}, err
	}
	// A summary line names somebody the page itself may never have heard from:
	// the tail of a collapsed thread, and every child a forward previews.
	// Both are gathered before the lookup, because the lookup is what gives
	// them a suffix and a picture.
	for _, g := range gists {
		for _, r := range g.Tail {
			addPerson(r.SenderID)
		}
	}
	for _, f := range forwards {
		for _, c := range f.Preview {
			addPerson(c.SenderID)
		}
	}
	contacts, err := st.ContactsByIDs(ctx, ids)
	if err != nil {
		return msgMeta{}, err
	}
	suffix := make(map[string]string, len(contacts))
	people := make(map[string]string, len(contacts))
	avatars := make(map[string]string, len(contacts))
	for id, c := range contacts {
		if s := c.AccountSuffix(); s != "" {
			suffix[id] = s
		}
		if c.Name != "" {
			people[id] = c.Name
		}
		if f := c.AvatarFile(); f != "" {
			avatars[id] = f
		}
	}
	// A bot that reacts is named by its app id, which no contact carries.
	// One whose name the tenant withholds is still known to be a bot, and
	// says so rather than joining the strangers counted in a chip's +N.
	apps, err := st.AppNames(ctx, ids)
	if err != nil {
		return msgMeta{}, err
	}
	for id, name := range apps {
		people[id] = cmp.Or(name, "Bot")
	}
	res, err := st.ResourcesForMessages(ctx, msgIDs)
	if err != nil {
		return msgMeta{}, err
	}
	imgText, err := st.ImageTextsFor(ctx, msgIDs)
	if err != nil {
		return msgMeta{}, err
	}
	docs, err := st.DocLabels(ctx)
	if err != nil {
		return msgMeta{}, err
	}
	replies, err := st.ReplyGists(ctx, msgIDs)
	if err != nil {
		return msgMeta{}, err
	}
	return msgMeta{suffix: suffix, people: people, avatars: avatars, res: res, imgText: imgText, docs: docs,
		parents: parents, forwards: forwards, threads: gists, replies: replies}, nil
}

// searchLimits bound each group. Messages get the most because they are what
// a search is usually for; the other two are there to be recognised, not
// scrolled.
const (
	searchMsgLimit    = 200
	searchChatLimit   = 20
	searchPeopleLimit = 20
	searchRemoteLimit = 50
)

// localSearch answers a query from the store alone: the reader is still
// typing, so the answer has to come back inside a keystroke.
func localSearch(d Deps, chats []store.Chat, query string, gen int) tea.Cmd {
	return func() tea.Msg {
		if strings.TrimSpace(query) == "" {
			return searchMsg{gen: gen}
		}
		ctx := context.Background()
		msgs, err := d.Store.SearchMessages(ctx, query, "", searchMsgLimit)
		if err != nil {
			return errMsg{err}
		}
		meta, err := loadMeta(ctx, d.Store, d.Self, msgs)
		if err != nil {
			return errMsg{err}
		}
		hits := make([]searchHit, 0, len(msgs)+searchChatLimit+searchPeopleLimit)
		for _, msg := range msgs {
			hits = append(hits, searchHit{kind: hitMessage, msg: msg})
		}
		// The index is built here rather than carried on the model: this runs
		// off the update loop, and the matcher's scratch space is not shared.
		ix := fuzzy.NewIndex()
		for _, c := range chats {
			if len(hits)-len(msgs) == searchChatLimit {
				break
			}
			if mark, ok := ix.Match(c.ChatID, flatten(c.Name), query); ok {
				hits = append(hits, searchHit{kind: hitChat, chat: c, mark: mark})
			}
		}
		people, err := d.Store.ListContacts(ctx, 0)
		if err != nil {
			return errMsg{err}
		}
		n := 0
		for _, c := range people {
			if n == searchPeopleLimit {
				break
			}
			mark, ok := ix.Match(c.OpenID, c.Name, query)
			if !ok && !strings.Contains(strings.ToLower(c.Email), strings.ToLower(query)) {
				continue
			}
			hits = append(hits, searchHit{kind: hitPerson, mark: mark, user: larkcli.User{
				OpenID: c.OpenID, Name: c.Name, Email: c.Email,
				Department: c.Department, P2PChatID: c.P2PChatID}})
			n++
		}
		return searchMsg{gen: gen, hits: hits, meta: meta}
	}
}

// waitForAI reads one piece of the answer owed to turn. A chunk naming a turn
// the panel no longer holds is dropped by the handler, which is what retires a
// stream whose session went away.
func waitForAI(turn string, ch <-chan ai.Chunk) tea.Cmd {
	return func() tea.Msg {
		c, ok := <-ch
		if !ok {
			return aiChunkMsg{turn: turn, chunk: ai.Chunk{Done: true}}
		}
		return aiChunkMsg{turn: turn, chunk: c}
	}
}

// loadChats reads the sidebar. The badge counts ride on the rows they belong
// to, so a chat's number and its place can never come from two different
// revisions of the database; the threads are a second query because they are
// rows of their own rather than a column of somebody else's.
func loadChats(d Deps) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		chats, err := d.Store.ListChats(ctx, store.ChatQuery{Self: d.Self})
		if err != nil {
			return errMsg{err}
		}
		unread := make(map[string]int64, len(chats))
		for _, c := range chats {
			if c.UnreadCount > 0 {
				unread[c.ChatID] = c.UnreadCount
			}
		}
		threads, err := d.Store.ListThreadFeed(ctx, store.ThreadFeedQuery{Self: d.Self})
		if err != nil {
			return errMsg{err}
		}
		// A draft the store cannot answer for costs the list its marker, not
		// its rows: the chats are what the reader asked for.
		drafts, err := d.Store.Drafts(ctx)
		if err != nil {
			d.log().Error("load drafts", "err", err)
			drafts = nil
		}
		frameDrafts, err := d.Store.FrameDrafts(ctx)
		if err != nil {
			d.log().Error("load frame drafts", "err", err)
			frameDrafts = nil
		}
		// A chat whose mirror count cannot be answered for keeps its rows and
		// loses only the marker, the same trade the drafts make.
		cands, err := d.Store.CandidateChats(ctx)
		if err != nil {
			d.log().Error("load candidates", "err", err)
			cands = nil
		}
		return chatsLoadedMsg{chats: chats, threads: threads, unread: unread,
			drafts: drafts, frameDrafts: frameDrafts, cands: cands}
	}
}

// messageQuery is the newest limit messages of a chat or, anchored at sinceMs,
// every message from that time on, so a search hit older than the page is
// included. Scrolling to the top raises limit — see growMessages.
func messageQuery(chatID string, sinceMs int64, limit int) store.MessageQuery {
	// Replies are folded into their root's own line, so the page neither
	// draws them nor spends its limit on them. A recall is not folded: the
	// client leaves a notice where the message stood, so the row comes along
	// and renderRows turns it into one.
	if sinceMs > 0 {
		return store.MessageQuery{ChatID: chatID, SinceMs: sinceMs, Limit: limit,
			ExcludeThreadReplies: true, IncludeDeleted: true}
	}
	return store.MessageQuery{ChatID: chatID, Desc: true, Limit: limit,
		ExcludeThreadReplies: true, IncludeDeleted: true}
}

func loadMessages(d Deps, chatID string, sinceMs int64, limit int) tea.Cmd {
	q := messageQuery(chatID, sinceMs, limit)
	return func() tea.Msg {
		ctx := context.Background()
		rows, err := d.Store.ListMessages(ctx, q)
		if err != nil {
			return errMsg{err}
		}
		if q.Desc {
			slices.Reverse(rows)
		}
		meta, err := loadMeta(ctx, d.Store, d.Self, rows)
		if err != nil {
			return errMsg{err}
		}
		draft, roster := chatSide(ctx, d, chatID)
		cands, err := d.Store.ChatCandidates(ctx, chatID, d.Self)
		if err != nil {
			d.log().Error("load message candidates", "chat_id", chatID, "err", err)
			cands = nil
		}
		return messagesLoadedMsg{chatID: chatID, msgs: rows, meta: meta, draft: draft, roster: roster, cands: cands}
	}
}

// chatSide is what the composer needs to answer a chat: the draft written into
// it and who it reaches. Either one the store cannot answer for costs the
// composer its text or its candidates, not the reader their page.
func chatSide(ctx context.Context, d Deps, chatID string) (store.Draft, []store.Contact) {
	draft, err := d.Store.LoadDraft(ctx, chatID, "")
	if err != nil {
		d.log().Error("load draft", "chat_id", chatID, "err", err)
		draft = store.Draft{ChatID: chatID}
	}
	roster, err := d.Store.ChatRoster(ctx, chatID, d.Self)
	if err != nil {
		d.log().Error("load roster", "chat_id", chatID, "err", err)
		roster = nil
	}
	return draft, roster
}

// threadQuery is a thread's whole reply list, root included. A recalled reply
// keeps its slot so the rows and the thread's reply count agree.
func threadQuery(threadID string) store.MessageQuery {
	return store.MessageQuery{ThreadID: threadID, Limit: threadPageSize, IncludeDeleted: true}
}

func loadThread(d Deps, chatID, threadID string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		rows, err := d.Store.ListMessages(ctx, threadQuery(threadID))
		if err != nil {
			return errMsg{err}
		}
		meta, err := loadMeta(ctx, d.Store, d.Self, rows)
		if err != nil {
			return errMsg{err}
		}
		return threadLoadedMsg{threadID: threadID, msgs: rows, meta: meta,
			draft: frameDraft(d, chatID, threadID)}
	}
}

// frameDraft is the box a frame carries. A draft the store cannot answer for
// costs the box its text, not the reader their frame.
func frameDraft(d Deps, chatID, frameID string) store.Draft {
	draft, err := d.Store.LoadDraft(context.Background(), chatID, frameID)
	if err != nil {
		d.log().Error("load draft", "chat_id", chatID, "frame_id", frameID, "err", err)
		return store.Draft{ChatID: chatID, FrameID: frameID}
	}
	return draft
}

// loadReplies fetches a reply tree for the right column. The rows are
// ordinary messages of the open chat, already synced with it, so nothing is
// asked of Feishu here.
func loadReplies(d Deps, chatID, rootID string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		rows, err := d.Store.ReplyTree(ctx, rootID)
		if err != nil {
			return errMsg{err}
		}
		meta, err := loadMeta(ctx, d.Store, d.Self, rows)
		if err != nil {
			return errMsg{err}
		}
		return replyLoadedMsg{root: rootID, msgs: rows, meta: meta,
			draft: frameDraft(d, chatID, rootID)}
	}
}

func waitForRev(ch <-chan int64) tea.Cmd {
	return func() tea.Msg {
		if _, ok := <-ch; !ok {
			return nil
		}
		return revMsg{}
	}
}

func syncStatus(st *store.Store) tea.Msg {
	ctx := context.Background()
	status, _, _ := st.GetState(ctx, sync.KeyStatus)
	lastErr, _, _ := st.GetState(ctx, sync.KeyLastError)
	return syncStatusMsg{status: status, lastError: lastErr}
}

func pollSyncStatus(st *store.Store) tea.Cmd {
	return tea.Tick(statusEvery, func(time.Time) tea.Msg { return syncStatus(st) })
}

func readSyncStatus(st *store.Store) tea.Cmd {
	return func() tea.Msg { return syncStatus(st) }
}

// syncTickTimeout bounds the one sweep a reader can ask for by hand. It is
// far longer than any single call because a tick is many of them, and it
// exists so a stalled gateway cannot hold the background lane for good.
const syncTickTimeout = 5 * time.Minute

// begin starts the operation a Cmd carries out. Every call the Cmd makes takes
// its ctx from the one begin returned, so a send's uploads, the send and the
// ingest after it are one op_id in the log rather than four.
func begin(name string) context.Context {
	return oplog.With(context.Background(), name)
}

// waited builds the context for a lark-cli call somebody pressed a key for,
// inside the operation op. It takes the interactive lane, so a send or a
// reaction does not queue behind the syncer's sweeps or the open chat's beat —
// both run every few seconds, so a shared line would be occupied more often
// than not.
func waited(op context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(larkcli.WithLane(op, larkcli.LaneInteractive), d)
}

// beat builds the context for a timer-driven refresh of what is already on
// screen. Nobody pressed a key for it, so it takes a lane of its own: sharing
// the interactive line, the 1.5s beat's two calls held two of its three slots
// and a send landed behind them.
func beat(op context.Context, d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(larkcli.WithLane(op, larkcli.LaneBeat), d)
}

// sendMsg hands a chat one message. localID doubles as the idempotency key,
// so a retry under the same id is the send Feishu already knows about rather
// than a second delivery.
func sendMsg(d Deps, localID string, target larkcli.Target, msg larkcli.Outgoing, imgs []draftImage, file draftFile, done []string) tea.Cmd {
	return func() tea.Msg {
		op := begin("send")
		msg, keys, err := uploadDraft(op, d, msg, imgs, file, done)
		if err != nil {
			return sentMsg{localID: localID, keys: keys, err: err}
		}
		ctx, cancel := waited(op, sendTimeout)
		defer cancel()
		sent, err := d.Client.Send(ctx, target, msg, localID)
		return stored(op, d, localID, keys, sent, err)
	}
}

func replyMsg(d Deps, localID, messageID string, msg larkcli.Outgoing, inThread bool, imgs []draftImage, file draftFile, done []string) tea.Cmd {
	return func() tea.Msg {
		op := begin("reply")
		msg, keys, err := uploadDraft(op, d, msg, imgs, file, done)
		if err != nil {
			return sentMsg{localID: localID, keys: keys, err: err}
		}
		ctx, cancel := waited(op, sendTimeout)
		defer cancel()
		sent, err := d.Client.Reply(ctx, messageID, msg, inThread, localID)
		return stored(op, d, localID, keys, sent, err)
	}
}

// stored puts a message Feishu just took into the store before the send is
// answered, so the row the panes draw comes from the store like every other
// and the bubble can hand over to it on the reload that write causes.
func stored(op context.Context, d Deps, localID string, keys []string, sent larkcli.SentMessage, err error) sentMsg {
	out := sentMsg{localID: localID, messageID: sent.MessageID, keys: keys, err: err}
	if err != nil {
		return out
	}
	ctx, cancel := waited(op, sendTimeout)
	defer cancel()
	out.ingestErr = d.Syncer.IngestSent(ctx, sent)
	return out
}

// uploadImages puts a draft's files on Feishu and swaps the placeholders in
// the body for the keys that came back. done carries the keys an earlier
// attempt already uploaded, so a retry is one more send rather than one more
// upload leaving an orphan key behind. Uploads run one at a time because
// lark-cli calls are serialised anyway, and each gets its own deadline rather
// than sharing one budget with the send that follows.
func uploadDraft(op context.Context, d Deps, msg larkcli.Outgoing, imgs []draftImage, file draftFile, done []string) (larkcli.Outgoing, []string, error) {
	// An attachment and a set of pictures never arrive together: Feishu
	// carries a file as a message of its own.
	if file.local != "" {
		if len(done) > 0 {
			msg.FileKey = done[0]
			return msg, done, nil
		}
		ctx, cancel := waited(op, sendTimeout)
		defer cancel()
		key, err := d.Client.UploadFile(ctx, file.local)
		if err != nil {
			return msg, nil, fmt.Errorf("upload %s: %w", filepath.Base(file.local), err)
		}
		keepSent(op, d, key, "file", file.local)
		msg.FileKey = key
		return msg, []string{key}, nil
	}
	if len(imgs) == 0 {
		return msg, nil, nil
	}
	keys := make([]string, 0, len(imgs))
	for i, img := range imgs {
		key := img.key
		switch {
		case i < len(done):
			key = done[i]
		case img.local != "":
			up, err := uploadOne(op, d, img.local)
			if err != nil {
				return msg, keys, err
			}
			keepSent(op, d, up, "image", img.local)
			key = up
		case img.url != "":
			path, err := fetchRemote(op, d, img.url)
			if err != nil {
				return msg, keys, err
			}
			up, err := uploadOne(op, d, path)
			if err == nil {
				keepSent(op, d, up, "image", path)
			}
			os.Remove(path)
			if err != nil {
				return msg, keys, err
			}
			key = up
		}
		keys = append(keys, key)
		msg.Markdown = strings.Replace(msg.Markdown, img.key, key, 1)
		if msg.ImageKey == img.key {
			msg.ImageKey = key
		}
	}
	return msg, keys, nil
}

// keepSent files what was just uploaded under its key. A failure costs only
// the download the message would have had anyway, so the send goes on.
func keepSent(op context.Context, d Deps, key, typ, path string) {
	if err := d.Syncer.KeepSent(op, key, typ, path); err != nil {
		d.Log.WarnContext(op, "keep sent file", "key", key, "err", err)
	}
}

// uploadOne puts one file on Feishu under a deadline of its own, rather than
// sharing one budget with the send and every other image behind it.
func uploadOne(op context.Context, d Deps, path string) (string, error) {
	ctx, cancel := waited(op, sendTimeout)
	defer cancel()
	key, err := d.Client.UploadImage(ctx, path)
	if err != nil {
		return "", fmt.Errorf("upload %s: %w", filepath.Base(path), err)
	}
	return key, nil
}

// fetchRemote downloads an image a draft named by URL under a deadline of its
// own, the way each upload beside it has one. The lane waited takes is
// lark-cli's, and this call reaches a CDN rather than lark-cli.
func fetchRemote(op context.Context, d Deps, url string) (string, error) {
	ctx, cancel := context.WithTimeout(op, sendTimeout)
	defer cancel()
	return sync.FetchToTemp(ctx, d.Fetch, url)
}

// pasteClipboard reads the clipboard off the Update loop: the osascript round
// trip takes long enough to be seen as a stutter if it ran inline.
func pasteClipboard(d Deps) tea.Cmd {
	return func() tea.Msg {
		c, err := d.Clipboard(filepath.Join(d.DataDir, pastedDir))
		return pastedMsg{clip: c, err: err}
	}
}

// ingestMessage pulls one message back from Feishu into the store, so what a
// send, a recall or a forward just did shows up without waiting for a tick.
func ingestMessage(op context.Context, d Deps, messageID string) error {
	ctx, cancel := waited(op, sendTimeout)
	defer cancel()
	return d.Syncer.IngestIDs(ctx, []string{messageID})
}

// selfNameOf is how the account this process signed in as spells its name. It
// is what a pending send is attributed to until Feishu answers with a real
// message, and what a forward the reader sent is titled by.
func selfNameOf(ctx context.Context, d Deps) (string, error) {
	if d.Self == "" {
		return "", nil
	}
	contacts, err := d.Store.ContactsByIDs(ctx, []string{d.Self})
	if err != nil {
		return "", err
	}
	return contacts[d.Self].Name, nil
}

// loadSelfName is that lookup as the panes make it, where a name that cannot
// be read is worth a log line rather than an error the reader has to see.
func loadSelfName(d Deps) tea.Cmd {
	if d.Self == "" {
		return nil
	}
	return func() tea.Msg {
		name, err := selfNameOf(context.Background(), d)
		if err != nil {
			d.Log.Warn("load self name", "open_id", d.Self, "err", err)
			return nil
		}
		return selfNameMsg{name: name}
	}
}

// env reads the environment, falling back to the process's own where no
// reader was injected. Tests replace it to keep the machine out of the run.
func (d Deps) env() func(string) string {
	if d.Env == nil {
		return os.Getenv
	}
	return d.Env
}

// openInFeishu opens a chat (optionally at a message position) in the desktop client.
func openInFeishu(d Deps, chatID, messageID string, position int64) tea.Cmd {
	return func() tea.Msg {
		ctx := begin("open")
		if err := d.OpenURL(ctx, []string{applink.ChatLink(chatID, messageID, position)}); err != nil {
			// The notice bar holds the message and is gone at the next
			// keypress; which chat was asked for only exists here.
			d.Log.ErrorContext(ctx, "open in feishu", "chat_id", chatID, "position", position, "err", err)
			return errMsg{err}
		}
		return noticeMsg{"opened in Feishu"}
	}
}

// openZone hands over what a row's target points at: a meeting to join, a
// link, or an attachment's own file on this machine. It takes the screen,
// which is what the keypress or the click asked for.
func openZone(d Deps, z clickZone) tea.Cmd {
	return func() tea.Msg {
		if len(z.urls) == 0 {
			return nil
		}
		ctx := begin("open")
		if err := d.OpenURL(ctx, z.urls); err != nil {
			// The notice bar has room for the message but not for what was
			// handed over, and a zone carries as many targets as the message
			// had attachments.
			d.Log.ErrorContext(ctx, "open zone", "targets", z.urls, "err", err)
			return errMsg{err}
		}
		return noticeMsg{z.note}
	}
}

// remoteRestDelay is the pause before Feishu is asked. It is longer than the
// store's: a remote search costs a subprocess and a round trip, so it waits
// until the reader has stopped typing rather than merely paused.
const remoteRestDelay = 300 * time.Millisecond

// remoteMinQuery is the shortest query worth a round trip. One character
// matches most of the archive and tells the reader nothing.
const remoteMinQuery = 2

// remoteSearchMsg carries what Feishu answered. gen names the query it was
// asked for, so an answer that arrives under a newer one is dropped.
type remoteSearchMsg struct {
	gen  int
	hits []searchHit
	err  error
}

// remoteSearch asks Feishu for messages this machine has never synced. Hits
// already in the store are dropped: they are in the local group already, and
// showing them twice would say the archive is larger than it is.
func remoteSearch(ctx context.Context, d Deps, query string, gen int) tea.Cmd {
	return func() tea.Msg {
		found, err := d.Client.SearchMessages(ctx, query, searchRemoteLimit)
		if err != nil {
			return remoteSearchMsg{gen: gen, err: err}
		}
		ids := make([]string, 0, len(found))
		for _, h := range found {
			ids = append(ids, h.MessageID)
		}
		cold, err := d.Store.UnknownMessageIDs(ctx, ids)
		if err != nil {
			return remoteSearchMsg{gen: gen, err: err}
		}
		if len(cold) == 0 {
			return remoteSearchMsg{gen: gen}
		}
		rendered, err := d.Client.MGetRendered(ctx, cold)
		if err != nil {
			return remoteSearchMsg{gen: gen, err: err}
		}
		return remoteSearchMsg{gen: gen, hits: coldHits(ctx, d, found, rendered)}
	}
}

// coldHits pairs the search metadata, which dates a message, with the
// rendered text, which says what it holds. Sender names come from the local
// contacts: a search hit names its sender by id only.
func coldHits(ctx context.Context, d Deps, found []larkcli.SearchHit, rendered []larkcli.RenderedMessage) []searchHit {
	meta := make(map[string]larkcli.SearchHit, len(found))
	senders := make([]string, 0, len(found))
	for _, h := range found {
		meta[h.MessageID] = h
		senders = append(senders, h.FromID)
	}
	people, err := d.Store.ContactsByIDs(ctx, senders)
	if err != nil {
		// The hits still carry their text and their date; only the names
		// beside them are lost, which is not worth discarding a search for.
		d.Log.WarnContext(ctx, "name search hit senders", "senders", len(senders), "err", err)
	}
	hits := make([]searchHit, 0, len(rendered))
	for _, r := range rendered {
		h, ok := meta[r.MessageID]
		if !ok {
			continue
		}
		hits = append(hits, searchHit{kind: hitMessage, remote: true, msg: store.Message{
			MessageID: r.MessageID, ChatID: r.ChatID, MsgType: r.MsgType, Content: r.Content,
			SenderID: h.FromID, SenderName: people[h.FromID].Name, ThreadID: h.ThreadID,
			MessagePosition: h.Position, CreateMs: h.CreateTime.UnixMilli(),
			UpdateMs: h.CreateTime.UnixMilli(), RenderedAt: 1,
		}})
	}
	slices.SortFunc(hits, func(a, b searchHit) int { return cmp.Compare(b.msg.CreateMs, a.msg.CreateMs) })
	return hits
}

// ingestThenOpen pulls a message this machine has not stored — one Feishu
// search turned up, or one a quote names — so the page that opens has it like
// any other. The chat and time that cut the page are read back from the store
// rather than from whatever named the message, because a quote names nothing
// but an id.
func ingestThenOpen(d Deps, messageID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := waited(begin("open-message"), sendTimeout)
		defer cancel()
		if err := d.Syncer.IngestIDs(ctx, []string{messageID}); err != nil {
			return errMsg{err}
		}
		x, err := d.Store.GetMessage(ctx, messageID)
		if err != nil {
			return errMsg{err}
		}
		return openHitMsg{chatID: x.ChatID, messageID: x.MessageID, threadID: threadOf(x), sinceMs: x.CreateMs}
	}
}

// openHitMsg lands a cold hit once it is in the store. threadID is set when
// the hit is a reply folded out of its chat's flow, which is reached through
// the thread rather than on the page.
type openHitMsg struct {
	chatID, messageID, threadID string
	sinceMs                     int64
}

// threadOf names the thread a message is only reachable through.
func threadOf(x store.Message) string {
	if x.MessagePosition < 0 {
		return x.ThreadID
	}
	return ""
}

// saveDraft writes one box's state under the chat and frame it was typed in.
// It is fire-and-forget: a draft is a convenience, and a chat switch must not
// wait on the disk. A failure is logged rather than shown, since the reader is
// already looking at the next chat by the time it could be.
func saveDraft(d Deps, draft store.Draft) tea.Cmd {
	if draft.ChatID == "" {
		return nil
	}
	return func() tea.Msg {
		err := d.Store.SaveDraft(context.Background(), draft, time.Now().UnixMilli())
		if err != nil {
			d.log().Error("save draft", "chat_id", draft.ChatID, "frame_id", draft.FrameID, "err", err)
		}
		return draftSavedMsg{draft: draft, err: err}
	}
}

package tui

import (
	"context"
	"os/exec"
	"slices"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
)

// Deps are the collaborators the TUI needs. Syncer is non-nil when this
// process holds the data-dir lock and syncs in-process.
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
	// AI is the assistant; nil when no API key is configured.
	AI        *ai.Client
	AIContext int // recent messages handed to the assistant
	// Nudge signals that the store changed, so the watch checks without
	// waiting out its interval. Only an embedded syncer can reach this
	// process; against a daemon it is nil and the interval is all there is.
	Nudge <-chan struct{}
}

const (
	messagePageSize  = 200
	anchoredPageSize = 2000 // from a search hit onwards
	threadPageSize   = 500
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
		chats  []store.Chat
		unread map[string]int64
	}
	messagesLoadedMsg struct {
		chatID string
		msgs   []store.Message
		meta   msgMeta
	}
	threadLoadedMsg struct {
		threadID string
		msgs     []store.Message
		meta     msgMeta
	}
	// revMsg says the store changed, not what changed: the revision is a
	// counter, so every pane reloads.
	revMsg struct{}
	// sentMsg answers one send. localID names the outbox item it belongs to,
	// empty for a send that never put a bubble on screen.
	sentMsg struct {
		localID   string
		messageID string
		err       error
	}
	// ingestedMsg answers the fetch that follows a send, which is what puts
	// the real row in the store.
	ingestedMsg struct {
		localID string
		err     error
	}
	selfNameMsg   struct{ name string }
	syncStatusMsg struct{ status, lastError string }
	errMsg        struct{ err error }
	noticeMsg     struct{ text string }
	aiChunkMsg    struct{ chunk ai.Chunk }
	searchMsg     struct {
		query string
		msgs  []store.Message
		meta  msgMeta
	}
)

// msgMeta is the per-message detail the store keeps outside the messages
// table: the sender's account suffix, which tells same-named colleagues
// apart, the files a message's attachments were downloaded to, and the
// messages this page replies to, which are often older than the page.
type msgMeta struct {
	suffix  map[string]string
	res     map[string][]store.Resource
	parents map[string]store.Message
}

func loadMeta(ctx context.Context, st *store.Store, msgs []store.Message) (msgMeta, error) {
	msgIDs := make([]string, 0, len(msgs))
	ids := make([]string, 0, len(msgs))
	var parentIDs []string
	seen := map[string]bool{}
	addSender := func(id string) {
		if id != "" && !seen[id] {
			seen[id] = true
			ids = append(ids, id)
		}
	}
	for _, x := range msgs {
		msgIDs = append(msgIDs, x.MessageID)
		addSender(x.SenderID)
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
		addSender(p.SenderID)
	}
	contacts, err := st.ContactsByIDs(ctx, ids)
	if err != nil {
		return msgMeta{}, err
	}
	suffix := make(map[string]string, len(contacts))
	for id, c := range contacts {
		if s := c.AccountSuffix(); s != "" {
			suffix[id] = s
		}
	}
	res, err := st.ResourcesForMessages(ctx, msgIDs)
	if err != nil {
		return msgMeta{}, err
	}
	return msgMeta{suffix: suffix, res: res, parents: parents}, nil
}

func searchMessages(st *store.Store, query string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		rows, err := st.SearchMessages(ctx, query, "", 200)
		if err != nil {
			return errMsg{err}
		}
		meta, err := loadMeta(ctx, st, rows)
		if err != nil {
			return errMsg{err}
		}
		return searchMsg{query: query, msgs: rows, meta: meta}
	}
}

func waitForAI(ch <-chan ai.Chunk) tea.Cmd {
	return func() tea.Msg {
		c, ok := <-ch
		if !ok {
			return aiChunkMsg{ai.Chunk{Done: true}}
		}
		return aiChunkMsg{c}
	}
}

func loadChats(st *store.Store) tea.Cmd {
	return func() tea.Msg {
		chats, err := st.ListChats(context.Background(), store.ChatQuery{})
		if err != nil {
			return errMsg{err}
		}
		unread, _ := st.UnreadCountsByChat(context.Background())
		return chatsLoadedMsg{chats: chats, unread: unread}
	}
}

// messageQuery is the newest page of a chat or, anchored at sinceMs, every
// message from that time on, so a search hit older than the page is included.
func messageQuery(chatID string, sinceMs int64) store.MessageQuery {
	if sinceMs > 0 {
		return store.MessageQuery{ChatID: chatID, SinceMs: sinceMs, Limit: anchoredPageSize}
	}
	return store.MessageQuery{ChatID: chatID, Desc: true, Limit: messagePageSize}
}

func loadMessages(st *store.Store, chatID string, sinceMs int64) tea.Cmd {
	q := messageQuery(chatID, sinceMs)
	return func() tea.Msg {
		ctx := context.Background()
		rows, err := st.ListMessages(ctx, q)
		if err != nil {
			return errMsg{err}
		}
		if q.Desc {
			slices.Reverse(rows)
		}
		meta, err := loadMeta(ctx, st, rows)
		if err != nil {
			return errMsg{err}
		}
		return messagesLoadedMsg{chatID: chatID, msgs: rows, meta: meta}
	}
}

func loadThread(st *store.Store, threadID string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		rows, err := st.ListMessages(ctx, store.MessageQuery{ThreadID: threadID, Limit: threadPageSize})
		if err != nil {
			return errMsg{err}
		}
		meta, err := loadMeta(ctx, st, rows)
		if err != nil {
			return errMsg{err}
		}
		return threadLoadedMsg{threadID: threadID, msgs: rows, meta: meta}
	}
}

func markConsumed(st *store.Store, msgs []store.Message) tea.Cmd {
	ids := make([]string, 0, len(msgs))
	for _, m := range msgs {
		if m.ConsumedAt == 0 {
			ids = append(ids, m.MessageID)
		}
	}
	if len(ids) == 0 {
		return nil
	}
	return func() tea.Msg {
		_ = st.MarkConsumed(context.Background(), ids, time.Now().UnixMilli())
		return nil
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

// sendText hands a chat one message. localID doubles as the idempotency key,
// so a retry under the same id is the send Feishu already knows about rather
// than a second delivery.
func sendText(d Deps, localID string, target larkcli.Target, text string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		sent, err := d.Client.SendText(ctx, target, text, localID)
		return sentMsg{localID: localID, messageID: sent.MessageID, err: err}
	}
}

func replyText(d Deps, localID, messageID, text string, inThread bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		sent, err := d.Client.ReplyText(ctx, messageID, text, inThread, localID)
		return sentMsg{localID: localID, messageID: sent.MessageID, err: err}
	}
}

// ingestCmd fetches a just-sent message past the sync watermark, so the row
// the panes draw comes from the store like every other.
func ingestCmd(d Deps, localID, messageID string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
		defer cancel()
		s := d.Syncer
		if s == nil {
			s = &sync.Syncer{Client: d.Client, Store: d.Store, Clock: sync.RealClock{}}
		}
		return ingestedMsg{localID: localID, err: s.IngestIDs(ctx, []string{messageID})}
	}
}

// loadSelfName names the account this process signed in as, which is all a
// pending send has to go on until Feishu answers with a real message.
func loadSelfName(st *store.Store, self string) tea.Cmd {
	if self == "" {
		return nil
	}
	return func() tea.Msg {
		contacts, _ := st.ContactsByIDs(context.Background(), []string{self})
		return selfNameMsg{name: contacts[self].Name}
	}
}

// feishuChatLink addresses a chat, optionally at a message position. The
// lark:// scheme reaches the desktop client directly; the https applink form
// would first open a browser tab that only redirects here.
func feishuChatLink(chatID string, position int64) string {
	url := "lark://applink.feishu.cn/client/chat/open?openChatId=" + chatID
	if position > 0 {
		url += "&position=" + strconv.FormatInt(position, 10)
	}
	return url
}

// openInFeishu opens a chat (optionally at a message position) in the desktop client.
func openInFeishu(chatID string, position int64) tea.Cmd {
	url := feishuChatLink(chatID, position)
	return func() tea.Msg {
		if err := exec.Command("open", url).Start(); err != nil {
			return errMsg{err}
		}
		return noticeMsg{"opened in Feishu"}
	}
}

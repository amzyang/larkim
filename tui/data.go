package tui

import (
	"context"
	"fmt"
	"os/exec"
	"slices"
	"strconv"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/google/uuid"
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
}

const (
	messagePageSize  = 200
	anchoredPageSize = 2000 // from a search hit onwards
	threadPageSize   = 500
	watchEvery       = 500 * time.Millisecond
	statusEvery      = 5 * time.Second
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
	changeMsg     struct{ msgs []store.Message }
	sentMsg       struct{ err error }
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
// apart, and the files a message's attachments were downloaded to.
type msgMeta struct {
	suffix map[string]string
	res    map[string][]store.Resource
}

func loadMeta(ctx context.Context, st *store.Store, msgs []store.Message) (msgMeta, error) {
	ids := make([]string, 0, len(msgs))
	msgIDs := make([]string, 0, len(msgs))
	seen := map[string]bool{}
	for _, x := range msgs {
		msgIDs = append(msgIDs, x.MessageID)
		if x.SenderID != "" && !seen[x.SenderID] {
			seen[x.SenderID] = true
			ids = append(ids, x.SenderID)
		}
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
	return msgMeta{suffix: suffix, res: res}, nil
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

func waitForChange(ch <-chan []store.Message) tea.Cmd {
	return func() tea.Msg {
		msgs, ok := <-ch
		if !ok {
			return nil
		}
		return changeMsg{msgs}
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

// sendText sends to a chat and ingests the result so the message appears at once.
func sendText(d Deps, target larkcli.Target, text string) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		sent, err := d.Client.SendText(ctx, target, text, uuid.NewString())
		if err != nil {
			return sentMsg{err}
		}
		return sentMsg{ingest(ctx, d, sent.MessageID)}
	}
}

func replyText(d Deps, messageID, text string, inThread bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
		defer cancel()
		sent, err := d.Client.ReplyText(ctx, messageID, text, inThread, uuid.NewString())
		if err != nil {
			return sentMsg{err}
		}
		return sentMsg{ingest(ctx, d, sent.MessageID)}
	}
}

func ingest(ctx context.Context, d Deps, id string) error {
	s := d.Syncer
	if s == nil {
		s = &sync.Syncer{Client: d.Client, Store: d.Store, Clock: sync.RealClock{}}
	}
	if err := s.IngestIDs(ctx, []string{id}); err != nil {
		return fmt.Errorf("sent, but storing locally failed: %w", err)
	}
	return nil
}

// openInFeishu opens a chat (optionally at a message position) in the desktop client.
func openInFeishu(chatID string, position int64) tea.Cmd {
	url := "https://applink.feishu.cn/client/chat/open?openChatId=" + chatID
	if position > 0 {
		url += "&position=" + strconv.FormatInt(position, 10)
	}
	return func() tea.Msg {
		if err := exec.Command("open", url).Start(); err != nil {
			return errMsg{err}
		}
		return noticeMsg{"opened in Feishu"}
	}
}

package tui

import (
	"cmp"
	"context"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/fuzzy"
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
	// OpenURL hands applinks, links and local files to the desktop. New fills
	// it when nil; tests replace it to keep the real `open` out of the run.
	// Several targets are opened together rather than one by one.
	OpenURL func(targets []string, background bool) error
	// Env reads the environment the external editor is named in. New fills it
	// when nil; tests replace it to keep a real editor out of the run.
	Env func(string) string
	// Clipboard reports what the system clipboard holds, staging an image
	// into stageDir. New fills it when nil; tests replace it to keep
	// osascript and the machine's own clipboard out of the run.
	Clipboard func(stageDir string) (clip, error)
	// Fetch downloads a remote image a draft names, returning the bytes and
	// the response content type. New fills it with sync.HTTPFetch; tests
	// replace it to keep the network out of the run.
	Fetch func(ctx context.Context, url string) ([]byte, string, error)
	// Log records what the TUI cannot show. stderr is the alternate screen
	// here, so anything worth knowing has to reach the log file or be lost.
	// New fills it with a discard logger when nil.
	Log *slog.Logger
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
		// drafts is every chat's unsent composer state, fetched with the list
		// rather than per row so the marker costs one query a refresh.
		drafts map[string]store.Draft
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
	}
	// draftSavedMsg closes a fire-and-forget draft write. Nothing acts on it;
	// it exists because a tea.Cmd has to return a message.
	draftSavedMsg   struct{}
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
		// keys are the image keys this attempt uploaded, in the order the
		// draft names them. They come back even when the send then failed,
		// so a retry is one more send rather than one more upload.
		keys []string
		err  error
	}
	// pastedMsg answers a read of the clipboard.
	pastedMsg struct {
		clip clip
		err  error
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
	// docs names the Feishu documents linked to from any message, by
	// store.DocRef.Key. It is not scoped to this page: a personal archive
	// holds these in the thousands at most, and working out which links the
	// rows happen to spell costs more than reading them all.
	docs    map[string]store.DocLabel
	parents map[string]store.Message
}

func loadMeta(ctx context.Context, st *store.Store, msgs []store.Message) (msgMeta, error) {
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
	for _, x := range msgs {
		msgIDs = append(msgIDs, x.MessageID)
		addPerson(x.SenderID)
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
	res, err := st.ResourcesForMessages(ctx, msgIDs)
	if err != nil {
		return msgMeta{}, err
	}
	docs, err := st.DocLabels(ctx)
	if err != nil {
		return msgMeta{}, err
	}
	return msgMeta{suffix: suffix, people: people, avatars: avatars, res: res, docs: docs, parents: parents}, nil
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
		meta, err := loadMeta(ctx, d.Store, msgs)
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

func waitForAI(ch <-chan ai.Chunk) tea.Cmd {
	return func() tea.Msg {
		c, ok := <-ch
		if !ok {
			return aiChunkMsg{ai.Chunk{Done: true}}
		}
		return aiChunkMsg{c}
	}
}

// loadChats reads the sidebar in one query. The badge counts ride on the rows
// they belong to, so a chat's number and its place can never come from two
// different revisions of the database.
func loadChats(d Deps) tea.Cmd {
	return func() tea.Msg {
		chats, err := d.Store.ListChats(context.Background(), store.ChatQuery{Self: d.Self})
		if err != nil {
			return errMsg{err}
		}
		unread := make(map[string]int64, len(chats))
		for _, c := range chats {
			if c.UnreadCount > 0 {
				unread[c.ChatID] = c.UnreadCount
			}
		}
		// A draft the store cannot answer for costs the list its marker, not
		// its rows: the chats are what the reader asked for.
		drafts, err := d.Store.Drafts(context.Background())
		if err != nil {
			d.log().Error("load drafts", "err", err)
			drafts = nil
		}
		return chatsLoadedMsg{chats: chats, unread: unread, drafts: drafts}
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

func loadMessages(d Deps, chatID string, sinceMs int64) tea.Cmd {
	q := messageQuery(chatID, sinceMs)
	return func() tea.Msg {
		ctx := context.Background()
		rows, err := d.Store.ListMessages(ctx, q)
		if err != nil {
			return errMsg{err}
		}
		if q.Desc {
			slices.Reverse(rows)
		}
		meta, err := loadMeta(ctx, d.Store, rows)
		if err != nil {
			return errMsg{err}
		}
		// A draft the store cannot answer for costs the composer its text, not
		// the reader their page.
		draft, err := d.Store.LoadDraft(ctx, chatID)
		if err != nil {
			d.log().Error("load draft", "chat_id", chatID, "err", err)
			draft = store.Draft{ChatID: chatID}
		}
		// A roster the store cannot answer for costs @ completion its
		// candidates, not the reader their page.
		roster, err := d.Store.ChatRoster(ctx, chatID, d.Self)
		if err != nil {
			d.log().Error("load roster", "chat_id", chatID, "err", err)
			roster = nil
		}
		return messagesLoadedMsg{chatID: chatID, msgs: rows, meta: meta, draft: draft, roster: roster}
	}
}

func loadThread(d Deps, threadID string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		rows, err := d.Store.ListMessages(ctx, store.MessageQuery{ThreadID: threadID, Limit: threadPageSize})
		if err != nil {
			return errMsg{err}
		}
		meta, err := loadMeta(ctx, d.Store, rows)
		if err != nil {
			return errMsg{err}
		}
		return threadLoadedMsg{threadID: threadID, msgs: rows, meta: meta}
	}
}

// markChatRead takes the chat's unread messages as read locally, which is
// what makes its badge fall: Feishu offers no way to say a message was read.
func markChatRead(st *store.Store, log *slog.Logger, chatID string) tea.Cmd {
	return func() tea.Msg {
		if err := st.MarkChatRead(context.Background(), chatID, time.Now().UnixMilli()); err != nil {
			// The badge staying up is the only symptom on screen, which says
			// nothing about why.
			log.Warn("mark chat read", "chat_id", chatID, "err", err)
		}
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

// waited builds the context for a lark-cli call somebody pressed a key for. It
// takes the interactive lane, so a send or a reaction does not queue behind
// the syncer's sweeps or the open chat's beat — both run every few seconds, so
// a shared line would be occupied more often than not.
func waited(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(larkcli.WithLane(context.Background(), larkcli.LaneInteractive), d)
}

// beat builds the context for a timer-driven refresh of what is already on
// screen. Nobody pressed a key for it, so it takes a lane of its own: sharing
// the interactive line, the 1.5s beat's two calls held two of its three slots
// and a send landed behind them.
func beat(d time.Duration) (context.Context, context.CancelFunc) {
	return context.WithTimeout(larkcli.WithLane(context.Background(), larkcli.LaneBeat), d)
}

// sendMsg hands a chat one message. localID doubles as the idempotency key,
// so a retry under the same id is the send Feishu already knows about rather
// than a second delivery.
func sendMsg(d Deps, localID string, target larkcli.Target, msg larkcli.Outgoing, imgs []draftImage, file draftFile, done []string) tea.Cmd {
	return func() tea.Msg {
		msg, keys, err := uploadDraft(d, msg, imgs, file, done)
		if err != nil {
			return sentMsg{localID: localID, keys: keys, err: err}
		}
		ctx, cancel := waited(sendTimeout)
		defer cancel()
		sent, err := d.Client.Send(ctx, target, msg, localID)
		return sentMsg{localID: localID, messageID: sent.MessageID, keys: keys, err: err}
	}
}

func replyMsg(d Deps, localID, messageID string, msg larkcli.Outgoing, inThread bool, imgs []draftImage, file draftFile, done []string) tea.Cmd {
	return func() tea.Msg {
		msg, keys, err := uploadDraft(d, msg, imgs, file, done)
		if err != nil {
			return sentMsg{localID: localID, keys: keys, err: err}
		}
		ctx, cancel := waited(sendTimeout)
		defer cancel()
		sent, err := d.Client.Reply(ctx, messageID, msg, inThread, localID)
		return sentMsg{localID: localID, messageID: sent.MessageID, keys: keys, err: err}
	}
}

// uploadImages puts a draft's files on Feishu and swaps the placeholders in
// the body for the keys that came back. done carries the keys an earlier
// attempt already uploaded, so a retry is one more send rather than one more
// upload leaving an orphan key behind. Uploads run one at a time because
// lark-cli calls are serialised anyway, and each gets its own deadline rather
// than sharing one budget with the send that follows.
func uploadDraft(d Deps, msg larkcli.Outgoing, imgs []draftImage, file draftFile, done []string) (larkcli.Outgoing, []string, error) {
	// An attachment and a set of pictures never arrive together: Feishu
	// carries a file as a message of its own.
	if file.local != "" {
		if len(done) > 0 {
			msg.FileKey = done[0]
			return msg, done, nil
		}
		ctx, cancel := waited(sendTimeout)
		defer cancel()
		key, err := d.Client.UploadFile(ctx, file.local)
		if err != nil {
			return msg, nil, fmt.Errorf("upload %s: %w", filepath.Base(file.local), err)
		}
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
			up, err := uploadOne(d, img.local)
			if err != nil {
				return msg, keys, err
			}
			key = up
		case img.url != "":
			path, err := fetchRemote(d, img.url)
			if err != nil {
				return msg, keys, err
			}
			up, err := uploadOne(d, path)
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

// uploadOne puts one file on Feishu under a deadline of its own, rather than
// sharing one budget with the send and every other image behind it.
func uploadOne(d Deps, path string) (string, error) {
	ctx, cancel := waited(sendTimeout)
	defer cancel()
	key, err := d.Client.UploadImage(ctx, path)
	if err != nil {
		return "", fmt.Errorf("upload %s: %w", filepath.Base(path), err)
	}
	return key, nil
}

// remoteCeiling is what the shared fetcher reads at most. It truncates rather
// than failing, so a body that comes back at exactly the ceiling is refused:
// uploading a half-downloaded picture is worse than refusing to send.
const remoteCeiling = 8 << 20

// fetchRemote downloads an image a draft named by URL and writes it where
// UploadImage can take it, since lark-cli uploads a path rather than bytes.
func fetchRemote(d Deps, url string) (string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), sendTimeout)
	defer cancel()
	body, ctype, err := d.Fetch(ctx, url)
	if err != nil {
		return "", fmt.Errorf("fetch %s: %w", url, err)
	}
	if len(body) == 0 {
		return "", fmt.Errorf("fetch %s: empty response", url)
	}
	if len(body) >= remoteCeiling {
		return "", fmt.Errorf("%s is at least %s, over the %s limit",
			url, humanBytes(int64(len(body))), humanBytes(remoteCeiling))
	}
	f, err := os.CreateTemp("", "larkim-remote-*"+remoteExt(ctype))
	if err != nil {
		return "", err
	}
	_, werr := f.Write(body)
	cerr := f.Close()
	if err := firstErr(werr, cerr); err != nil {
		os.Remove(f.Name())
		return "", err
	}
	return f.Name(), nil
}

// remoteExt names the temp file after what the server said it sent. Feishu
// sniffs the bytes, so this only has to be plausible, never authoritative.
func remoteExt(contentType string) string {
	switch {
	case strings.Contains(contentType, "jpeg"), strings.Contains(contentType, "jpg"):
		return ".jpg"
	case strings.Contains(contentType, "gif"):
		return ".gif"
	case strings.Contains(contentType, "webp"):
		return ".webp"
	default:
		return ".png"
	}
}

// pasteClipboard reads the clipboard off the Update loop: the osascript round
// trip takes long enough to be seen as a stutter if it ran inline.
func pasteClipboard(d Deps) tea.Cmd {
	return func() tea.Msg {
		c, err := d.Clipboard(filepath.Join(d.DataDir, pastedDir))
		return pastedMsg{clip: c, err: err}
	}
}

// ingestCmd fetches a just-sent message past the sync watermark, so the row
// the panes draw comes from the store like every other.
func ingestCmd(d Deps, localID, messageID string) tea.Cmd {
	return func() tea.Msg {
		return ingestedMsg{localID: localID, err: ingestMessage(d, messageID)}
	}
}

// syncerFor is the syncer a write uses to pull what it changed back into the
// store. The injected one when this process holds the lock, a throwaway
// otherwise: ingest is an idempotent upsert of ids Feishu just answered for,
// so it is safe beside a daemon and is what lets a write land without one.
func syncerFor(d Deps) *sync.Syncer {
	if d.Syncer != nil {
		return d.Syncer
	}
	return &sync.Syncer{Client: d.Client, Store: d.Store, Clock: sync.RealClock{}, Log: d.Log}
}

// ingestMessage pulls one message back from Feishu into the store, so what a
// send, a recall or a forward just did shows up without waiting for a tick.
func ingestMessage(d Deps, messageID string) error {
	ctx, cancel := waited(sendTimeout)
	defer cancel()
	return syncerFor(d).IngestIDs(ctx, []string{messageID})
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

// openURL hands targets to macOS. A keypress asking for the Feishu client
// wants the screen; an applink fired to clear a badge must leave the reader
// in the terminal, which is what background buys.
//
// Several targets go in one invocation rather than one each, because that is
// what puts a message's pictures in a single viewer window with the rest in
// its sidebar — the way the client opens them — instead of scattering them
// over as many windows as the message had pictures.
func openURL(log *slog.Logger, targets []string, background bool) error {
	args := targets
	if background {
		args = append([]string{"-g"}, targets...)
	}
	// -g is decided here, not by the caller, so this is the only place the
	// argv exists whole. It is logged the way lark-cli's is: quoted, nothing
	// elided, paste-able back into a shell to see what macOS was asked.
	log.Debug("open", "argv", "open "+larkcli.ArgvLine(args))
	// open reports why it refused on stderr and nothing but a status to the
	// caller, so dropping stderr would leave every failure as "exit status 1".
	cmd := exec.Command("open", args...)
	var stderr strings.Builder
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		if msg := strings.TrimSpace(stderr.String()); msg != "" {
			return fmt.Errorf("open: %w: %s", err, msg)
		}
		return fmt.Errorf("open: %w", err)
	}
	return nil
}

// openInFeishu opens a chat (optionally at a message position) in the desktop client.
func openInFeishu(d Deps, chatID string, position int64) tea.Cmd {
	return func() tea.Msg {
		if err := d.OpenURL([]string{feishuChatLink(chatID, position)}, false); err != nil {
			// The notice bar holds the message and is gone at the next
			// keypress; which chat was asked for only exists here.
			d.Log.Error("open in feishu", "chat_id", chatID, "position", position, "err", err)
			return errMsg{err}
		}
		return noticeMsg{"opened in Feishu"}
	}
}

// clearFeishuBadge walks the desktop client onto a chat without taking the
// screen, which is what makes it send the read receipt Feishu offers no API
// for. It is the only lever larkim has on the client's own red dot, and it is
// best effort: local_read_at has already dropped the badge drawn here.
func clearFeishuBadge(d Deps, chatID string) tea.Cmd {
	return func() tea.Msg {
		if err := d.OpenURL([]string{feishuChatLink(chatID, 0)}, true); err != nil {
			// Best effort, and fired by a chat switch rather than by a request
			// to open anything: an error banner here would blame the reader's
			// navigation for a dot only the client still draws.
			d.Log.Warn("clear feishu badge", "chat_id", chatID, "err", err)
		}
		return nil
	}
}

// feishuMeetingLink joins a meeting by its number. The lark:// scheme works
// on the vc host too, so the client goes straight into the call rather than
// through a browser redirect.
func feishuMeetingLink(meetNumber string) string {
	return "lark://vc.feishu.cn/j/" + meetNumber
}

// openZone hands over what a row's target points at: a meeting to join, a
// link, or an attachment's own file on this machine. It takes the screen,
// which is what the keypress or the click asked for.
func openZone(d Deps, z clickZone) tea.Cmd {
	return func() tea.Msg {
		if len(z.urls) == 0 {
			return nil
		}
		if err := d.OpenURL(z.urls, false); err != nil {
			// The notice bar has room for the message but not for what was
			// handed over, and a zone carries as many targets as the message
			// had attachments.
			d.Log.Error("open zone", "targets", z.urls, "err", err)
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
		rendered, err := d.Client.MGetRendered(ctx, cold, false)
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
	people, _ := d.Store.ContactsByIDs(ctx, senders)
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
		ctx, cancel := waited(sendTimeout)
		defer cancel()
		if err := d.Syncer.IngestIDs(ctx, []string{messageID}); err != nil {
			return errMsg{err}
		}
		x, err := d.Store.GetMessage(ctx, messageID)
		if err != nil {
			return errMsg{err}
		}
		return openHitMsg{chatID: x.ChatID, messageID: x.MessageID, sinceMs: x.CreateMs}
	}
}

// openHitMsg lands a cold hit once it is in the store.
type openHitMsg struct {
	chatID, messageID string
	sinceMs           int64
}

// saveDraft writes the composer's state under the chat it was typed in. It is
// fire-and-forget: a draft is a convenience, and a chat switch must not wait on
// the disk. A failure is logged rather than shown, since the reader is already
// looking at the next chat by the time it could be.
func saveDraft(d Deps, chatID, text, replyTo string, inThread bool) tea.Cmd {
	if chatID == "" {
		return nil
	}
	return func() tea.Msg {
		err := d.Store.SaveDraft(context.Background(), store.Draft{
			ChatID: chatID, Text: text, ReplyTo: replyTo, InThread: inThread,
		}, time.Now().UnixMilli())
		if err != nil {
			d.log().Error("save draft", "chat_id", chatID, "err", err)
		}
		return draftSavedMsg{}
	}
}

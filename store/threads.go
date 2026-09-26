package store

import "context"

// StakedThread names a thread the reader has a standing in, and the chat it
// happens in.
type StakedThread struct {
	ThreadID string
	ChatID   string
}

// StakedThreads names the threads the reader took a turn in — the root counts
// as a turn — or was called by name in, freshest first. A chat's own listing
// returns a thread's root and none of its replies, and the sweep asks a thread
// container only about threads whose root it saw in the window it just listed.
// A reply to an older root, which is the whole point of a thread, therefore
// reaches the store only if the thread is asked after by name.
//
// Freshest first and bounded is self-limiting: a thread that goes quiet falls
// out of the window on its own, which is where the reader's interest went too,
// so no cursor has to be carried between passes.
func (s *Store) StakedThreads(ctx context.Context, self string, limit int) ([]StakedThread, error) {
	// An empty reader has a stake in nothing rather than in everything: instr
	// with an empty needle answers 1 on any string, the way ChatQuery.Self
	// guards.
	if self == "" || limit <= 0 {
		return nil, nil
	}
	// chat_id is bare under the grouping because a thread lives in exactly one
	// chat: every row of a group carries the same one. A chat the listing
	// already refused is left out — its threads would refuse too, and the
	// refusal is recorded on the chat.
	return queryAll(ctx, s.db, func(sc scanner) (StakedThread, error) {
		var t StakedThread
		err := sc.Scan(&t.ThreadID, &t.ChatID)
		return t, err
	}, `SELECT m.thread_id, m.chat_id FROM messages m JOIN chats c ON c.chat_id = m.chat_id
 WHERE m.thread_id <> '' AND c.left_at = 0 AND c.sync_error = '' AND `+threadStake+`
 GROUP BY m.thread_id ORDER BY max(m.create_ms) DESC, m.thread_id LIMIT ?`,
		self, self, limit)
}

// ThreadFeed is one thread as the chats list shows it: the conversation a
// root started, standing beside the chat it happens in the way the client's
// own list stands one there. Only threads the reader has a stake in appear —
// a thread nobody asked them about is somebody else's conversation, which is
// why the chat's badge leaves replies out in the first place.
type ThreadFeed struct {
	ThreadID string
	ChatID   string
	ChatName string
	ChatMode string
	// P2PTargetID is the peer of a chat of two, which is what shades the
	// avatar there — see Chat.AvatarSeed.
	P2PTargetID string
	Muted       bool
	// Root is the message the thread hangs from; it titles the row.
	Root Message
	// Last is the newest reply, which is where the thread stands: a thread is
	// alive, unlike a forward, so the last word is the state of it.
	Last Message
	// Unread counts the replies still owed an answer, on the badge's own rule
	// except for the sign of the position: these are the replies the chat's
	// own badge leaves out.
	Unread int64
	// NamesSelf says one of those unread replies calls the reader by name.
	NamesSelf bool
}

// Chat is the little of the owning chat a row of this kind draws with: the
// name and the peer shade the avatar column, the mute mark rides the summary,
// and a chat of two names nobody on it. Everything here comes from the same
// chats row as the thread itself, so no second lookup can disagree with it.
func (t ThreadFeed) Chat() Chat {
	return Chat{ChatID: t.ChatID, Name: t.ChatName, ChatMode: t.ChatMode,
		P2PTargetID: t.P2PTargetID, Muted: t.Muted}
}

// ThreadFeedQuery filters ListThreadFeed.
type ThreadFeedQuery struct {
	// Self is the reader's open id. Empty answers with nothing rather than
	// with everything: instr with an empty needle answers 1 on any string.
	Self  string
	Limit int
}

// ListThreadFeed returns the reader's threads, newest reply first. A thread
// with no reply is absent: its root is the newest thing in it and the chat's
// own row already says that much.
//
// Unlike a chat's, these rows are derived on the spot rather than kept in
// cold columns. A chat has one row per chat and the list reloads on every
// batch of messages; the threads with a stake in them are few, and they walk
// the messages_thread index rather than the whole of messages.
func (s *Store) ListThreadFeed(ctx context.Context, q ThreadFeedQuery) ([]ThreadFeed, error) {
	if q.Self == "" {
		return nil, nil
	}
	limit := q.Limit
	if limit <= 0 {
		limit = 200
	}
	// The placeholders bind in the order they are written: the mention inside
	// the window, then the stake twice, then the bound.
	return queryAll(ctx, s.db, scanThreadFeed, threadFeedQuery, q.Self, q.Self, q.Self, limit)
}

// threadFeedQuery is ListThreadFeed's own statement, named so a test can read
// the plan it produces: the rows come out the same however the planner walks
// them, and walking messages rather than the thread index is the difference
// between costing what is listed and costing the whole synced history.
//
// A silenced reply is out of the window entirely, the way last_unsilenced_ms
// keeps one from moving its chat: silence decides what may pull the reader,
// not what counts as read. A recalled one stays, the way a chat's summary line
// keeps its place and says it was recalled.
//
// The root is joined rather than left-joined: a thread whose root the store
// has not seen has nothing to title a row with, and it gets one as soon as the
// root arrives.
var threadFeedQuery = `WITH reps AS (
 SELECT m.thread_id, m.chat_id, m.message_id, m.sender_id, m.sender_type, m.sender_name,
   m.msg_type, m.content, m.content_raw, m.mentions_json, m.rendered_at, m.deleted, m.create_ms,
   row_number() OVER (PARTITION BY m.thread_id ORDER BY m.create_ms DESC, m.message_position DESC, m.id DESC) AS rn,
   SUM(CASE WHEN ` + stillUnread + ` THEN 1 ELSE 0 END) OVER (PARTITION BY m.thread_id) AS unread,
   MAX(CASE WHEN ` + stillUnread + ` AND ` + namesSelf + ` THEN 1 ELSE 0 END) OVER (PARTITION BY m.thread_id) AS at_me
 FROM messages m LEFT JOIN read_state r ON r.message_id = m.message_id
 WHERE m.message_position < 0 AND m.thread_id <> '' AND m.silenced = 0
), roots AS (
 SELECT m.thread_id, m.message_id, m.sender_id, m.sender_type, m.sender_name,
   m.msg_type, m.content, m.content_raw, m.rendered_at, m.deleted, m.create_ms,
   row_number() OVER (PARTITION BY m.thread_id ORDER BY m.create_ms, m.message_position, m.id) AS rn
 FROM messages m WHERE m.thread_id <> '' AND m.message_position >= 0
)
SELECT x.thread_id, x.chat_id, c.name, c.chat_mode, c.p2p_target_id, c.muted,
 ro.message_id, ro.sender_id, ro.sender_type, ro.sender_name, ro.msg_type, ro.content,
 ro.content_raw, ro.rendered_at, ro.deleted, ro.create_ms,
 x.message_id, x.sender_id, x.sender_type, x.sender_name, x.msg_type, x.content,
 x.content_raw, x.mentions_json, x.rendered_at, x.deleted, x.create_ms, x.unread, x.at_me
 FROM reps x JOIN chats c ON c.chat_id = x.chat_id JOIN roots ro ON ro.thread_id = x.thread_id AND ro.rn = 1
 WHERE x.rn = 1 AND c.left_at = 0 AND ` + threadStakeOn("x") + `
 ORDER BY x.create_ms DESC, x.thread_id LIMIT ?`

func scanThreadFeed(sc scanner) (ThreadFeed, error) {
	var t ThreadFeed
	err := sc.Scan(&t.ThreadID, &t.ChatID, &t.ChatName, &t.ChatMode, &t.P2PTargetID, &t.Muted,
		&t.Root.MessageID, &t.Root.SenderID, &t.Root.SenderType, &t.Root.SenderName, &t.Root.MsgType,
		&t.Root.Content, &t.Root.ContentRaw, &t.Root.RenderedAt, &t.Root.Deleted, &t.Root.CreateMs,
		&t.Last.MessageID, &t.Last.SenderID, &t.Last.SenderType, &t.Last.SenderName, &t.Last.MsgType,
		&t.Last.Content, &t.Last.ContentRaw, &t.Last.MentionsJSON, &t.Last.RenderedAt, &t.Last.Deleted, &t.Last.CreateMs,
		&t.Unread, &t.NamesSelf)
	t.Root.ThreadID, t.Root.ChatID = t.ThreadID, t.ChatID
	t.Last.ThreadID, t.Last.ChatID = t.ThreadID, t.ChatID
	return t, err
}

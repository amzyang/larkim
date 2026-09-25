package larkcli

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"
	"time"
)

// Fake is an in-memory Client for tests. Messages are keyed by id; list and
// search derive their results from the same set so scenarios stay coherent.
type Fake struct {
	mu       sync.Mutex
	Messages map[string]RawMessage
	Chats    []RawChat
	Rendered map[string]RenderedMessage
	// Resources are returned by MGetRendered with download=true.
	Resources map[string][]Resource
	// Singles answer DownloadResource, keyed "<message id>/<file key>". A key
	// listed nowhere is refused the way Feishu refuses one the message does
	// not carry.
	Singles map[string]Resource
	Read    map[string]bool
	// Reactions answers ReactionCounts; a message absent from it holds none.
	Reactions map[string]json.RawMessage
	// Reacted is what AddReaction and DeleteReaction write, keyed by message
	// id, so a test can assert on what reached Feishu rather than on the
	// summary a later refresh would have brought back.
	Reacted     map[string][]Reaction
	reactionSeq int
	// Muted answers MuteStatus for chats in Chats; one that is listed
	// nowhere comes back unknown, as a chat the user is not a member of does.
	Muted   map[string]bool
	Members map[string][]ChatMember
	// MembersTruncated marks a chat whose roster the server caps, which is
	// what a tenant's security config does to a large group.
	MembersTruncated map[string]bool
	Users            []User
	Details          map[string]UserDetail
	Self             Identity
	// Truncate makes SearchMessageIDs report truncation when a window holds
	// more than this many hits (0 disables).
	Truncate int
	// SearchQueries records the keywords SearchMessages was asked for.
	SearchQueries []string
	// Err, when set, is returned by every call until cleared.
	Err error
	// ListErr injects a per-container error into ListMessagesRaw.
	ListErr map[string]error
	// DetailsErr injects an error into UserDetails alone.
	DetailsErr error
	// SendErr injects an error into Send alone, which is how a test gets an
	// upload that landed behind a send that did not.
	SendErr error
	// Apps are the apps AppDetail can resolve, by app id.
	Apps map[string]AppDetail
	// SentKeys records the idempotency key of every send, in order, so a
	// test can tell a retry from a second delivery.
	SentKeys []string
	// Sent records the body of every send, in the same order as SentKeys.
	Sent []Outgoing
	// Recalled records the id of every message taken back, in order.
	Recalled []string
	// Forwarded records each forward as "<message id>->
	// <chat or user id>", in order.
	Forwarded []string
	// Uploads records the path of every image upload, in order.
	Uploads  []string
	Calls    []string
	sent     int
	uploaded int
}

// NewFake returns an empty Fake with a default identity.
func NewFake() *Fake {
	return &Fake{
		Messages:         map[string]RawMessage{},
		Rendered:         map[string]RenderedMessage{},
		Resources:        map[string][]Resource{},
		Singles:          map[string]Resource{},
		Read:             map[string]bool{},
		Reactions:        map[string]json.RawMessage{},
		Reacted:          map[string][]Reaction{},
		Muted:            map[string]bool{},
		Members:          map[string][]ChatMember{},
		MembersTruncated: map[string]bool{},
		Details:          map[string]UserDetail{},
		Apps:             map[string]AppDetail{},
		Self:             Identity{AppID: "cli_test", UserOpenID: "ou_self"},
	}
}

// AddMessage registers a message; Raw is synthesized when empty.
func (f *Fake) AddMessage(m RawMessage) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if m.Raw == nil {
		m.Raw, _ = json.Marshal(map[string]any{
			"message_id": m.MessageID, "chat_id": m.ChatID, "msg_type": m.MsgType,
			"create_time": fmt.Sprint(int64(m.CreateTime)), "body": map[string]string{"content": m.Body.Content},
		})
	}
	f.Messages[m.MessageID] = m
}

func (f *Fake) record(call string) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Calls = append(f.Calls, call)
	return f.Err
}

func (f *Fake) SearchMessageIDs(_ context.Context, start, end time.Time) ([]SearchHit, bool, error) {
	if err := f.record("search"); err != nil {
		return nil, false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var hits []SearchHit
	for _, m := range f.Messages {
		ct := m.CreateTime.Time()
		if ct.Before(start) || ct.After(end) {
			continue
		}
		hits = append(hits, SearchHit{MessageID: m.MessageID, ChatID: m.ChatID, FromID: m.Sender.ID,
			ThreadID: m.ThreadID, Type: m.MsgType, CreateTime: ct, Position: int64(m.MessagePosition)})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].CreateTime.After(hits[j].CreateTime) })
	if f.Truncate > 0 && len(hits) > f.Truncate {
		return hits[:f.Truncate], true, nil
	}
	return hits, false, nil
}

// SearchMessages answers a keyword search from the messages the fake holds,
// newest first. A test plays a hit this machine has never synced by putting
// the message here and leaving it out of the store.
func (f *Fake) SearchMessages(_ context.Context, query string, limit int) ([]SearchHit, error) {
	if err := f.record("search-query"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.SearchQueries = append(f.SearchQueries, query)
	var hits []SearchHit
	for _, m := range f.Messages {
		if query != "" && !strings.Contains(m.Body.Content, query) {
			continue
		}
		hits = append(hits, SearchHit{MessageID: m.MessageID, ChatID: m.ChatID, FromID: m.Sender.ID,
			ThreadID: m.ThreadID, Type: m.MsgType, CreateTime: m.CreateTime.Time(),
			Position: int64(m.MessagePosition)})
	}
	sort.Slice(hits, func(i, j int) bool { return hits[i].CreateTime.After(hits[j].CreateTime) })
	if limit > 0 && len(hits) > limit {
		hits = hits[:limit]
	}
	return hits, nil
}

func (f *Fake) MGetRaw(_ context.Context, ids []string) ([]RawMessage, error) {
	if err := f.record("mget"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []RawMessage
	for _, id := range ids {
		if m, ok := f.Messages[id]; ok {
			out = append(out, m)
		}
	}
	return out, nil
}

func (f *Fake) ListMessagesRaw(_ context.Context, containerType, containerID string, start, end time.Time) ([]RawMessage, error) {
	if err := f.record("list:" + containerType + ":" + containerID); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.ListErr[containerID]; err != nil {
		return nil, err
	}
	var out []RawMessage
	for _, m := range f.Messages {
		switch containerType {
		case "chat":
			// Thread replies live in the thread container, as the real API does.
			if m.ChatID != containerID || (m.ThreadID != "" && int64(m.MessagePosition) < 0) {
				continue
			}
		case "thread":
			if m.ThreadID != containerID || int64(m.MessagePosition) >= 0 {
				continue
			}
		}
		ct := m.CreateTime.Time()
		if !start.IsZero() && ct.Before(start.Truncate(time.Second)) {
			continue
		}
		if !end.IsZero() && ct.After(end) {
			continue
		}
		out = append(out, m)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].CreateTime < out[j].CreateTime })
	return out, nil
}

func (f *Fake) ListChats(_ context.Context, activeFirstPage bool) ([]RawChat, error) {
	if err := f.record(fmt.Sprintf("chats:%v", activeFirstPage)); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]RawChat(nil), f.Chats...), nil
}

func (f *Fake) MGetRendered(_ context.Context, ids []string, download bool) ([]RenderedMessage, error) {
	if err := f.record(fmt.Sprintf("render:%v", download)); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []RenderedMessage
	for _, id := range ids {
		r, ok := f.Rendered[id]
		if !ok {
			m, known := f.Messages[id]
			if !known {
				continue
			}
			r = RenderedMessage{MessageID: id, ChatID: m.ChatID, Content: "rendered:" + m.Body.Content}
		}
		if download {
			r.Resources = append([]Resource(nil), f.Resources[id]...)
		}
		out = append(out, r)
	}
	return out, nil
}

func (f *Fake) DownloadResource(_ context.Context, messageID, fileKey, typ string) (Resource, error) {
	if err := f.record("download:" + messageID + ":" + fileKey); err != nil {
		return Resource{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.Singles[messageID+"/"+fileKey]
	if !ok {
		return Resource{}, &Error{ExitCode: ExitAPI, Message: "234003 File not in msg"}
	}
	r.MessageID, r.Key, r.Type = messageID, fileKey, typ
	return r, nil
}

func (f *Fake) AddReaction(_ context.Context, messageID, emojiType string) (Reaction, error) {
	if err := f.record("react:add:" + messageID + ":" + emojiType); err != nil {
		return Reaction{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.reactionSeq++
	r := Reaction{ReactionID: fmt.Sprintf("rx_%d", f.reactionSeq), EmojiType: emojiType,
		OperatorID: f.Self.UserOpenID}
	f.Reacted[messageID] = append(f.Reacted[messageID], r)
	return r, nil
}

func (f *Fake) ListReactions(_ context.Context, messageID, emojiType string) ([]Reaction, error) {
	if err := f.record("react:list:" + messageID + ":" + emojiType); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []Reaction
	for _, r := range f.Reacted[messageID] {
		if r.EmojiType == emojiType {
			out = append(out, r)
		}
	}
	return out, nil
}

func (f *Fake) DeleteReaction(_ context.Context, messageID, reactionID string) error {
	if err := f.record("react:delete:" + messageID + ":" + reactionID); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	kept := f.Reacted[messageID][:0]
	for _, r := range f.Reacted[messageID] {
		if r.ReactionID != reactionID {
			kept = append(kept, r)
		}
	}
	f.Reacted[messageID] = kept
	return nil
}

func (f *Fake) ReactionCounts(_ context.Context, messageIDs []string) (map[string]json.RawMessage, error) {
	if err := f.record("reaction-counts:" + strings.Join(messageIDs, ",")); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	out := make(map[string]json.RawMessage, len(messageIDs))
	for _, id := range messageIDs {
		out[id] = f.Reactions[id]
	}
	return out, nil
}

func (f *Fake) ReadStatus(_ context.Context, ids []string) ([]ReadStatus, []string, error) {
	if err := f.record("read-status"); err != nil {
		return nil, nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var items []ReadStatus
	var invalid []string
	for _, id := range ids {
		if _, ok := f.Messages[id]; !ok {
			invalid = append(invalid, id)
			continue
		}
		items = append(items, ReadStatus{MessageID: id, IsRead: f.Read[id]})
	}
	return items, invalid, nil
}

func (f *Fake) MuteStatus(_ context.Context, chatIDs []string) (map[string]bool, []string, error) {
	if err := f.record("mute-status"); err != nil {
		return nil, nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	known := map[string]bool{}
	for _, c := range f.Chats {
		known[c.ChatID] = true
	}
	muted := map[string]bool{}
	var unknown []string
	for _, id := range chatIDs {
		if !known[id] {
			unknown = append(unknown, id)
			continue
		}
		muted[id] = f.Muted[id]
	}
	return muted, unknown, nil
}

func (f *Fake) ChatMembers(_ context.Context, chatID string) ([]ChatMember, bool, error) {
	if err := f.record("members:" + chatID); err != nil {
		return nil, false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ChatMember(nil), f.Members[chatID]...), f.MembersTruncated[chatID], nil
}

func (f *Fake) SearchUsers(_ context.Context, query string, ids []string) ([]User, error) {
	if err := f.record("users"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []User
	for _, u := range f.Users {
		if query != "" && (u.Email == query || u.Name == query) {
			out = append(out, u)
		}
		for _, id := range ids {
			if u.OpenID == id {
				out = append(out, u)
			}
		}
	}
	return out, nil
}

func (f *Fake) AppDetail(_ context.Context, appID string) (AppDetail, error) {
	if err := f.record("app:" + appID); err != nil {
		return AppDetail{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	a, ok := f.Apps[appID]
	if !ok {
		return AppDetail{}, &Error{ExitCode: ExitAPI, Type: "api", Subtype: "not_found", Code: 210508,
			Message: "insufficient permission level"}
	}
	return a, nil
}

func (f *Fake) UserDetails(_ context.Context, openIDs []string) ([]UserDetail, error) {
	if err := f.record("users:" + strings.Join(openIDs, ",")); err != nil {
		return nil, err
	}
	if f.DetailsErr != nil {
		return nil, f.DetailsErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []UserDetail
	for _, id := range openIDs {
		// A user outside the directory scope is simply absent, as upstream.
		if d, ok := f.Details[id]; ok {
			out = append(out, d)
		}
	}
	return out, nil
}

func (f *Fake) SearchChats(_ context.Context, query string) ([]RawChat, error) {
	if err := f.record("chat-search"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []RawChat
	for _, c := range f.Chats {
		if c.Name == query {
			out = append(out, c)
		}
	}
	return out, nil
}

// body is what lark-cli would put in the message's content column for this
// kind, and rendered is what it would render that content back to. Keeping
// both here lets a test follow a send all the way through ingest.
func (o Outgoing) body() (msgType, content, rendered string) {
	switch {
	case o.Markdown != "":
		// Built by the same function the wire uses, so the two cannot drift.
		return "post", postContent(o.Markdown), o.Markdown
	case o.ImageKey != "":
		key, _ := json.Marshal(o.ImageKey)
		return "image", `{"image_key":` + string(key) + `}`, "[Image: " + o.ImageKey + "]"
	case o.FileKey != "":
		key, _ := json.Marshal(o.FileKey)
		return "file", `{"file_key":` + string(key) + `}`, "[File: " + o.FileKey + "]"
	default:
		text, _ := json.Marshal(o.Text)
		return "text", `{"text":` + string(text) + `}`, o.Text
	}
}

func (f *Fake) UploadImage(_ context.Context, path string) (string, error) {
	f.mu.Lock()
	f.Uploads = append(f.Uploads, path)
	f.mu.Unlock()
	if err := f.record("image-upload"); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploaded++
	return fmt.Sprintf("img_fake_%d", f.uploaded), nil
}

func (f *Fake) UploadFile(_ context.Context, path string) (string, error) {
	f.mu.Lock()
	f.Uploads = append(f.Uploads, path)
	f.mu.Unlock()
	if err := f.record("file-upload"); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploaded++
	return fmt.Sprintf("file_fake_%d", f.uploaded), nil
}

func (f *Fake) Send(_ context.Context, target Target, msg Outgoing, idempotencyKey string) (SentMessage, error) {
	f.mu.Lock()
	f.SentKeys = append(f.SentKeys, idempotencyKey)
	f.Sent = append(f.Sent, msg)
	f.mu.Unlock()
	if err := f.record("send"); err != nil {
		return SentMessage{}, err
	}
	if f.SendErr != nil {
		return SentMessage{}, f.SendErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent++
	id := fmt.Sprintf("om_sent_%d", f.sent)
	chat := target.ChatID
	if chat == "" {
		chat = "oc_p2p_" + target.UserID
	}
	msgType, content, rendered := msg.body()
	m := RawMessage{MessageID: id, ChatID: chat, MsgType: msgType, CreateTime: Millis(time.Now().UnixMilli()),
		Sender: RawSender{ID: f.Self.UserOpenID, SenderType: "user"}, Body: RawBody{Content: content}}
	m.Raw, _ = json.Marshal(map[string]string{"message_id": id})
	f.Messages[id] = m
	f.Rendered[id] = RenderedMessage{MessageID: id, ChatID: chat, MsgType: msgType, Content: rendered, Raw: m.Raw}
	return SentMessage{MessageID: id, ChatID: chat}, nil
}

func (f *Fake) Recall(_ context.Context, messageID string) error {
	f.mu.Lock()
	f.Recalled = append(f.Recalled, messageID)
	f.mu.Unlock()
	if err := f.record("recall:" + messageID); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.Messages[messageID]
	if !ok {
		return fmt.Errorf("no such message %s", messageID)
	}
	m.Deleted = true
	f.Messages[messageID] = m
	return nil
}

func (f *Fake) Forward(ctx context.Context, messageID string, target Target, key string) (SentMessage, error) {
	to := target.ChatID
	if to == "" {
		to = target.UserID
	}
	f.mu.Lock()
	f.Forwarded = append(f.Forwarded, messageID+"->"+to)
	f.mu.Unlock()
	if err := f.record("forward:" + messageID); err != nil {
		return SentMessage{}, err
	}
	f.mu.Lock()
	src, ok := f.Messages[messageID]
	f.mu.Unlock()
	if !ok {
		return SentMessage{}, fmt.Errorf("no such message %s", messageID)
	}
	// A forward lands as a new message carrying the original's body, which is
	// what makes it ingestable like any other send.
	return f.Send(ctx, target, Outgoing{Text: src.Body.Content}, key)
}

func (f *Fake) Reply(ctx context.Context, messageID string, msg Outgoing, inThread bool, key string) (SentMessage, error) {
	f.mu.Lock()
	parent, ok := f.Messages[messageID]
	f.mu.Unlock()
	if !ok {
		return SentMessage{}, &Error{ExitCode: ExitAPI, Type: "api", Subtype: "not_found", Message: "message not found"}
	}
	s, err := f.Send(ctx, Target{ChatID: parent.ChatID}, msg, key)
	if err != nil {
		return s, err
	}
	f.mu.Lock()
	m := f.Messages[s.MessageID]
	m.ParentID = messageID
	if inThread {
		m.ThreadID = "omt_" + messageID
		m.MessagePosition = -1
	}
	f.Messages[s.MessageID] = m
	f.mu.Unlock()
	return s, nil
}

func (f *Fake) Whoami(_ context.Context) (Identity, error) {
	if err := f.record("whoami"); err != nil {
		return Identity{}, err
	}
	return f.Self, nil
}

var _ Client = (*Fake)(nil)

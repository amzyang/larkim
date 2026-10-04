package larkcli

import (
	"cmp"
	"context"
	"encoding/json"
	"fmt"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/amzyang/larkim/larkmd"
)

// Fake is an in-memory Client for tests. Messages are keyed by id; list and
// search derive their results from the same set so scenarios stay coherent.
type Fake struct {
	mu       sync.Mutex
	Messages map[string]RawMessage
	Chats    []RawChat
	Rendered map[string]RenderedMessage
	// Resources answer DownloadResource, keyed "<message id>/<file key>". A key
	// listed nowhere is refused the way Feishu refuses one the message does
	// not carry.
	Resources map[string]Resource
	// Recognized answers RecognizeText, keyed by the path it is asked for. A
	// path listed nowhere reads as a picture with nothing written in it.
	Recognized map[string][]string
	// RecognizeErr fails RecognizeText alone, so a test can starve the text
	// sweep while the rest of the client keeps working.
	RecognizeErr error
	Read         map[string]bool
	// Reactions answers ReactionCounts; a message absent from it holds none.
	Reactions map[string]json.RawMessage
	// Reacted is what AddReaction and DeleteReaction write, keyed by message
	// id, so a test can assert on what reached Feishu rather than on the
	// summary a later refresh would have brought back.
	Reacted     map[string][]Reaction
	reactionSeq int
	// Muted answers MuteStatus for chats in Chats; one that is listed
	// nowhere comes back unknown, as a chat the user is not a member of does.
	Muted map[string]bool
	// Tasks answers ListTasks, keyed by task guid: true is done. A guid
	// listed nowhere is a task the listing does not name, which a caller
	// reads as "keep the box it had".
	Tasks   map[string]bool
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
	// OlderPage is how many messages one OlderMessagesRaw page holds; 0 takes
	// the cap the real client asks under. A short page is what tells a caller
	// it has reached the start of the chat, so a test sizing this is sizing
	// the end of history.
	OlderPage int
	// SearchHidden names messages SearchMessageIDs withholds, standing in for
	// the search index running behind the message store.
	SearchHidden []string
	// SearchQueries records the keywords SearchMessages was asked for.
	SearchQueries []string
	// Enter runs at the top of every call, outside the lock, so a test can
	// hold callers there and count how many arrive at once. Holding them
	// inside the lock would show one at a time whatever the caller did. Set
	// it before the calls start; nothing reads it under a lock.
	Enter func(call string)
	// Err, when set, is returned by every call until cleared.
	Err error
	// ListErr injects a per-container error into ListMessagesRaw.
	ListErr map[string]error
	// Bundles answers ForwardedMessages, by the bundle's own message id.
	// The name keeps clear of Forwarded, which records outgoing forwards.
	Bundles map[string][]RawForwarded
	// BundleErr injects a per-bundle error, the way ListErr does.
	BundleErr map[string]error
	// DetailsErr injects an error into UserDetails alone.
	DetailsErr error
	// SendErr injects an error into Send alone, which is how a test gets an
	// upload that landed behind a send that did not.
	SendErr error
	// ReactionErr injects an error into ReactionCounts alone, which is how a
	// test gets a message that landed without the reactions on it.
	ReactionErr error
	// Docs are the documents DocTitles can name, by "<doc_type>/<token>" as
	// the request spells it. A document listed nowhere comes back denied, as
	// one the identity cannot read does.
	Docs map[string]DocTitle
	// DocsSilent are documents DocTitles leaves out of both lists, keyed like
	// Docs. The endpoint answers per token, and the echo a title is matched
	// back by is optional upstream, so a token can come back neither named
	// nor refused.
	DocsSilent map[string]bool
	// Forms are the Base forms FormTitle can name, by share token, and
	// Minutes the recordings MinuteTitle can name, by minute token. A token
	// listed in neither is refused the way the endpoints refuse a deleted or
	// unreadable one: an API error, which is permanent.
	Forms   map[string]string
	Minutes map[string]string
	// Apps are the apps AppDetail can resolve, by app id.
	Apps map[string]AppDetail
	// SentKeys records the idempotency key of every send, in order, so a
	// test can tell a retry from a second delivery.
	SentKeys []string
	// Sent records the body of every send, in the same order as SentKeys.
	Sent []Outgoing
	// Patched records each card rewrite as "<message id> <content>", in
	// order, so a test can read the whole life of a streamed card.
	Patched []string
	// PatchErr fails PatchMessage alone, so a test can take the final
	// rewrite of a streamed card down without touching the send.
	PatchErr error
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
		Bundles:          map[string][]RawForwarded{},
		BundleErr:        map[string]error{},
		Rendered:         map[string]RenderedMessage{},
		Resources:        map[string]Resource{},
		Read:             map[string]bool{},
		Reactions:        map[string]json.RawMessage{},
		Reacted:          map[string][]Reaction{},
		Muted:            map[string]bool{},
		Tasks:            map[string]bool{},
		Members:          map[string][]ChatMember{},
		MembersTruncated: map[string]bool{},
		Details:          map[string]UserDetail{},
		Apps:             map[string]AppDetail{},
		Docs:             map[string]DocTitle{},
		DocsSilent:       map[string]bool{},
		Forms:            map[string]string{},
		Minutes:          map[string]string{},
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
	if f.Enter != nil {
		f.Enter(call)
	}
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
		if ct.Before(start) || ct.After(end) || slices.Contains(f.SearchHidden, m.MessageID) {
			continue
		}
		hits = append(hits, SearchHit{MessageID: m.MessageID, ChatID: m.ChatID, FromID: m.Sender.ID,
			ThreadID: m.ThreadID, Type: m.MsgType, CreateTime: ct, Position: int64(m.MessagePosition)})
	}
	slices.SortFunc(hits, func(a, b SearchHit) int { return b.CreateTime.Compare(a.CreateTime) })
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
	slices.SortFunc(hits, func(a, b SearchHit) int { return b.CreateTime.Compare(a.CreateTime) })
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

func (f *Fake) ForwardedMessages(_ context.Context, rootMessageID string) ([]RawForwarded, error) {
	if err := f.record("forwarded:" + rootMessageID); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.BundleErr[rootMessageID]; err != nil {
		return nil, err
	}
	return f.Bundles[rootMessageID], nil
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
	slices.SortFunc(out, func(a, b RawMessage) int { return cmp.Compare(a.CreateTime, b.CreateTime) })
	return out, nil
}

func (f *Fake) OlderMessagesRaw(_ context.Context, chatID string, before time.Time) ([]RawMessage, bool, error) {
	if err := f.record("older:" + chatID); err != nil {
		return nil, false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.ListErr[chatID]; err != nil {
		return nil, false, err
	}
	var out []RawMessage
	for _, m := range f.Messages {
		// Thread replies live in the thread container, as the real API does.
		if m.ChatID != chatID || (m.ThreadID != "" && int64(m.MessagePosition) < 0) {
			continue
		}
		if m.CreateTime.Time().After(before) {
			continue
		}
		out = append(out, m)
	}
	slices.SortFunc(out, func(a, b RawMessage) int { return cmp.Compare(b.CreateTime, a.CreateTime) })
	size := cmp.Or(f.OlderPage, listPageSize)
	return out[:min(len(out), size)], len(out) > size, nil
}

func (f *Fake) ListChats(_ context.Context) ([]RawChat, error) {
	if err := f.record("chats"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Chats), nil
}

// ActiveChats takes f.Chats to be in active-time order already, which is what
// a test arranging them means.
func (f *Fake) ActiveChats(_ context.Context, n int) ([]RawChat, error) {
	if err := f.record(fmt.Sprintf("active:%d", n)); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Chats[:min(len(f.Chats), n)]), nil
}

func (f *Fake) MGetRendered(_ context.Context, ids []string) ([]RenderedMessage, error) {
	if len(ids) == 0 {
		return nil, nil // no subprocess either, the way ExecClient answers
	}
	if err := f.record("render"); err != nil {
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
		out = append(out, r)
	}
	return out, nil
}

func (f *Fake) RecognizeText(_ context.Context, path string) ([]string, error) {
	if err := f.record("recognize:" + path); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.RecognizeErr != nil {
		return nil, f.RecognizeErr
	}
	return f.Recognized[path], nil
}

func (f *Fake) DownloadResource(_ context.Context, messageID, fileKey, typ string) (Resource, error) {
	if err := f.record("download:" + messageID + ":" + fileKey); err != nil {
		return Resource{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.Resources[messageID+"/"+fileKey]
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
	f.Reacted[messageID] = slices.DeleteFunc(f.Reacted[messageID], func(r Reaction) bool { return r.ReactionID == reactionID })
	return nil
}

func (f *Fake) ReactionCounts(_ context.Context, messageIDs []string) (map[string]json.RawMessage, error) {
	if err := f.record("reaction-counts:" + strings.Join(messageIDs, ",")); err != nil {
		return nil, err
	}
	if f.ReactionErr != nil {
		return nil, f.ReactionErr
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

func (f *Fake) SetChatMuted(_ context.Context, chatID string, muted bool) error {
	if err := f.record("set-chat-muted"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, c := range f.Chats {
		if c.ChatID == chatID {
			if f.Muted == nil {
				f.Muted = map[string]bool{}
			}
			f.Muted[chatID] = muted
			return nil
		}
	}
	return fmt.Errorf("not a member")
}

func (f *Fake) ListTasks(_ context.Context) ([]Task, error) {
	if err := f.record("tasks"); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	tasks := make([]Task, 0, len(f.Tasks))
	for guid, done := range f.Tasks {
		tasks = append(tasks, Task{GUID: guid, Done: done})
	}
	return tasks, nil
}

func (f *Fake) CompleteTask(_ context.Context, guid string) error {
	if err := f.record("task complete"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Tasks[guid] = true
	return nil
}

func (f *Fake) ReopenTask(_ context.Context, guid string) error {
	if err := f.record("task reopen"); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.Tasks[guid] = false
	return nil
}

func (f *Fake) ChatMembers(_ context.Context, chatID string) ([]ChatMember, bool, error) {
	if err := f.record("members:" + chatID); err != nil {
		return nil, false, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.Members[chatID]), f.MembersTruncated[chatID], nil
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

func (f *Fake) DocTitles(_ context.Context, refs []DocRef) (DocTitles, error) {
	if err := f.record("doc-titles"); err != nil {
		return DocTitles{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	var out DocTitles
	for _, r := range refs {
		key := r.Type + "/" + r.Token
		if f.DocsSilent[key] {
			continue
		}
		d, ok := f.Docs[key]
		if !ok {
			out.Denied = append(out.Denied, r)
			continue
		}
		d.Ref = r
		out.Found = append(out.Found, d)
	}
	return out, nil
}

func (f *Fake) FormTitle(_ context.Context, shareToken string) (string, error) {
	if err := f.record("form-detail"); err != nil {
		return "", err
	}
	return f.lookupTitle(f.Forms, shareToken)
}

func (f *Fake) MinuteTitle(_ context.Context, token string) (string, error) {
	if err := f.record("minute-get"); err != nil {
		return "", err
	}
	return f.lookupTitle(f.Minutes, token)
}

func (f *Fake) lookupTitle(from map[string]string, token string) (string, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	title, ok := from[token]
	if !ok {
		return "", &Error{ExitCode: ExitAPI, Type: "api", Message: "resource not found"}
	}
	return title, nil
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
	case o.Post != "":
		return "post", o.Post, o.Post
	case o.Card != "":
		return "interactive", o.Card, CardMarkdown(o.Card)
	case o.Markdown != "":
		// Built by the same functions the wire uses, so the two cannot drift.
		if md, ok := larkmd.CardForm(o.Markdown); ok {
			c := Card(md).Card
			return "interactive", c, md
		}
		return "post", larkmd.PostContent(o.Markdown), o.Markdown
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
	return f.upload(path, "image-upload", "img")
}

func (f *Fake) UploadFile(_ context.Context, path string) (string, error) {
	return f.upload(path, "file-upload", "file")
}

// upload records the path before the call can fail, so a test sees every
// attempt, and answers with a key spelled the way Feishu spells kind's.
func (f *Fake) upload(path, call, kind string) (string, error) {
	f.mu.Lock()
	f.Uploads = append(f.Uploads, path)
	f.mu.Unlock()
	if err := f.record(call); err != nil {
		return "", err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.uploaded++
	return fmt.Sprintf("%s_fake_%d", kind, f.uploaded), nil
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
	chat := cmp.Or(target.ChatID, "oc_p2p_"+target.UserID)
	msgType, content, rendered := msg.body()
	m := RawMessage{MessageID: id, ChatID: chat, MsgType: msgType, CreateTime: Millis(time.Now().UnixMilli()),
		Sender: RawSender{ID: f.Self.UserOpenID, SenderType: "user"}, Body: RawBody{Content: content}}
	m.Raw, _ = json.Marshal(map[string]string{"message_id": id})
	f.Messages[id] = m
	f.Rendered[id] = RenderedMessage{MessageID: id, ChatID: chat, MsgType: msgType, Content: rendered, Raw: m.Raw}
	return SentMessage{MessageID: id, ChatID: chat, Message: answered(m)}, nil
}

// answered is the message as a send's answer carries it: Feishu leaves the
// position out.
func answered(m RawMessage) *RawMessage {
	m.MessagePosition = 0
	return &m
}

// PatchMessage rewrites the stored card the way the wire does: the message's
// content becomes the new card JSON, and the rewrite is recorded in order.
// PatchErr, when set, fails every rewrite.
func (f *Fake) PatchMessage(_ context.Context, messageID, content string) error {
	if err := f.record("patch:" + messageID); err != nil {
		return err
	}
	if f.PatchErr != nil {
		return f.PatchErr
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.Messages[messageID]
	if !ok {
		return &Error{ExitCode: ExitAPI, Type: "api", Subtype: "not_found", Message: "message not found"}
	}
	f.Patched = append(f.Patched, messageID+" "+content)
	m.Body.Content = content
	m.Updated = true
	f.Messages[messageID] = m
	return nil
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
	to := cmp.Or(target.ChatID, target.UserID)
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
	// what makes it ingestable like any other send. The forward command keeps
	// only the ids of its answer.
	s, err := f.Send(ctx, target, Outgoing{Text: src.Body.Content}, key)
	s.Message = nil
	return s, err
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
	s.Message = answered(m)
	return s, nil
}

func (f *Fake) Whoami(_ context.Context) (Identity, error) {
	if err := f.record("whoami"); err != nil {
		return Identity{}, err
	}
	return f.Self, nil
}

var _ Client = (*Fake)(nil)

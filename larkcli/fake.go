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
	Read      map[string]bool
	Members   map[string][]ChatMember
	Users     []User
	Details   map[string]UserDetail
	Self      Identity
	// Truncate makes SearchMessageIDs report truncation when a window holds
	// more than this many hits (0 disables).
	Truncate int
	// Err, when set, is returned by every call until cleared.
	Err error
	// ListErr injects a per-container error into ListMessagesRaw.
	ListErr map[string]error
	// DetailsErr injects an error into UserDetails alone.
	DetailsErr error
	Calls      []string
	sent       int
}

// NewFake returns an empty Fake with a default identity.
func NewFake() *Fake {
	return &Fake{
		Messages:  map[string]RawMessage{},
		Rendered:  map[string]RenderedMessage{},
		Resources: map[string][]Resource{},
		Read:      map[string]bool{},
		Members:   map[string][]ChatMember{},
		Details:   map[string]UserDetail{},
		Self:      Identity{AppID: "cli_test", UserOpenID: "ou_self"},
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

func (f *Fake) ChatMembers(_ context.Context, chatID string) ([]ChatMember, error) {
	if err := f.record("members:" + chatID); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]ChatMember(nil), f.Members[chatID]...), nil
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

func (f *Fake) SendText(_ context.Context, target Target, text, _ string) (SentMessage, error) {
	if err := f.record("send"); err != nil {
		return SentMessage{}, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent++
	id := fmt.Sprintf("om_sent_%d", f.sent)
	chat := target.ChatID
	if chat == "" {
		chat = "oc_p2p_" + target.UserID
	}
	m := RawMessage{MessageID: id, ChatID: chat, MsgType: "text", CreateTime: Millis(time.Now().UnixMilli()),
		Sender: RawSender{ID: f.Self.UserOpenID, SenderType: "user"}, Body: RawBody{Content: `{"text":"` + text + `"}`}}
	m.Raw, _ = json.Marshal(map[string]string{"message_id": id})
	f.Messages[id] = m
	return SentMessage{MessageID: id, ChatID: chat}, nil
}

func (f *Fake) ReplyText(ctx context.Context, messageID, text string, inThread bool, key string) (SentMessage, error) {
	f.mu.Lock()
	parent, ok := f.Messages[messageID]
	f.mu.Unlock()
	if !ok {
		return SentMessage{}, &Error{ExitCode: ExitAPI, Type: "api", Subtype: "not_found", Message: "message not found"}
	}
	s, err := f.SendText(ctx, Target{ChatID: parent.ChatID}, text, key)
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

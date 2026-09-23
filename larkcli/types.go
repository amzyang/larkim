// Package larkcli wraps the official lark-cli binary as a typed client.
//
// lark-cli owns authentication (OAuth device flow, keychain, token refresh),
// pagination and rate-limit classification; this package only shells out to it
// and decodes its documented JSON envelope.
//
// Calls run with user identity, except contact reads, which go as the app so
// they reach the whole tenant rather than one person's view of it.
package larkcli

import (
	"encoding/json"
	"strconv"
	"time"
)

// SearchHit is one entry of POST /im/v1/messages/search (items[].meta_data).
type SearchHit struct {
	MessageID  string
	ChatID     string
	FromID     string
	ThreadID   string
	Type       string
	IsP2P      bool
	Position   int64
	CreateTime time.Time
}

// RawSender mirrors the sender object of the raw message API.
type RawSender struct {
	ID         string            `json:"id"`
	IDType     string            `json:"id_type"`
	SenderType string            `json:"sender_type"`
	SenderName string            `json:"sender_name"`
	OpenBotID  string            `json:"open_bot_id"`
	I18nNames  map[string]string `json:"sender_i18n_names"`
}

// RawMention mirrors mentions[] of the raw message API.
type RawMention struct {
	Key    string `json:"key"`
	ID     string `json:"id"`
	IDType string `json:"id_type"`
	Name   string `json:"name"`
}

// RawMessage is one item of GET /im/v1/messages or /im/v1/messages/mget.
type RawMessage struct {
	MessageID       string       `json:"message_id"`
	ChatID          string       `json:"chat_id"`
	MsgType         string       `json:"msg_type"`
	CreateTime      Millis       `json:"create_time"`
	UpdateTime      Millis       `json:"update_time"`
	MessagePosition intString    `json:"message_position"`
	Deleted         bool         `json:"deleted"`
	Updated         bool         `json:"updated"`
	ThreadID        string       `json:"thread_id"`
	ParentID        string       `json:"parent_id"`
	RootID          string       `json:"root_id"`
	Sender          RawSender    `json:"sender"`
	Body            RawBody      `json:"body"`
	Mentions        []RawMention `json:"mentions"`
	Raw             json.RawMessage
}

// RawBody is the body object of a raw message; Content is the msg_type-specific JSON string.
type RawBody struct {
	Content string `json:"content"`
}

// RawChat is one item of GET /im/v1/chats.
type RawChat struct {
	ChatID        string `json:"chat_id"`
	Name          string `json:"name"`
	Description   string `json:"description"`
	Avatar        string `json:"avatar"`
	ChatMode      string `json:"chat_mode"`
	ChatStatus    string `json:"chat_status"`
	OwnerID       string `json:"owner_id"`
	External      bool   `json:"external"`
	P2PTargetID   string `json:"p2p_target_id"`
	P2PTargetType string `json:"p2p_target_type"`
	Raw           json.RawMessage
}

// Resource is one entry of resources[] produced by --download-resources.
type Resource struct {
	MessageID string `json:"message_id"`
	Key       string `json:"key"`
	Type      string `json:"type"`
	LocalPath string `json:"local_path"`
	SizeBytes int64  `json:"size_bytes"`
}

// RenderedMessage is one item of `im +messages-mget`: content is human-readable text.
type RenderedMessage struct {
	MessageID string          `json:"message_id"`
	ChatID    string          `json:"chat_id"`
	Content   string          `json:"content"`
	Mentions  json.RawMessage `json:"mentions"`
	Reactions json.RawMessage `json:"reactions"`
	Resources []Resource      `json:"resources"`
	Raw       json.RawMessage
}

// ReadStatus is one item of `im +messages-read-status`.
type ReadStatus struct {
	MessageID string `json:"message_id"`
	IsRead    bool   `json:"is_read"`
}

// ChatMember is one entry of GET /im/v1/chats/{id}/members.
type ChatMember struct {
	MemberID   string `json:"member_id"`
	MemberType string `json:"member_id_type"`
	Name       string `json:"name"`
}

// User is one entry of `contact +search-user`. EnterpriseEmail carries the
// tenant account name, whose numeric suffix is how Feishu tells same-named
// colleagues apart.
type User struct {
	OpenID          string `json:"open_id"`
	Name            string `json:"localized_name"`
	Email           string `json:"email"`
	EnterpriseEmail string `json:"enterprise_email"`
	Department      string `json:"department"`
	IsCrossTenant   bool   `json:"is_cross_tenant"`
	P2PChatID       string `json:"p2p_chat_id"`
}

// UserDetail is the contact card of one user (GET /contact/v3/users/{id}).
type UserDetail struct {
	OpenID    string
	Name      string
	AvatarURL string // 240px variant
}

// Identity is the resolved caller of a lark-cli invocation.
type Identity struct {
	AppID      string
	UserOpenID string
}

// SentMessage is the result of `im +messages-send` / `+messages-reply`.
type SentMessage struct {
	MessageID string `json:"message_id"`
	ChatID    string `json:"chat_id"`
}

// Millis decodes a millisecond timestamp encoded as a JSON string.
type Millis int64

func (m *Millis) UnmarshalJSON(b []byte) error { return unquoteInt(b, (*int64)(m)) }

// Time converts the millisecond value to time.Time in UTC.
func (m Millis) Time() time.Time { return time.UnixMilli(int64(m)).UTC() }

// intString decodes an integer encoded as a JSON string or number.
type intString int64

func (i *intString) UnmarshalJSON(b []byte) error { return unquoteInt(b, (*int64)(i)) }

// unquoteInt parses a JSON string or number into dst; null and "" read as 0.
func unquoteInt(b []byte, dst *int64) error {
	s := string(b)
	if s == "null" || s == `""` {
		*dst = 0
		return nil
	}
	if len(s) >= 2 && s[0] == '"' {
		s = s[1 : len(s)-1]
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return err
	}
	*dst = n
	return nil
}

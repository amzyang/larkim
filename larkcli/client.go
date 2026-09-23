package larkcli

import (
	"context"
	"fmt"
	"time"
)

// Client is the subset of lark-cli capabilities larkim depends on. All calls run
// with user identity.
type Client interface {
	// SearchMessageIDs lists messages created in [start, end] across every chat
	// the user can see. truncated reports that the server had more pages than
	// the page cap allowed.
	SearchMessageIDs(ctx context.Context, start, end time.Time) (hits []SearchHit, truncated bool, err error)
	// MGetRaw fetches up to 50 messages by id in their raw API shape.
	MGetRaw(ctx context.Context, ids []string) ([]RawMessage, error)
	// ListMessagesRaw lists a chat ("chat") or thread ("thread") container in
	// ascending create time. A zero start or end means unbounded.
	ListMessagesRaw(ctx context.Context, containerType, containerID string, start, end time.Time) ([]RawMessage, error)
	// ListChats lists p2p and group chats the user is in. With activeFirstPage
	// only the first page sorted by activity (most recent first) is returned.
	ListChats(ctx context.Context, activeFirstPage bool) ([]RawChat, error)
	// MGetRendered fetches up to 50 messages rendered to human-readable text;
	// with download, image/file resources are saved under the client's Dir.
	MGetRendered(ctx context.Context, ids []string, download bool) ([]RenderedMessage, error)
	// ReadStatus reports whether the current user has read each message.
	ReadStatus(ctx context.Context, ids []string) (items []ReadStatus, invalid []string, err error)
	// ChatMembers lists user members of a chat.
	ChatMembers(ctx context.Context, chatID string) ([]ChatMember, error)
	// SearchUsers finds users by keyword (name or email) or by open_id list.
	SearchUsers(ctx context.Context, query string, ids []string) ([]User, error)
	// AppDetail fetches one app's name and icon, which is how a bot's
	// picture is reached.
	AppDetail(ctx context.Context, appID string) (AppDetail, error)
	// UserDetails fetches names and avatar URLs. Users outside the app's
	// directory scope come back absent rather than as an error, so the result
	// may be shorter than the input.
	UserDetails(ctx context.Context, openIDs []string) ([]UserDetail, error)
	// SearchChats finds group chats by name keyword.
	SearchChats(ctx context.Context, query string) ([]RawChat, error)
	// SendText sends a plain-text message to a chat or a user.
	SendText(ctx context.Context, target Target, text string, idempotencyKey string) (SentMessage, error)
	// ReplyText replies to a message, optionally inside its thread.
	ReplyText(ctx context.Context, messageID, text string, inThread bool, idempotencyKey string) (SentMessage, error)
	// Whoami returns the current user identity without hitting the IM API.
	Whoami(ctx context.Context) (Identity, error)
}

// Target addresses a send: exactly one of ChatID or UserID is set.
type Target struct {
	ChatID string
	UserID string
}

// Error is a failed lark-cli invocation decoded from its stderr envelope.
type Error struct {
	ExitCode   int
	Type       string
	Subtype    string
	Code       int
	Message    string
	Hint       string
	RetryAfter time.Duration
	Stderr     string
}

func (e *Error) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("lark-cli exit %d: %s", e.ExitCode, truncate(e.Stderr, 300))
	}
	return fmt.Sprintf("lark-cli %s/%s (exit %d): %s", e.Type, e.Subtype, e.ExitCode, e.Message)
}

// Exit codes documented in lark-cli internal/output/exitcode.go.
const (
	ExitAPI     = 1
	ExitAuth    = 3
	ExitNetwork = 4
)

// IsAuth reports an invalid, expired or missing user token.
func (e *Error) IsAuth() bool { return e.ExitCode == ExitAuth }

// IsNetwork reports a transport failure.
func (e *Error) IsNetwork() bool { return e.ExitCode == ExitNetwork }

// IsRateLimit reports a gateway rate limit; RetryAfter is populated when known.
func (e *Error) IsRateLimit() bool { return e.Subtype == "rate_limit" }

// IsPermanent reports an API rejection that retrying the same request will
// not fix (permission, not found, unsupported chat type, …).
func (e *Error) IsPermanent() bool { return e.ExitCode == ExitAPI && !e.IsRateLimit() }

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}

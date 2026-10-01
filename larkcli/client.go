package larkcli

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

// Client is the subset of lark-cli capabilities larkim depends on. All calls run
// with user identity.
type Client interface {
	// SearchMessageIDs lists messages created in [start, end] across every chat
	// the user can see. truncated reports that the server had more pages than
	// the page cap allowed.
	SearchMessageIDs(ctx context.Context, start, end time.Time) (hits []SearchHit, truncated bool, err error)

	// SearchMessages finds messages by keyword across every chat, for a
	// reader's own query rather than the syncer's sweep. It takes one page.
	SearchMessages(ctx context.Context, query string, limit int) ([]SearchHit, error)
	// MGetRaw fetches up to 50 messages by id in their raw API shape.
	MGetRaw(ctx context.Context, ids []string) ([]RawMessage, error)
	// ListMessagesRaw lists a chat ("chat") or thread ("thread") container in
	// ascending create time. A zero start or end means unbounded.
	ListMessagesRaw(ctx context.Context, containerType, containerID string, start, end time.Time) ([]RawMessage, error)
	// OlderMessagesRaw takes one page of a chat's messages ending at before,
	// newest first. Bounding by count rather than by a time window is what
	// makes a quiet chat cheap to walk back: one call reaches whatever it last
	// said, however long ago that was.
	OlderMessagesRaw(ctx context.Context, chatID string, before time.Time) (msgs []RawMessage, more bool, err error)
	// ForwardedMessages lists every message inside a merged-forward bundle.
	// One call answers the whole tree: the items are flat, linked by
	// UpperMessageID, and one of them is the bundle itself.
	ForwardedMessages(ctx context.Context, rootMessageID string) ([]RawForwarded, error)
	// ListChats lists every p2p and group chat the user is in.
	ListChats(ctx context.Context) ([]RawChat, error)
	// ActiveChats lists the first n of those chats by activity, most recent
	// first: a chat that has just seen a message comes first.
	ActiveChats(ctx context.Context, n int) ([]RawChat, error)
	// MGetRendered fetches up to 50 messages rendered to human-readable text.
	MGetRendered(ctx context.Context, ids []string) ([]RenderedMessage, error)
	// DownloadResource fetches one attachment by key. typ is "image" or "file".
	DownloadResource(ctx context.Context, messageID, fileKey, typ string) (Resource, error)
	// RecognizeText reads the writing in a picture already on disk, one
	// string per region the recognizer found. A picture with nothing written
	// in it comes back empty rather than failing.
	RecognizeText(ctx context.Context, path string) ([]string, error)
	// ReactionCounts reads who reacted to each message. Every requested id gets
	// an entry; a nil one means Feishu holds no reaction for that message.
	ReactionCounts(ctx context.Context, messageIDs []string) (map[string]json.RawMessage, error)
	// AddReaction puts one emoji on a message under the user's own name.
	AddReaction(ctx context.Context, messageID, emojiType string) (Reaction, error)
	// ListReactions lists who reacted to a message with one emoji, which is
	// the only way to learn the reaction id a delete needs.
	ListReactions(ctx context.Context, messageID, emojiType string) ([]Reaction, error)
	// DeleteReaction takes back a reaction this identity added.
	DeleteReaction(ctx context.Context, messageID, reactionID string) error
	// ReadStatus reports whether the current user has read each message.
	ReadStatus(ctx context.Context, ids []string) (items []ReadStatus, invalid []string, err error)
	// ChatMembers lists the users and the bots in a chat. truncated reports
	// that the server capped the list, so what came back is a part of the
	// roster and must not be presented as the whole of it.
	ChatMembers(ctx context.Context, chatID string) (members []ChatMember, truncated bool, err error)
	// MuteStatus reports the user's do-not-disturb setting per chat. unknown
	// carries the chats the API declined to answer for, which a caller must
	// not read as "not muted".
	MuteStatus(ctx context.Context, chatIDs []string) (muted map[string]bool, unknown []string, err error)
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
	// Send posts one message body to a chat or a user.
	Send(ctx context.Context, target Target, msg Outgoing, idempotencyKey string) (SentMessage, error)
	// Reply answers a message, optionally inside its thread.
	Reply(ctx context.Context, messageID string, msg Outgoing, inThread bool, idempotencyKey string) (SentMessage, error)
	// UploadImage registers a local image and returns its key. Callers upload
	// before sending because lark-cli's --image refuses an absolute path and
	// resolves a relative one against the client's Dir, while images create
	// takes any path.
	UploadImage(ctx context.Context, path string) (string, error)
	// UploadFile registers a local file and returns its key, for the same
	// reason UploadImage exists: the send flag will not take an absolute path.
	UploadFile(ctx context.Context, path string) (string, error)
	// Recall takes back a message. Feishu lets an identity recall only what it
	// sent, and only inside its own time limit, so it answers the refusal
	// rather than this deciding either.
	Recall(ctx context.Context, messageID string) error
	// PatchMessage replaces a message's content wholesale — the streaming
	// card's route. Feishu takes it for a message this identity sent within
	// 14 days whose content stays under 30 KB, and answers for both itself.
	PatchMessage(ctx context.Context, messageID, content string) error
	// Forward sends an existing message on to another chat or person. The
	// idempotency key deduplicates for an hour, the way a send's does.
	Forward(ctx context.Context, messageID string, target Target, idempotencyKey string) (SentMessage, error)
	// DocTitles names Feishu documents by token. A document this identity
	// cannot read comes back under Denied rather than failing the call, so one
	// unreadable link does not cost the rest of the batch their titles.
	DocTitles(ctx context.Context, refs []DocRef) (DocTitles, error)
	// FormTitle names a Base form by the share token its published URL
	// carries. The metadata endpoint DocTitles uses has no doc_type for a
	// form, so a form is asked for on its own.
	FormTitle(ctx context.Context, shareToken string) (string, error)
	// MinuteTitle names a Minutes recording by its token, which is likewise
	// outside the metadata endpoint's reach.
	MinuteTitle(ctx context.Context, token string) (string, error)
	// Whoami returns the current user identity without hitting the IM API.
	Whoami(ctx context.Context) (Identity, error)
}

// Target addresses a send: exactly one of ChatID or UserID is set.
type Target struct {
	ChatID string
	UserID string
}

// Outgoing is one message body: exactly one field is set, which is what picks
// the Feishu message type. lark-cli's content flags are mutually exclusive, so
// a body that set two would be refused by the subprocess rather than here.
type Outgoing struct {
	Text     string // msg_type text
	Markdown string // msg_type post, still to be converted from markdown
	Post     string // msg_type post, already the body Feishu stores
	ImageKey string // msg_type image; a key, never a path
	FileKey  string // msg_type file; a key, never a path
	Card     string // msg_type interactive, already the 2.0 card JSON
}

// Text is an Outgoing carrying plain text.
func Text(s string) Outgoing { return Outgoing{Text: s} }

// Markdown is an Outgoing sent as the rich-text post larkmd.PostContent
// spells it into.
func Markdown(s string) Outgoing { return Outgoing{Markdown: s} }

// Emotion is an Outgoing carrying one emoji and nothing else, as the post
// element Feishu spells an emoji with inside a message. It is how an emoji
// Feishu refuses as a reaction still reaches the other side as itself:
// another tenant's culture emoji goes inside a message fine, and a picture of
// it would only look like one.
func Emotion(emojiType string) Outgoing {
	key, _ := json.Marshal(emojiType)
	return Outgoing{Post: `{"zh_cn":{"content":[[{"tag":"emotion","emoji_type":` + string(key) + `}]]}}`}
}

// File is an Outgoing naming an already-uploaded file. Feishu carries a file
// as a message of its own rather than as something inside one, so a draft that
// names a file names nothing else.
func File(key string) Outgoing { return Outgoing{FileKey: key} }

// cardBody is the shape of the one-element card Card builds. Unexported so
// the JSON stays this package's own spelling of it.
type cardBody struct {
	Schema string `json:"schema"`
	Body   struct {
		Elements []struct {
			Tag     string `json:"tag"`
			Content string `json:"content"`
		} `json:"elements"`
	} `json:"body"`
}

// Card is an Outgoing carrying a 2.0 card whose one element is the markdown:
// the streaming answer's vehicle, posted once and rewritten whole.
func Card(md string) Outgoing {
	var c cardBody
	c.Schema = "2.0"
	c.Body.Elements = []struct {
		Tag     string `json:"tag"`
		Content string `json:"content"`
	}{{Tag: "markdown", Content: md}}
	b, _ := json.Marshal(c)
	return Outgoing{Card: string(b)}
}

// CardMarkdown reads the markdown back out of the card JSON Card builds, and
// of any card shaped like it — the renderer of a stored card walks the same
// element.
func CardMarkdown(cardJSON string) string {
	var c cardBody
	if json.Unmarshal([]byte(cardJSON), &c) != nil {
		return ""
	}
	for _, e := range c.Body.Elements {
		if e.Tag == "markdown" {
			return e.Content
		}
	}
	return ""
}

// Image is an Outgoing naming an already-uploaded image.
func Image(key string) Outgoing { return Outgoing{ImageKey: key} }

// imageKey is how Feishu spells the handle it gives an uploaded picture.
var imageKey = regexp.MustCompile(`^img_[A-Za-z0-9_-]+$`)

// IsImageKey reports whether ref names a picture Feishu already holds, which
// is what tells a key apart from a path that still has to be uploaded.
func IsImageKey(ref string) bool { return imageKey.MatchString(ref) }

// fileKey is how Feishu spells the handle it gives an uploaded file.
var fileKey = regexp.MustCompile(`^file_[A-Za-z0-9_-]+$`)

// IsFileKey reports whether ref names a file Feishu already holds.
func IsFileKey(ref string) bool { return fileKey.MatchString(ref) }

// Error is a failed lark-cli invocation decoded from its stderr envelope.
type Error struct {
	ExitCode   int
	Type       string
	Subtype    string
	Code       int
	Message    string
	Hint       string
	RetryAfter time.Duration
	// LogID is Feishu's server-side request id, the only handle that ties a
	// failure here to what the gateway recorded.
	LogID string
	// APICode and APIMessage are Feishu's own verdict, which reaches a
	// download failure only inside Message: lark-cli puts the HTTP status in
	// Code and leaves the raw body as text. APICode is the number the open
	// platform is searched with, so it is worth having on its own. Nothing
	// decides anything on it; see IsPermanent.
	APICode    int
	APIMessage string
	Stderr     string
	// Argv is the lark-cli call this came back from. It is what a crash
	// report is reproduced from, so it is kept whole, message body included.
	Argv []string
}

// withArgv records the call an error came back from, so a report carries the
// command that produced it rather than only its verdict.
func (e *Error) withArgv(argv []string) *Error {
	e.Argv = argv
	return e
}

func (e *Error) Error() string {
	switch {
	case e.APICode != 0:
		return fmt.Sprintf("lark-cli %s/%s (exit %d): %d %s", e.Type, e.Subtype, e.ExitCode, e.APICode, e.APIMessage)
	case e.Message != "":
		return fmt.Sprintf("lark-cli %s/%s (exit %d): %s", e.Type, e.Subtype, e.ExitCode, e.Message)
	}
	return fmt.Sprintf("lark-cli exit %d: %s", e.ExitCode, e.Stderr)
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

// IsPermanent reports a rejection that retrying the same request will not fix
// (permission, not found, unsupported chat type, a deleted attachment, …).
//
// Exit 1 carries Feishu's own business code. A resource download reports the
// HTTP status as a transport failure instead, so exit 4 with a client-error
// status is permanent too: a file key names fixed bytes, and 400, 404 and 410
// against them are the same answer every time. 401 and 403 are left out
// because a token midway through a refresh looks like both, and 408 and 429
// are the two 4xx that ask to be retried.
func (e *Error) IsPermanent() bool {
	if e.ExitCode == ExitAPI {
		return !e.IsRateLimit()
	}
	return e.ExitCode == ExitNetwork && permanentStatus[e.Code]
}

var permanentStatus = map[int]bool{
	http.StatusBadRequest: true,
	http.StatusNotFound:   true,
	http.StatusGone:       true,
}

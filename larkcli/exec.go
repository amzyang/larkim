package larkcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

// DefaultPath is where the npm-installed lark-cli wrapper lands on Homebrew
// macOS. launchd services get a minimal PATH, so the daemon cannot rely on
// lookup.
const DefaultPath = "/opt/homebrew/bin/lark-cli"

// extraPath is prepended to the child's PATH so the npm wrapper can find node
// even under launchd or a bare environment.
const extraPath = "/opt/homebrew/bin:/usr/local/bin"

// larkTimeLayout renders the local offset explicitly ("+00:00", never "Z"):
// messages/search forwards the string to the server verbatim.
const larkTimeLayout = "2006-01-02T15:04:05-07:00"

const (
	searchPageSize  = 50
	searchPageLimit = 40 // lark-cli's own cap for messages-search
	listPageSize    = 50
	chatsPageSize   = 100
	mgetBatch       = 50
)

// ExecClient runs the lark-cli binary as a subprocess. Calls are serialized:
// lark-cli refreshes the user token under a cross-process file lock, and the
// gateway rate limit is per user, so concurrency buys nothing.
type ExecClient struct {
	// Path to the lark-cli binary; empty means DefaultPath then $PATH lookup.
	Path string
	// Dir is the working directory; --download-resources writes below it.
	Dir string
	// Timeout bounds a single invocation including all auto-paginated pages.
	Timeout time.Duration

	mu sync.Mutex
}

// ResolvePath returns the binary that will be executed. The npm package
// installs a node wrapper script next to the real Go binary
// (lib/node_modules/@larksuite/cli/bin/lark-cli); the real binary is preferred
// because it needs no node on PATH and starts five times faster.
func (c *ExecClient) ResolvePath() (string, error) {
	if c.Path != "" {
		return realBinary(c.Path), nil
	}
	if _, err := os.Stat(DefaultPath); err == nil {
		return realBinary(DefaultPath), nil
	}
	p, err := exec.LookPath("lark-cli")
	if err != nil {
		return "", err
	}
	return realBinary(p), nil
}

// realBinary maps an npm wrapper path onto the Go binary it launches, when
// that layout is present; otherwise it returns path unchanged.
func realBinary(path string) string {
	resolved, err := filepath.EvalSymlinks(path)
	if err != nil {
		return path
	}
	// <prefix>/lib/node_modules/@larksuite/cli/scripts/run.js → ../bin/lark-cli
	if filepath.Base(resolved) == "run.js" {
		candidate := filepath.Join(filepath.Dir(resolved), "..", "bin", "lark-cli")
		if st, err := os.Stat(candidate); err == nil && st.Mode()&0o111 != 0 {
			return candidate
		}
	}
	return path
}

type envelope struct {
	OK       bool            `json:"ok"`
	Identity string          `json:"identity"`
	Data     json.RawMessage `json:"data"`
	Meta     json.RawMessage `json:"meta"`
	Error    *envelopeError  `json:"error"`
}

type envelopeError struct {
	Type              string `json:"type"`
	Subtype           string `json:"subtype"`
	Code              int    `json:"code"`
	Message           string `json:"message"`
	Hint              string `json:"hint"`
	RetryAfterSeconds int    `json:"retry_after_seconds"`
}

// run executes lark-cli with args plus the user identity and JSON output flags
// and returns the envelope's data.
func (c *ExecClient) run(ctx context.Context, args ...string) (json.RawMessage, error) {
	stdout, stderr, exitCode, err := c.exec(ctx, append(args, "--as", "user", "--json")...)
	if err != nil {
		return nil, err
	}
	if exitCode != 0 {
		return nil, decodeError(exitCode, stderr)
	}
	var env envelope
	if err := json.Unmarshal(stdout, &env); err != nil {
		return nil, fmt.Errorf("lark-cli: decode stdout: %w (%s)", err, truncate(string(stdout), 200))
	}
	if !env.OK {
		return nil, decodeError(exitCode, stdout)
	}
	return env.Data, nil
}

func (c *ExecClient) exec(ctx context.Context, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	path, err := c.ResolvePath()
	if err != nil {
		return nil, nil, 0, fmt.Errorf("lark-cli not found: %w", err)
	}
	timeout := c.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	c.mu.Lock()
	defer c.mu.Unlock()

	cmd := exec.CommandContext(ctx, path, args...)
	cmd.Dir = c.Dir
	cmd.Env = append(childEnv(os.Environ()), "NO_COLOR=1")
	// The npm wrapper is a node process that spawns the real binary; kill the
	// whole group so a cancelled call does not leave the child running.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }
	cmd.WaitDelay = 2 * time.Second
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	runErr := cmd.Run()
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			return out.Bytes(), errBuf.Bytes(), exitErr.ExitCode(), nil
		}
		if ctx.Err() != nil {
			runErr = ctx.Err()
		}
		return nil, nil, 0, fmt.Errorf("lark-cli %s: %w", args[0], runErr)
	}
	return out.Bytes(), errBuf.Bytes(), 0, nil
}

// childEnv prepends extraPath to PATH (or sets one) for the subprocess.
func childEnv(env []string) []string {
	out := make([]string, 0, len(env)+1)
	found := false
	for _, kv := range env {
		if strings.HasPrefix(kv, "PATH=") {
			out = append(out, "PATH="+extraPath+":"+strings.TrimPrefix(kv, "PATH="))
			found = true
			continue
		}
		out = append(out, kv)
	}
	if !found {
		out = append(out, "PATH="+extraPath+":/usr/bin:/bin")
	}
	return out
}

// decodeError finds the JSON error envelope in stderr; lark-cli prints
// progress lines such as "[page 1] fetching..." before it and pretty-prints
// the envelope over several lines.
func decodeError(exitCode int, stderr []byte) *Error {
	e := &Error{ExitCode: exitCode, Stderr: string(stderr)}
	for off := 0; off < len(stderr); {
		nl := bytes.IndexByte(stderr[off:], '\n')
		lineEnd := len(stderr)
		if nl >= 0 {
			lineEnd = off + nl
		}
		line := bytes.TrimSpace(stderr[off:lineEnd])
		if bytes.HasPrefix(line, []byte("{")) {
			var env envelope
			if json.NewDecoder(bytes.NewReader(stderr[off:])).Decode(&env) == nil && env.Error != nil {
				e.Type = env.Error.Type
				e.Subtype = env.Error.Subtype
				e.Code = env.Error.Code
				e.Message = env.Error.Message
				e.Hint = env.Error.Hint
				e.RetryAfter = time.Duration(env.Error.RetryAfterSeconds) * time.Second
			}
		}
		off = lineEnd + 1
	}
	return e
}

func jsonArg(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}

func (c *ExecClient) SearchMessageIDs(ctx context.Context, start, end time.Time) ([]SearchHit, bool, error) {
	body := map[string]any{
		"query": "",
		"filter": map[string]any{
			"time_range": map[string]string{
				"start_time": start.Format(larkTimeLayout),
				"end_time":   end.Format(larkTimeLayout),
			},
		},
	}
	data, err := c.run(ctx, "api", "POST", "/open-apis/im/v1/messages/search",
		"--data", jsonArg(body),
		"--page-size", strconv.Itoa(searchPageSize),
		"--page-all", "--page-limit", strconv.Itoa(searchPageLimit))
	if err != nil {
		return nil, false, err
	}
	var resp struct {
		Items []struct {
			Meta struct {
				MessageID  string `json:"message_id"`
				ChatID     string `json:"chat_id"`
				FromID     string `json:"from_id"`
				ThreadID   string `json:"thread_id"`
				Type       string `json:"type"`
				IsP2P      bool   `json:"is_p2p_chat"`
				Position   int64  `json:"position"`
				CreateTime string `json:"create_time"`
			} `json:"meta_data"`
		} `json:"items"`
		HasMore bool `json:"has_more"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, false, fmt.Errorf("decode search: %w", err)
	}
	hits := make([]SearchHit, 0, len(resp.Items))
	for _, it := range resp.Items {
		m := it.Meta
		ct, _ := time.Parse(time.RFC3339, m.CreateTime)
		hits = append(hits, SearchHit{
			MessageID: m.MessageID, ChatID: m.ChatID, FromID: m.FromID, ThreadID: m.ThreadID,
			Type: m.Type, IsP2P: m.IsP2P, Position: m.Position, CreateTime: ct,
		})
	}
	return hits, resp.HasMore, nil
}

func (c *ExecClient) MGetRaw(ctx context.Context, ids []string) ([]RawMessage, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	params := map[string]any{"message_ids": ids, "with_sender_name": true}
	data, err := c.run(ctx, "api", "GET", "/open-apis/im/v1/messages/mget", "--params", jsonArg(params))
	if err != nil {
		return nil, err
	}
	return decodeItems[RawMessage](data, "items")
}

func (c *ExecClient) ListMessagesRaw(ctx context.Context, containerType, containerID string, start, end time.Time) ([]RawMessage, error) {
	params := map[string]any{
		"container_id_type":     containerType,
		"container_id":          containerID,
		"sort_type":             "ByCreateTimeAsc",
		"page_size":             listPageSize,
		"with_sender_name":      true,
		"card_msg_content_type": "raw_card_content",
	}
	if !start.IsZero() {
		params["start_time"] = strconv.FormatInt(start.Unix(), 10)
	}
	if !end.IsZero() {
		params["end_time"] = strconv.FormatInt(end.Unix(), 10)
	}
	data, err := c.run(ctx, "api", "GET", "/open-apis/im/v1/messages", "--params", jsonArg(params),
		"--page-all", "--page-limit", "0")
	if err != nil {
		return nil, err
	}
	return decodeItems[RawMessage](data, "items")
}

// rawKeeper is implemented by decoded items that retain their source JSON.
type rawKeeper interface{ keepRaw(json.RawMessage) }

func (m *RawMessage) keepRaw(r json.RawMessage)      { m.Raw = r }
func (c *RawChat) keepRaw(r json.RawMessage)         { c.Raw = r }
func (m *RenderedMessage) keepRaw(r json.RawMessage) { m.Raw = r }

// decodeItems decodes the list under data[key], keeping each item's raw JSON.
func decodeItems[T any, PT interface {
	*T
	rawKeeper
}](data json.RawMessage, key string) ([]T, error) {
	var resp map[string]json.RawMessage
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode %s: %w", key, err)
	}
	var items []json.RawMessage
	if raw, ok := resp[key]; ok {
		if err := json.Unmarshal(raw, &items); err != nil {
			return nil, fmt.Errorf("decode %s: %w", key, err)
		}
	}
	out := make([]T, 0, len(items))
	for _, raw := range items {
		var v T
		if err := json.Unmarshal(raw, &v); err != nil {
			return nil, fmt.Errorf("decode %s item: %w", key, err)
		}
		PT(&v).keepRaw(raw)
		out = append(out, v)
	}
	return out, nil
}

func (c *ExecClient) ListChats(ctx context.Context, activeFirstPage bool) ([]RawChat, error) {
	params := map[string]any{
		"types":        "p2p,group",
		"page_size":    chatsPageSize,
		"user_id_type": "open_id",
	}
	args := []string{"api", "GET", "/open-apis/im/v1/chats"}
	if activeFirstPage {
		params["sort_type"] = "ByActiveTimeDesc"
	}
	args = append(args, "--params", jsonArg(params))
	if !activeFirstPage {
		args = append(args, "--page-all", "--page-limit", "0")
	}
	data, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return decodeItems[RawChat](data, "items")
}

func (c *ExecClient) MGetRendered(ctx context.Context, ids []string, download bool) ([]RenderedMessage, error) {
	if len(ids) == 0 {
		return nil, nil
	}
	args := []string{"im", "+messages-mget", "--message-ids", strings.Join(ids, ",")}
	if download {
		args = append(args, "--download-resources")
	}
	data, err := c.run(ctx, args...)
	if err != nil {
		return nil, err
	}
	return decodeItems[RenderedMessage](data, "messages")
}

func (c *ExecClient) ReadStatus(ctx context.Context, ids []string) ([]ReadStatus, []string, error) {
	if len(ids) == 0 {
		return nil, nil, nil
	}
	data, err := c.run(ctx, "im", "+messages-read-status", "--message-ids", strings.Join(ids, ","))
	if err != nil {
		return nil, nil, err
	}
	var resp struct {
		Items   []ReadStatus `json:"items"`
		Invalid []string     `json:"invalid_message_ids"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, nil, fmt.Errorf("decode read status: %w", err)
	}
	return resp.Items, resp.Invalid, nil
}

func (c *ExecClient) ChatMembers(ctx context.Context, chatID string) ([]ChatMember, error) {
	params := map[string]any{"member_id_type": "open_id", "page_size": 100}
	data, err := c.run(ctx, "api", "GET", "/open-apis/im/v1/chats/"+chatID+"/members",
		"--params", jsonArg(params), "--page-all", "--page-limit", "0")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []ChatMember `json:"items"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode members: %w", err)
	}
	return resp.Items, nil
}

// MaxUserIDsPerSearch is how many ids one `+search-user --user-ids` call
// resolves. The flag accepts 100, but the server answers at most this many and
// sets has_more, and the shortcut has no pagination.
const MaxUserIDsPerSearch = 20

func (c *ExecClient) SearchUsers(ctx context.Context, query string, ids []string) ([]User, error) {
	if len(ids) == 0 {
		return c.searchUsers(ctx, "--query", query)
	}
	var out []User
	for start := 0; start < len(ids); start += MaxUserIDsPerSearch {
		batch := ids[start:min(start+MaxUserIDsPerSearch, len(ids))]
		users, err := c.searchUsers(ctx, "--user-ids", strings.Join(batch, ","))
		if err != nil {
			return nil, err
		}
		out = append(out, users...)
	}
	return out, nil
}

func (c *ExecClient) searchUsers(ctx context.Context, flag, value string) ([]User, error) {
	data, err := c.run(ctx, "contact", "+search-user", flag, value)
	if err != nil {
		return nil, err
	}
	var resp struct {
		Users []User `json:"users"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode users: %w", err)
	}
	return resp.Users, nil
}

// MaxUserDetailsBatch is the documented cap of contact/v3/users/batch.
const MaxUserDetailsBatch = 50

func (c *ExecClient) UserDetails(ctx context.Context, openIDs []string) ([]UserDetail, error) {
	var out []UserDetail
	for start := 0; start < len(openIDs); start += MaxUserDetailsBatch {
		batch := openIDs[start:min(start+MaxUserDetailsBatch, len(openIDs))]
		// user_ids has to repeat as a query parameter; a comma-joined string
		// reads as one malformed id and the whole call comes back empty.
		data, err := c.run(ctx, "api", "GET", "/open-apis/contact/v3/users/batch",
			"--params", jsonArg(map[string]any{"user_ids": batch, "user_id_type": "open_id"}))
		if err != nil {
			return nil, err
		}
		var resp struct {
			Items []struct {
				OpenID string `json:"open_id"`
				Name   string `json:"name"`
				Avatar struct {
					Avatar240 string `json:"avatar_240"`
				} `json:"avatar"`
			} `json:"items"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("decode users: %w", err)
		}
		for _, it := range resp.Items {
			out = append(out, UserDetail{OpenID: it.OpenID, Name: it.Name, AvatarURL: it.Avatar.Avatar240})
		}
	}
	return out, nil
}

func (c *ExecClient) SearchChats(ctx context.Context, query string) ([]RawChat, error) {
	data, err := c.run(ctx, "im", "+chat-search", "--query", query)
	if err != nil {
		return nil, err
	}
	return decodeItems[RawChat](data, "chats")
}

func (c *ExecClient) SendText(ctx context.Context, target Target, text, idempotencyKey string) (SentMessage, error) {
	args := []string{"im", "+messages-send", "--text", text}
	if target.ChatID != "" {
		args = append(args, "--chat-id", target.ChatID)
	} else {
		args = append(args, "--user-id", target.UserID)
	}
	if idempotencyKey != "" {
		args = append(args, "--idempotency-key", idempotencyKey)
	}
	return c.sent(ctx, args...)
}

func (c *ExecClient) ReplyText(ctx context.Context, messageID, text string, inThread bool, idempotencyKey string) (SentMessage, error) {
	args := []string{"im", "+messages-reply", "--message-id", messageID, "--text", text}
	if inThread {
		args = append(args, "--reply-in-thread")
	}
	if idempotencyKey != "" {
		args = append(args, "--idempotency-key", idempotencyKey)
	}
	return c.sent(ctx, args...)
}

func (c *ExecClient) sent(ctx context.Context, args ...string) (SentMessage, error) {
	data, err := c.run(ctx, args...)
	if err != nil {
		return SentMessage{}, err
	}
	var s SentMessage
	if err := json.Unmarshal(data, &s); err != nil {
		return SentMessage{}, fmt.Errorf("decode send result: %w", err)
	}
	return s, nil
}

// Whoami parses `lark-cli whoami --json`, which is not enveloped.
func (c *ExecClient) Whoami(ctx context.Context) (Identity, error) {
	stdout, stderr, exitCode, err := c.exec(ctx, "whoami", "--json")
	if err != nil {
		return Identity{}, err
	}
	if exitCode != 0 {
		return Identity{}, decodeError(exitCode, stderr)
	}
	var resp struct {
		AppID      string `json:"appId"`
		Identity   string `json:"identity"`
		Available  bool   `json:"available"`
		OnBehalfOf struct {
			OpenID string `json:"openId"`
		} `json:"onBehalfOf"`
	}
	if err := json.Unmarshal(stdout, &resp); err != nil {
		return Identity{}, fmt.Errorf("decode whoami: %w", err)
	}
	if resp.Identity != "user" || !resp.Available {
		return Identity{}, &Error{ExitCode: ExitAuth, Type: "auth", Subtype: "token_missing",
			Message: "user identity unavailable; run: lark-cli auth login"}
	}
	return Identity{AppID: resp.AppID, UserOpenID: resp.OnBehalfOf.OpenID}, nil
}

var _ Client = (*ExecClient)(nil)

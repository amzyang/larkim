package larkcli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
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

// resourceSubdir is the directory lark-cli's own batch download writes below
// the client's Dir; a single download is aimed at it so both land together.
const resourceSubdir = "lark-im-resources"

const (
	searchPageSize  = 50
	searchPageLimit = 40 // lark-cli's own cap for messages-search
	listPageSize    = 50
	chatsPageSize   = 100
	mgetBatch       = 50
)

// ExecClient runs the lark-cli binary as a subprocess. Calls run in three
// lanes — the syncer's sweeps, the open chat's beat, and what a person pressed
// a key for — so neither a sweep nor a beat is ever in front of a keystroke.
// The widths are also the only throttle in front of the gateway: lark-cli
// carries no limiter of its own.
type ExecClient struct {
	// Path to the lark-cli binary; empty means DefaultPath then $PATH lookup.
	Path string
	// Dir is the working directory; --download-resources writes below it.
	Dir string
	// Timeout bounds a single invocation including all auto-paginated pages.
	// It starts once the call holds a lane, so time spent queued behind
	// another call is not charged to it.
	Timeout time.Duration
	// Log records every call: failures always, the full request and response
	// pair at debug level. Nil discards.
	Log *slog.Logger

	once         sync.Once
	bg, beat, fg lane
	// calls numbers the invocations so a request line and its response line
	// can be paired, which the lanes make necessary: several calls are in
	// flight at once and their lines interleave.
	calls atomic.Uint64
}

func (c *ExecClient) logger() *slog.Logger {
	if c.Log == nil {
		return slog.New(slog.DiscardHandler)
	}
	return c.Log
}

// laneFor exposes one line to tests, which hold it occupied to see what the
// other lines do about it.
func (c *ExecClient) laneFor(l Lane) lane { return c.lane(WithLane(context.Background(), l)) }

func (c *ExecClient) lanes() {
	c.once.Do(func() {
		c.bg = make(lane, backgroundLane)
		c.beat = make(lane, beatLane)
		c.fg = make(lane, interactiveLane)
	})
}

// lane picks the line this call waits in.
func (c *ExecClient) lane(ctx context.Context) lane {
	c.lanes()
	switch LaneOf(ctx) {
	case LaneInteractive:
		return c.fg
	case LaneBeat:
		return c.beat
	default:
		return c.bg
	}
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
	LogID             string `json:"log_id"`
	RetryAfterSeconds int    `json:"retry_after_seconds"`
}

// run executes lark-cli with args plus the user identity and JSON output flags
// and returns the envelope's data.
func (c *ExecClient) run(ctx context.Context, args ...string) (json.RawMessage, error) {
	return c.runAs(ctx, "user", args...)
}

// runAs is run with an explicit identity. Contact reads go as the app, whose
// directory scope is the tenant-wide one; the user identity sees only what
// the signed-in person sees.
func (c *ExecClient) runAs(ctx context.Context, identity string, args ...string) (json.RawMessage, error) {
	stdout, stderr, exitCode, err := c.exec(ctx, append(args, "--as", identity, "--json")...)
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
		// A refusal that lark-cli reported on stdout with a zero exit is the
		// one failure exec cannot see, so it is logged here instead.
		e := decodeError(exitCode, stdout)
		c.logger().WarnContext(ctx, "lark-cli refused", "argv", ArgvLine(args), "code", e.Code, "error", e.Message, "log_id", e.LogID)
		return nil, e
	}
	return env.Data, nil
}

func (c *ExecClient) exec(ctx context.Context, args ...string) (stdout, stderr []byte, exitCode int, err error) {
	call := c.calls.Add(1)
	path, err := c.ResolvePath()
	if err != nil {
		return nil, nil, 0, c.logFailure(ctx, call, args, fmt.Errorf("lark-cli not found: %w", err))
	}
	queued := time.Now()
	l := c.lane(ctx)
	if aerr := l.acquire(ctx); aerr != nil {
		return nil, nil, 0, c.logFailure(ctx, call, args, fmt.Errorf("lark-cli %s: %w", args[0], aerr))
	}
	defer l.release()
	c.logRequest(ctx, call, args, LaneOf(ctx), time.Since(queued))

	timeout := c.Timeout
	if timeout == 0 {
		timeout = 5 * time.Minute
	}
	// The timeout bounds the run, not the wait: a call that sat behind a
	// sweep still gets its whole budget once it reaches the front.
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

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
	started := time.Now()
	runErr := cmd.Run()
	dur := time.Since(started)
	// One exit, so the response line is emitted exactly once.
	var exitErr *exec.ExitError
	switch {
	case runErr == nil:
		stdout, stderr = out.Bytes(), errBuf.Bytes()
	case errors.As(runErr, &exitErr):
		stdout, stderr, exitCode = out.Bytes(), errBuf.Bytes(), exitErr.ExitCode()
	default:
		if ctx.Err() != nil {
			runErr = ctx.Err()
		}
		err = fmt.Errorf("lark-cli %s: %w", args[0], runErr)
	}
	c.logResponse(ctx, call, args, dur, stdout, stderr, exitCode, err)
	return stdout, stderr, exitCode, err
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
				e.LogID = env.Error.LogID
				e.RetryAfter = time.Duration(env.Error.RetryAfterSeconds) * time.Second
			}
		}
		off = lineEnd + 1
	}
	e.adoptAPIBody()
	return e
}

// adoptAPIBody lifts Feishu's own code out of Message. A download is served
// outside the API envelope, so lark-cli reports its failure as a transport
// error whose message is the raw HTTP body ("HTTP 400: {...}") — which is
// where the code that names the cause stays. A body truncated by lark-cli
// does not parse, and then there is nothing to lift.
func (e *Error) adoptAPIBody() {
	i := strings.IndexByte(e.Message, '{')
	if i < 0 {
		return
	}
	var body struct {
		Code  int    `json:"code"`
		Msg   string `json:"msg"`
		Error struct {
			LogID string `json:"log_id"`
		} `json:"error"`
	}
	if json.NewDecoder(strings.NewReader(e.Message[i:])).Decode(&body) != nil || body.Code == 0 {
		return
	}
	e.APICode, e.APIMessage = body.Code, body.Msg
	if e.LogID == "" {
		e.LogID = body.Error.LogID
	}
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
	return decodeSearchHits(data)
}

// SearchMessages finds messages by keyword across every chat. SearchMessageIDs
// sweeps a time window on the syncer's behalf; this is the reader's own query,
// so it asks for one page and waits for nobody. The same endpoint answers
// both, and it is the one that dates a hit to the second — `im
// +messages-search` renders create_time for a person to read, which is too
// coarse to put a cursor on.
func (c *ExecClient) SearchMessages(ctx context.Context, query string, limit int) ([]SearchHit, error) {
	if strings.TrimSpace(query) == "" {
		return nil, nil
	}
	if limit <= 0 || limit > searchPageSize {
		limit = searchPageSize
	}
	data, err := c.run(ctx, "api", "POST", "/open-apis/im/v1/messages/search",
		"--data", jsonArg(map[string]any{"query": query}),
		"--page-size", strconv.Itoa(limit))
	if err != nil {
		return nil, err
	}
	hits, _, err := decodeSearchHits(data)
	return hits, err
}

func decodeSearchHits(data json.RawMessage) ([]SearchHit, bool, error) {
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
	// Without card_msg_content_type an interactive message comes back as a
	// placeholder telling the reader to upgrade their client, which carries
	// neither the card nor its attachment table. ListMessagesRaw asks for the
	// real body; a card first seen by id has to arrive the same way.
	params := map[string]any{"message_ids": ids, "with_sender_name": true, "card_msg_content_type": "raw_card_content"}
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

// DownloadResource fetches one attachment on its own. It lands beside the
// batch download's files and under the same name, so a reader holding either
// kind of path finds them in one place.
func (c *ExecClient) DownloadResource(ctx context.Context, messageID, fileKey, typ string) (Resource, error) {
	data, err := c.run(ctx, "im", "+messages-resources-download", "--message-id", messageID,
		"--file-key", fileKey, "--type", typ, "--output", resourceSubdir+"/"+fileKey)
	if err != nil {
		return Resource{}, err
	}
	var saved struct {
		Path string `json:"saved_path"`
		Size int64  `json:"size_bytes"`
	}
	if err := json.Unmarshal(data, &saved); err != nil {
		return Resource{}, fmt.Errorf("decode download: %w", err)
	}
	return Resource{MessageID: messageID, Key: fileKey, Type: typ, LocalPath: saved.Path, SizeBytes: saved.Size}, nil
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

// ChatMembers reads a chat's roster through the shortcut rather than the raw
// members endpoint, which answers with users alone: a bot only ever sees the
// message that names it, so a roster without the bots cannot be completed
// against. No --page-size, because --page-all already asks for the largest.
func (c *ExecClient) ChatMembers(ctx context.Context, chatID string) ([]ChatMember, bool, error) {
	data, err := c.run(ctx, "im", "+chat-members-list", "--chat-id", chatID,
		"--member-types", "user,bot", "--member-id-type", "open_id",
		"--page-all", "--page-limit", "0")
	if err != nil {
		return nil, false, err
	}
	var resp struct {
		Users []ChatMember `json:"users"`
		Bots  []ChatMember `json:"bots"`
		// Truncations names each bucket the tenant's security config capped.
		// It is why this shortcut exists: the roster it answers with looks
		// complete, and a caller that drops this cannot tell that it is not.
		Truncations []struct{} `json:"truncations"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, false, fmt.Errorf("decode members: %w", err)
	}
	for i := range resp.Bots {
		resp.Bots[i].IsBot = true
	}
	return append(resp.Users, resp.Bots...), len(resp.Truncations) > 0, nil
}

// MaxChatIDsPerMuteCall is the upstream cap on one mute lookup.
const MaxChatIDsPerMuteCall = 100

// MuteStatus reads do-not-disturb, which no chat listing carries and lark-cli
// only exposes as a filter, so it goes through the raw API. The setting
// belongs to the signed-in user; under bot identity the endpoint has no data
// at all, which is why this rides the user identity every other call uses.
func (c *ExecClient) MuteStatus(ctx context.Context, chatIDs []string) (map[string]bool, []string, error) {
	muted := map[string]bool{}
	var unknown []string
	for start := 0; start < len(chatIDs); start += MaxChatIDsPerMuteCall {
		batch := chatIDs[start:min(start+MaxChatIDsPerMuteCall, len(chatIDs))]
		data, err := c.run(ctx, "api", "POST", "/open-apis/im/v1/chat_user_setting/batch_get_mute_status",
			"--data", jsonArg(map[string]any{"chat_ids": batch}))
		if err != nil {
			return nil, nil, err
		}
		var resp struct {
			Items []struct {
				ChatID  string `json:"chat_id"`
				IsMuted bool   `json:"is_muted"`
			} `json:"items"`
			Invalid []struct {
				ID string `json:"id"`
			} `json:"invalid_id_list"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, nil, fmt.Errorf("decode mute status: %w", err)
		}
		for _, it := range resp.Items {
			muted[it.ChatID] = it.IsMuted
		}
		for _, it := range resp.Invalid {
			unknown = append(unknown, it.ID)
		}
	}
	return muted, unknown, nil
}

// MaxMessageIDsPerReactionCall is the upstream cap on one reaction lookup.
const MaxMessageIDsPerReactionCall = 20

// reactionsPerMessage is how many individual reactions one message's answer
// carries. Ten is the server's ceiling — eleven is rejected outright — and it
// is why the totals are read off the counts rather than counted here: a
// popular message has more reactors than one page names.
const reactionsPerMessage = 10

// ReactionCounts asks Feishu who reacted to each message. Every requested id
// gets an entry: a message whose reactions were all taken back answers with
// nothing, and the caller has to clear the summary it holds rather than keep
// showing one Feishu no longer has.
//
// The result is the same `{counts, details}` block the message-pulling
// shortcuts attach, so the store keeps one shape and every reader one decoder.
// The individual reactions are passed through as they came, because Feishu
// carries the reaction id there and only there.
func (c *ExecClient) ReactionCounts(ctx context.Context, messageIDs []string) (map[string]json.RawMessage, error) {
	out := make(map[string]json.RawMessage, len(messageIDs))
	for _, id := range messageIDs {
		out[id] = nil
	}
	for start := 0; start < len(messageIDs); start += MaxMessageIDsPerReactionCall {
		batch := messageIDs[start:min(start+MaxMessageIDsPerReactionCall, len(messageIDs))]
		queries := make([]map[string]string, 0, len(batch))
		for _, id := range batch {
			queries = append(queries, map[string]string{"message_id": id})
		}
		data, err := c.run(ctx, "api", "POST", "/open-apis/im/v1/messages/reactions/batch_query",
			"--data", jsonArg(map[string]any{"queries": queries, "page_size_per_message": reactionsPerMessage}))
		if err != nil {
			return nil, err
		}
		var resp struct {
			Counts []struct {
				MessageID string            `json:"message_id"`
				Items     []json.RawMessage `json:"reaction_count"`
			} `json:"success_msg_reaction_counts"`
			Details []struct {
				MessageID string            `json:"message_id"`
				Items     []json.RawMessage `json:"message_reaction_items"`
			} `json:"success_msg_reaction_details"`
		}
		if err := json.Unmarshal(data, &resp); err != nil {
			return nil, fmt.Errorf("decode reactions: %w", err)
		}
		details := make(map[string][]json.RawMessage, len(resp.Details))
		for _, d := range resp.Details {
			details[d.MessageID] = d.Items
		}
		for _, c := range resp.Counts {
			if len(c.Items) == 0 {
				continue
			}
			block, err := json.Marshal(map[string]any{"counts": c.Items, "details": details[c.MessageID]})
			if err != nil {
				return nil, err
			}
			out[c.MessageID] = block
		}
	}
	return out, nil
}

// Reaction is one person's reaction to one message. ReactionID is the only
// handle that deletes it, and Feishu hands it out nowhere but reactions.list —
// the block the message-pulling shortcuts attach leaves it out.
type Reaction struct {
	ReactionID string `json:"reaction_id"`
	EmojiType  string `json:"emoji_type"`
	OperatorID string `json:"operator_id"`
}

// AddReaction puts one emoji on a message under the user's own name. Feishu
// checks the emoji_type against its own list and answers 231001 for anything
// else, and the check is case-sensitive: the key has to carry the spelling
// the client ships it under, not a folded one.
func (c *ExecClient) AddReaction(ctx context.Context, messageID, emojiType string) (Reaction, error) {
	data, err := c.run(ctx, "api", "POST", "/open-apis/im/v1/messages/"+messageID+"/reactions",
		"--data", jsonArg(map[string]any{"reaction_type": map[string]string{"emoji_type": emojiType}}))
	if err != nil {
		return Reaction{}, err
	}
	var resp struct {
		ReactionID   string `json:"reaction_id"`
		ReactionType struct {
			EmojiType string `json:"emoji_type"`
		} `json:"reaction_type"`
		Operator struct {
			OperatorID string `json:"operator_id"`
		} `json:"operator"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return Reaction{}, fmt.Errorf("decode reaction: %w", err)
	}
	return Reaction{ReactionID: resp.ReactionID, EmojiType: resp.ReactionType.EmojiType,
		OperatorID: resp.Operator.OperatorID}, nil
}

// ListReactions lists who reacted to a message with one emoji. It exists for
// the sake of taking a reaction back: the delete needs a reaction id, and this
// is the only call that gives one for a reaction this process did not make.
func (c *ExecClient) ListReactions(ctx context.Context, messageID, emojiType string) ([]Reaction, error) {
	data, err := c.run(ctx, "im", "reactions", "list", "--message-id", messageID,
		"--reaction-type", emojiType, "--page-size", "50")
	if err != nil {
		return nil, err
	}
	var resp struct {
		Items []struct {
			ReactionID   string `json:"reaction_id"`
			ReactionType struct {
				EmojiType string `json:"emoji_type"`
			} `json:"reaction_type"`
			Operator struct {
				OperatorID string `json:"operator_id"`
			} `json:"operator"`
		} `json:"items"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return nil, fmt.Errorf("decode reactions: %w", err)
	}
	out := make([]Reaction, 0, len(resp.Items))
	for _, it := range resp.Items {
		out = append(out, Reaction{ReactionID: it.ReactionID, EmojiType: it.ReactionType.EmojiType,
			OperatorID: it.Operator.OperatorID})
	}
	return out, nil
}

// DeleteReaction takes one reaction back. Feishu only lets an identity delete
// what it added, so a reaction id belonging to somebody else fails here rather
// than removing their reaction.
func (c *ExecClient) DeleteReaction(ctx context.Context, messageID, reactionID string) error {
	_, err := c.run(ctx, "api", "DELETE", "/open-apis/im/v1/messages/"+messageID+"/reactions/"+reactionID)
	return err
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

// AppDetail reads one app as the app itself, which is the only identity the
// endpoint accepts. Reaching an app other than this one needs
// admin:app.info:readonly; without it the server answers 210508.
func (c *ExecClient) AppDetail(ctx context.Context, appID string) (AppDetail, error) {
	data, err := c.runAs(ctx, "bot", "api", "GET", "/open-apis/application/v6/applications/"+appID,
		"--params", jsonArg(map[string]string{"lang": "zh_cn"}))
	if err != nil {
		return AppDetail{}, err
	}
	var resp struct {
		App struct {
			AppID     string `json:"app_id"`
			AppName   string `json:"app_name"`
			AvatarURL string `json:"avatar_url"`
		} `json:"app"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return AppDetail{}, fmt.Errorf("decode app: %w", err)
	}
	return AppDetail{AppID: resp.App.AppID, Name: resp.App.AppName, AvatarURL: resp.App.AvatarURL}, nil
}

// MaxDocTokensPerBatch is the documented cap of drive/v1/metas/batch_query.
const MaxDocTokensPerBatch = 200

// DocTitles reads document metadata as the user, not the app: a title is
// worth showing only if the reader could open the document, and the app's
// own reach over the tenant's drive is neither the same set nor a subset.
// A /wiki/ token needs no unwrapping first — the endpoint takes doc_type
// "wiki" and answers with the document the node holds.
func (c *ExecClient) DocTitles(ctx context.Context, refs []DocRef) (DocTitles, error) {
	if len(refs) == 0 {
		return DocTitles{}, nil
	}
	data, err := c.runAs(ctx, "user", "api", "POST", "/open-apis/drive/v1/metas/batch_query",
		"--data", jsonArg(map[string]any{"request_docs": refs}))
	if err != nil {
		return DocTitles{}, err
	}
	var resp struct {
		Metas []struct {
			DocType string `json:"doc_type"`
			Title   string `json:"title"`
			// Asked echoes the request, which is the only way back to a wiki
			// node: doc_token beside it is the document the node resolved to.
			Asked DocRef `json:"request_doc_info"`
		} `json:"metas"`
		Failed []struct {
			Token string `json:"token"`
			Code  int    `json:"code"`
		} `json:"failed_list"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return DocTitles{}, fmt.Errorf("decode doc titles: %w", err)
	}
	out := DocTitles{Found: make([]DocTitle, 0, len(resp.Metas))}
	for _, m := range resp.Metas {
		out.Found = append(out.Found, DocTitle{Ref: m.Asked, Type: m.DocType, Title: m.Title})
	}
	// failed_list names the token that was asked for, not the type it was
	// asked under, so the request is what says which document it was.
	byToken := make(map[string]DocRef, len(refs))
	for _, r := range refs {
		byToken[r.Token] = r
	}
	for _, f := range resp.Failed {
		if ref, ok := byToken[f.Token]; ok {
			out.Denied = append(out.Denied, ref)
		}
	}
	return out, nil
}

// MaxUserDetailsBatch is the documented cap of contact/v3/users/batch.
const MaxUserDetailsBatch = 50

func (c *ExecClient) UserDetails(ctx context.Context, openIDs []string) ([]UserDetail, error) {
	var out []UserDetail
	for start := 0; start < len(openIDs); start += MaxUserDetailsBatch {
		batch := openIDs[start:min(start+MaxUserDetailsBatch, len(openIDs))]
		// user_ids has to repeat as a query parameter; a comma-joined string
		// reads as one malformed id and the whole call comes back empty.
		// As the app, not the user: the app's directory scope covers the whole
		// tenant, while a user token returns only the colleagues that person
		// can see.
		data, err := c.runAs(ctx, "bot", "api", "GET", "/open-apis/contact/v3/users/batch",
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

// flags is the content flag the body asks for. Send and Reply share it so the
// two cannot drift apart on which field wins.
func (o Outgoing) flags() []string {
	switch {
	case o.Markdown != "":
		// Not --markdown: that flag rewrites H1-H3 into H4/H5 before sending,
		// and the rewrite lands in what this client stores and draws.
		return []string{"--msg-type", "post", "--content", postContent(o.Markdown)}
	case o.ImageKey != "":
		return []string{"--image", o.ImageKey}
	case o.FileKey != "":
		return []string{"--file", o.FileKey}
	default:
		return []string{"--text", o.Text}
	}
}

func (c *ExecClient) Send(ctx context.Context, target Target, msg Outgoing, idempotencyKey string) (SentMessage, error) {
	args := append([]string{"im", "+messages-send"}, msg.flags()...)
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

func (c *ExecClient) Reply(ctx context.Context, messageID string, msg Outgoing, inThread bool, idempotencyKey string) (SentMessage, error) {
	args := append([]string{"im", "+messages-reply", "--message-id", messageID}, msg.flags()...)
	if inThread {
		args = append(args, "--reply-in-thread")
	}
	if idempotencyKey != "" {
		args = append(args, "--idempotency-key", idempotencyKey)
	}
	return c.sent(ctx, args...)
}

// postContent is the rich-text body Feishu stores for a markdown draft: one md
// element per paragraph, with an empty text element standing in for every
// blank line between them. Feishu expands an md element into paragraphs of its
// own and drops the blank lines inside it, and an empty paragraph sent as an
// empty array is stripped on the way in, so a line carrying an empty text
// element is the only spelling of a gap that survives the round trip.
func postContent(markdown string) string {
	var b strings.Builder
	b.WriteString(`{"zh_cn":{"content":[`)
	for i, para := range postParagraphs(markdown) {
		if i > 0 {
			b.WriteByte(',')
		}
		if para == "" {
			b.WriteString(`[{"tag":"text","text":""}]`)
			continue
		}
		text, _ := json.Marshal(para)
		b.WriteString(`[{"tag":"md","text":` + string(text) + `}]`)
	}
	b.WriteString(`]}}`)
	return b.String()
}

// mdFenceLine opens or closes a fenced code block. The composer has a fence of
// its own to classify drafts by; this one is not shared with it because the
// two answer different questions and neither is worth a package.
var mdFenceLine = regexp.MustCompile("^ {0,3}(`{3,}|~{3,})")

// postParagraphs cuts a markdown body at the blank lines between its blocks,
// which come back as empty strings. A blank line inside a fence is code, so
// the fence stays whole, and the blank lines around the body are dropped:
// nobody typed a gap there.
func postParagraphs(markdown string) []string {
	var out, cur []string
	var fence string
	flush := func() {
		if len(cur) > 0 {
			out = append(out, strings.Join(cur, "\n"))
			cur = nil
		}
	}
	for _, line := range strings.Split(markdown, "\n") {
		switch {
		case fence != "":
			cur = append(cur, line)
			// A closing fence is at least as long as the one that opened the
			// block, which is what the prefix test comes to.
			if strings.HasPrefix(strings.TrimSpace(line), fence) {
				fence = ""
			}
		case mdFenceLine.MatchString(line):
			cur = append(cur, line)
			fence = mdFenceLine.FindStringSubmatch(line)[1]
		case strings.TrimSpace(line) == "":
			flush()
			out = append(out, "")
		default:
			cur = append(cur, line)
		}
	}
	flush()
	start, end := 0, len(out)
	for start < end && out[start] == "" {
		start++
	}
	for end > start && out[end-1] == "" {
		end--
	}
	return out[start:end]
}

// UploadImage registers a local file as a message image. The raw images create
// command is used rather than +messages-send --image because that flag refuses
// an absolute path and resolves a relative one against c.Dir, which leaves no
// way to name a file the user picked anywhere else.
func (c *ExecClient) UploadImage(ctx context.Context, path string) (string, error) {
	data, err := c.run(ctx, "im", "images", "create", "--data", `{"image_type":"message"}`, "--file", path)
	if err != nil {
		return "", err
	}
	var resp struct {
		ImageKey string `json:"image_key"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("decode image upload: %w", err)
	}
	if resp.ImageKey == "" {
		return "", fmt.Errorf("image upload returned no key")
	}
	return resp.ImageKey, nil
}

// UploadFile registers a local file as a message attachment, for the same
// reason UploadImage does not go through the send flag: --file refuses an
// absolute path and resolves a relative one against c.Dir.
//
// file_type is always stream. The enum's named types (pdf, doc, mp4, …) only
// change the icon Feishu draws, and guessing one from an extension would be a
// guess the server can already make; stream is what it falls back to anyway.
func (c *ExecClient) UploadFile(ctx context.Context, path string) (string, error) {
	name := filepath.Base(path)
	body, err := json.Marshal(map[string]string{"file_type": "stream", "file_name": name})
	if err != nil {
		return "", err
	}
	data, err := c.run(ctx, "im", "files", "create", "--data", string(body), "--file", path)
	if err != nil {
		return "", err
	}
	var resp struct {
		FileKey string `json:"file_key"`
	}
	if err := json.Unmarshal(data, &resp); err != nil {
		return "", fmt.Errorf("decode file upload: %w", err)
	}
	if resp.FileKey == "" {
		return "", fmt.Errorf("file upload returned no key")
	}
	return resp.FileKey, nil
}

// Recall takes a message back. Whether this identity may — it sent it, and the
// window has not closed — is Feishu's to answer, so nothing is checked here:
// a local guess at the limit would refuse sends the server would have taken.
func (c *ExecClient) Recall(ctx context.Context, messageID string) error {
	_, err := c.run(ctx, "im", "messages", "delete", "--message-id", messageID)
	return err
}

// Forward sends an existing message on. receive_id_type follows the target, so
// a chat and a person are told apart by which field is set, as everywhere else.
func (c *ExecClient) Forward(ctx context.Context, messageID string, target Target, idempotencyKey string) (SentMessage, error) {
	idType, receiveID := "chat_id", target.ChatID
	if target.ChatID == "" {
		idType, receiveID = "open_id", target.UserID
	}
	body, err := json.Marshal(map[string]string{"receive_id": receiveID})
	if err != nil {
		return SentMessage{}, err
	}
	args := []string{"im", "messages", "forward", "--message-id", messageID,
		"--receive-id-type", idType, "--data", string(body)}
	if idempotencyKey != "" {
		args = append(args, "--uuid", idempotencyKey)
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

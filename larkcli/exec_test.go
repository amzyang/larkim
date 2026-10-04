package larkcli

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

// fakeBinary writes a shell script that plays lark-cli: it dispatches on the
// first two arguments and prints canned stdout/stderr with an exit code.
//
// The lark-cli it hands out is a symlink to the one shim TestMain wrote, which
// runs the script beside the link. macOS vets every new executable on its
// first launch, a tenth of a second or more each, and a script sh only reads
// is not one.
func fakeBinary(t *testing.T, script string) *ExecClient {
	t.Helper()
	dir := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(dir, "script"), []byte(script), 0o644))
	path := filepath.Join(dir, "lark-cli")
	require.NoError(t, os.Symlink(shimPath, path))
	return &ExecClient{Path: path, Dir: dir, Timeout: 10 * time.Second}
}

func TestActiveChats_AsksForOnePageOfThatSize(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `echo "$*" > "$(dirname "$0")/argv"; echo '{"ok":true,"data":{"items":[]}}'`)
	_, err := c.ActiveChats(t.Context(), 30)
	require.NoError(t, err)
	argv, err := os.ReadFile(filepath.Join(filepath.Dir(c.Path), "argv"))
	require.NoError(t, err)
	require.Contains(t, string(argv), `"page_size":30`)
	require.Contains(t, string(argv), `"sort_type":"ByActiveTimeDesc"`)
	require.NotContains(t, string(argv), "--page-all", "the first page by activity is the whole answer")
}

func TestListChats_ListsEveryChat(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `echo "$*" > "$(dirname "$0")/argv"; echo '{"ok":true,"data":{"items":[]}}'`)
	_, err := c.ListChats(t.Context())
	require.NoError(t, err)
	argv, err := os.ReadFile(filepath.Join(filepath.Dir(c.Path), "argv"))
	require.NoError(t, err)
	require.Contains(t, string(argv), `"page_size":100`)
	require.Contains(t, string(argv), "--page-all")
	require.NotContains(t, string(argv), "sort_type")
}

func TestExec_AnAPIErrorOnALaterPageFailsTheCall(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "[page 1] fetching..." >&2
echo "[page 2] fetching..." >&2
echo "[page 2] API error (code=99991400), stopping pagination" >&2
echo '{"ok":true,"identity":"user","data":{"items":[{"message_id":"om_1"}],"has_more":false}}'`)
	msgs, err := c.ListMessagesRaw(t.Context(), "chat", "oc_a", time.Now().Add(-time.Hour), time.Time{})
	le, ok := errors.AsType[*Error](err)
	require.True(t, ok, "the pages before the failure are not the listing")
	require.Nil(t, msgs)
	require.Equal(t, 99991400, le.Code)
	require.True(t, le.IsRateLimit(), "a later page's rate limit backs the caller off like a first page's")
	require.False(t, le.IsPermanent(), "the same request may well succeed, so no chat is retired over it")
	require.Contains(t, le.Argv, "--page-all")
}

func TestExec_ATransportErrorOnALaterPageIsANetworkError(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "[page 1] fetching..." >&2
echo "[page 2] fetching..." >&2
echo "[page 2] error, stopping pagination" >&2
echo '{"ok":true,"identity":"user","data":{"items":[{"chat_id":"oc_a"}],"has_more":true}}'`)
	chats, err := c.ListChats(t.Context())
	le, ok := errors.AsType[*Error](err)
	require.True(t, ok, "a chat list cut short would mark every chat past the cut as left")
	require.Nil(t, chats)
	require.True(t, le.IsNetwork())
	require.False(t, le.IsPermanent())
}

func TestSearchMessageIDs_DecodesMetaAndTruncation(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
case "$1 $2" in
"api POST") cat <<'JSON'
{"ok":true,"identity":"user","data":{"items":[
 {"id":"om_1","meta_data":{"message_id":"om_1","chat_id":"oc_a","from_id":"ou_x","is_p2p_chat":true,"position":7,"type":"TEXT","create_time":"2026-09-22T12:20:31Z"}},
 {"id":"om_2","meta_data":{"message_id":"om_2","chat_id":"oc_b","thread_id":"omt_9","type":"CARD","create_time":"2026-09-22T12:01:35Z"}}
],"has_more":true},"meta":null}
JSON
;;
*) echo "unexpected: $*" >&2; exit 5;;
esac`)
	hits, truncated, err := c.SearchMessageIDs(t.Context(), time.Now().Add(-time.Hour), time.Now())
	require.NoError(t, err)
	require.True(t, truncated)
	require.Len(t, hits, 2)
	require.Equal(t, "oc_a", hits[0].ChatID)
	require.Equal(t, int64(7), hits[0].Position)
	require.True(t, hits[0].IsP2P)
	require.Equal(t, time.Date(2026, 9, 22, 12, 20, 31, 0, time.UTC), hits[0].CreateTime)
	require.Equal(t, "omt_9", hits[1].ThreadID)
}

func TestMGetRaw_ParsesMillisecondTimesAndKeepsRaw(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `cat <<'JSON'
{"ok":true,"identity":"user","data":{"items":[{"message_id":"om_1","chat_id":"oc_a","msg_type":"text","create_time":"1790078010746","update_time":"1790078010746","message_position":"4745","deleted":false,"updated":false,"sender":{"id":"ou_x","id_type":"open_id","sender_type":"user","sender_name":"Alice"},"body":{"content":"{\"text\":\"hi\"}"},"mentions":[{"key":"@_user_1","id":"ou_y","name":"Bob"}]}]}}
JSON`)
	msgs, err := c.MGetRaw(t.Context(), []string{"om_1"})
	require.NoError(t, err)
	require.Len(t, msgs, 1)
	m := msgs[0]
	require.Equal(t, int64(1790078010746), int64(m.CreateTime))
	require.Equal(t, int64(4745), int64(m.MessagePosition))
	require.Equal(t, "Alice", m.Sender.SenderName)
	require.Equal(t, `{"text":"hi"}`, m.Body.Content)
	require.Equal(t, "Bob", m.Mentions[0].Name)
	require.Contains(t, string(m.Raw), `"message_position":"4745"`)
}

func TestRun_DecodesErrorEnvelopeAfterProgressLines(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "[page 1] fetching..." >&2
echo '{"ok":false,"identity":"user","error":{"type":"api","subtype":"rate_limit","code":99991400,"message":"too many requests","retry_after_seconds":4}}' >&2
exit 1`)
	_, _, err := c.SearchMessageIDs(t.Context(), time.Now(), time.Now())
	var lerr *Error
	require.ErrorAs(t, err, &lerr)
	require.Equal(t, 1, lerr.ExitCode)
	require.True(t, lerr.IsRateLimit())
	require.Equal(t, 4*time.Second, lerr.RetryAfter)
	require.False(t, lerr.IsAuth())
}

func TestRun_DecodesPrettyPrintedErrorEnvelope(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "[page 1] fetching..." >&2
cat >&2 <<'JSON'
{
  "ok": false,
  "identity": "user",
  "error": {
    "type": "api",
    "subtype": "unknown",
    "code": 231203,
    "message": "The chat type is not supported",
    "log_id": "x"
  }
}
JSON
exit 1`)
	_, err := c.ListMessagesRaw(t.Context(), "chat", "oc_1", time.Time{}, time.Time{})
	var lerr *Error
	require.ErrorAs(t, err, &lerr)
	require.Equal(t, 231203, lerr.Code)
	require.Equal(t, "The chat type is not supported", lerr.Message)
	require.True(t, lerr.IsPermanent())
}

func TestRun_AuthExitCode(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo '{"ok":false,"identity":"user","error":{"type":"auth","subtype":"token_missing","message":"no token"}}' >&2
exit 3`)
	_, err := c.MGetRaw(t.Context(), []string{"om_1"})
	var lerr *Error
	require.ErrorAs(t, err, &lerr)
	require.True(t, lerr.IsAuth())
	require.Equal(t, "token_missing", lerr.Subtype)
}

func TestWhoami_UserIdentity(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `cat <<'JSON'
{"profile":"cli_x","appId":"cli_x","identity":"user","available":true,"tokenStatus":"ready","onBehalfOf":{"userName":"A","openId":"ou_me"}}
JSON`)
	id, err := c.Whoami(t.Context())
	require.NoError(t, err)
	require.Equal(t, Identity{AppID: "cli_x", UserOpenID: "ou_me"}, id)
}

func TestWhoami_BotFallbackIsAuthError(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `echo '{"appId":"cli_x","identity":"bot","available":true}'`)
	_, err := c.Whoami(t.Context())
	var lerr *Error
	require.ErrorAs(t, err, &lerr)
	require.True(t, lerr.IsAuth())
}

func TestListMessagesRaw_PassesEpochSecondsAndContainer(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" > "$(dirname "$0")/args.txt"
echo '{"ok":true,"identity":"user","data":{"items":[]}}'`)
	start := time.Unix(1700000000, 0)
	_, err := c.ListMessagesRaw(t.Context(), "thread", "omt_1", start, time.Time{})
	require.NoError(t, err)
	args, err := os.ReadFile(filepath.Join(c.Dir, "args.txt"))
	require.NoError(t, err)
	require.Contains(t, string(args), `"container_id_type":"thread"`)
	require.Contains(t, string(args), `"start_time":"1700000000"`)
	require.NotContains(t, string(args), `"end_time"`)
	require.Contains(t, string(args), "--page-limit 0 --as user --json")
}

func TestLarkTimeLayout_NeverRendersZ(t *testing.T) {
	t.Parallel()
	require.Equal(t, "2026-09-22T12:00:00+00:00", time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC).Format(larkTimeLayout))
}

func TestRealBinary_PrefersGoBinaryBehindNpmWrapper(t *testing.T) {
	t.Parallel()
	root := t.TempDir()
	pkg := filepath.Join(root, "lib", "node_modules", "@larksuite", "cli")
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "scripts"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(pkg, "bin"), 0o755))
	require.NoError(t, os.WriteFile(filepath.Join(pkg, "scripts", "run.js"), []byte("#!/usr/bin/env node\n"), 0o755))
	real := filepath.Join(pkg, "bin", "lark-cli")
	require.NoError(t, os.WriteFile(real, []byte("#!/bin/sh\n"), 0o755))
	require.NoError(t, os.MkdirAll(filepath.Join(root, "bin"), 0o755))
	link := filepath.Join(root, "bin", "lark-cli")
	require.NoError(t, os.Symlink(filepath.Join(pkg, "scripts", "run.js"), link))

	got, err := (&ExecClient{Path: link}).ResolvePath()
	require.NoError(t, err)
	wantReal, _ := filepath.EvalSymlinks(real)
	require.Equal(t, wantReal, got)

	plain := filepath.Join(root, "plain-lark-cli")
	require.NoError(t, os.WriteFile(plain, []byte("#!/bin/sh\n"), 0o755))
	got, err = (&ExecClient{Path: plain}).ResolvePath()
	require.NoError(t, err)
	require.Equal(t, plain, got)
}

func TestChildEnv_PrependsHomebrewPath(t *testing.T) {
	t.Parallel()
	env := childEnv([]string{"HOME=/x", "PATH=/usr/bin"})
	require.Contains(t, env, "PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin")
	env = childEnv([]string{"HOME=/x"})
	require.Contains(t, env, "PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin")
}

func TestSearchUsers_SplitsIDsIntoServerSizedBatches(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" >> "$(dirname "$0")/calls"
n=0
for id in $(echo "$4" | tr ',' ' '); do
  n=$((n+1))
  [ $n -gt 1 ] && printf ,
  printf '{"open_id":"%s","localized_name":"u","enterprise_email":"%s01@x.cn"}' "$id" "$id"
done > "$(dirname "$0")/users"
printf '{"ok":true,"identity":"user","data":{"users":[%s]}}' "$(cat "$(dirname "$0")/users")"`)
	ids := make([]string, 45)
	for i := range ids {
		ids[i] = fmt.Sprintf("ou_%02d", i)
	}
	users, err := c.SearchUsers(t.Context(), "", ids)
	require.NoError(t, err)
	require.Len(t, users, len(ids), "every id is resolved across batches")
	require.Equal(t, "ou_00", users[0].OpenID)
	require.Equal(t, "ou_0001@x.cn", users[0].EnterpriseEmail)
	require.Equal(t, "ou_44", users[len(users)-1].OpenID)

	calls, err := os.ReadFile(filepath.Join(c.Dir, "calls"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	require.Len(t, lines, 3, "45 ids split into batches of %d", MaxUserIDsPerSearch)
	require.Equal(t, MaxUserIDsPerSearch, strings.Count(lines[0], ",")+1)
	require.Equal(t, 5, strings.Count(lines[2], ",")+1, "last batch holds the remainder")
}

func TestSearchUsers_QueryModeSendsOneCall(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" >> "$(dirname "$0")/calls"
echo '{"ok":true,"identity":"user","data":{"users":[{"open_id":"ou_1","localized_name":"李明","enterprise_email":"liming01@example.com","department":"产品部"}]}}'`)
	users, err := c.SearchUsers(t.Context(), "李明", nil)
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, "产品部", users[0].Department)

	calls, err := os.ReadFile(filepath.Join(c.Dir, "calls"))
	require.NoError(t, err)
	require.Contains(t, string(calls), "--query 李明")
}

func TestUserDetails_RepeatsUserIDsAndDropsWithheldUsers(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" >> "$(dirname "$0")/calls"
echo '{"ok":true,"identity":"user","data":{"items":[
 {"open_id":"ou_in","name":"赵思远","avatar":{"avatar_240":"https://cdn/a.png"}}
]}}'`)
	got, err := c.UserDetails(t.Context(), []string{"ou_in", "ou_out"})
	require.NoError(t, err)
	require.Len(t, got, 1, "a user outside the directory scope is absent, not an error")
	require.Equal(t, "ou_in", got[0].OpenID)
	require.Equal(t, "https://cdn/a.png", got[0].AvatarURL)

	calls, err := os.ReadFile(filepath.Join(c.Dir, "calls"))
	require.NoError(t, err)
	require.Contains(t, string(calls), `"user_ids":["ou_in","ou_out"]`,
		"user_ids must repeat as an array; a comma-joined string reads as one malformed id")
	require.Contains(t, string(calls), "--as bot",
		"contact reads go as the app, whose directory scope is tenant-wide")
}

func TestUserDetails_SplitsIntoBatches(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" >> "$(dirname "$0")/calls"
echo '{"ok":true,"identity":"user","data":{"items":[]}}'`)
	ids := make([]string, MaxUserDetailsBatch+1)
	for i := range ids {
		ids[i] = fmt.Sprintf("ou_%02d", i)
	}
	_, err := c.UserDetails(t.Context(), ids)
	require.NoError(t, err)

	calls, err := os.ReadFile(filepath.Join(c.Dir, "calls"))
	require.NoError(t, err)
	require.Len(t, strings.Split(strings.TrimSpace(string(calls)), "\n"), 2,
		"%d ids need a second call", len(ids))
}

func TestMuteStatus_KeepsUnansweredChatsApartFromUnmutedOnes(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" >> "$(dirname "$0")/calls"
echo '{"ok":true,"identity":"user","data":{"items":[{"chat_id":"oc_a","is_muted":true},{"chat_id":"oc_b","is_muted":false}],"invalid_id_list":[{"id":"oc_x","msg":"not a member"}]}}'`)

	muted, unknown, err := c.MuteStatus(t.Context(), []string{"oc_a", "oc_b", "oc_x"})
	require.NoError(t, err)
	require.Equal(t, map[string]bool{"oc_a": true, "oc_b": false}, muted)
	require.Equal(t, []string{"oc_x"}, unknown, "a chat the API would not answer for is not an unmuted one")

	calls, err := os.ReadFile(filepath.Join(c.Dir, "calls"))
	require.NoError(t, err)
	require.Contains(t, string(calls), "POST /open-apis/im/v1/chat_user_setting/batch_get_mute_status")
	require.Contains(t, string(calls), `{"chat_ids":["oc_a","oc_b","oc_x"]}`)
	require.Contains(t, string(calls), "--as user", "mute is a per-user setting; a bot has no answer to give")
}

func TestMuteStatus_SplitsIDsIntoServerSizedBatches(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" >> "$(dirname "$0")/calls"
echo '{"ok":true,"identity":"user","data":{"items":[]}}'`)
	ids := make([]string, MaxChatIDsPerMuteCall+5)
	for i := range ids {
		ids[i] = fmt.Sprintf("oc_%03d", i)
	}

	_, _, err := c.MuteStatus(t.Context(), ids)
	require.NoError(t, err)

	calls, err := os.ReadFile(filepath.Join(c.Dir, "calls"))
	require.NoError(t, err)
	lines := strings.Split(strings.TrimSpace(string(calls)), "\n")
	require.Len(t, lines, 2, "%d ids do not fit one call of %d", len(ids), MaxChatIDsPerMuteCall)
	require.Equal(t, MaxChatIDsPerMuteCall, strings.Count(lines[0], ",")+1)
	require.Equal(t, 5, strings.Count(lines[1], ",")+1, "the last call holds the remainder")
}

func TestSetChatMuted_CallsBatchUpdate(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" >> "$(dirname "$0")/calls"
echo '{"ok":true,"identity":"user","data":{}}'`)
	err := c.SetChatMuted(t.Context(), "oc_a", true)
	require.NoError(t, err)

	calls, err := os.ReadFile(filepath.Join(c.Dir, "calls"))
	require.NoError(t, err)
	body := string(calls)
	require.Contains(t, body, "POST /open-apis/im/v1/chat_user_setting/batch_update")
	require.Contains(t, body, `"chat_settings"`)
	require.Contains(t, body, `"chat_id":"oc_a"`)
	require.Contains(t, body, `"is_muted":true`)
	require.Contains(t, body, "--as user")
}

func TestSetChatMuted_SurfacesInvalidID(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" >> "$(dirname "$0")/calls"
echo '{"ok":true,"identity":"user","data":{"invalid_ids":[{"id":"oc_x","msg":"not_a_member"}]}}'`)
	err := c.SetChatMuted(t.Context(), "oc_x", true)
	require.Error(t, err)
	require.Contains(t, err.Error(), "oc_x")
	require.Contains(t, err.Error(), "not_a_member")
}

func wireOf(o Outgoing) []string {
	msgType, content := o.wire()
	return []string{msgType, content}
}

func TestOutgoing_WireSendsAListWithAnEmojiAsACard(t *testing.T) {
	t.Parallel()
	// A post moves an emotion out of its list item; a card's markdown keeps
	// it in the item, spelled as its key.
	require.Equal(t, []string{"interactive", Card("* :DONE:\n* :DONE:").Card}, wireOf(Markdown("* [Done]\n* [Done]")))
	require.Equal(t, "post", wireOf(Markdown("* plain"))[0])
	require.Equal(t, "post", wireOf(Markdown("收到 [Done]"))[0], "a line of words already carries the emotion")
}

func TestOutgoing_WirePicksTheMessageType(t *testing.T) {
	t.Parallel()
	require.Equal(t, []string{"text", `{"text":"hi"}`}, wireOf(Text("hi")))
	require.Equal(t, []string{"post", `{"zh_cn":{"content":[[{"tag":"md","text":"## hi"}]]}}`}, wireOf(Markdown("## hi")))
	require.Equal(t, []string{"image", `{"image_key":"img_a"}`}, wireOf(Image("img_a")))
	require.Equal(t, []string{"file", `{"file_key":"file_a"}`}, wireOf(File("file_a")))
	// An empty body is still a text send, which is what an empty draft would be.
	require.Equal(t, []string{"text", `{"text":""}`}, wireOf(Outgoing{}))
}

func TestExecClient_SendPostsTheWholeBodyAndKeepsTheAnswer(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
printf '%s\n' "$@" > "$(dirname "$0")/args"
echo '{"ok":true,"identity":"user","data":{"message_id":"om_new","chat_id":"oc_quiet","msg_type":"post","create_time":"1700000000000","body":{"content":"{}"}}}'`)
	sent, err := c.Send(t.Context(), Target{ChatID: "oc_quiet"}, Markdown("# 发布说明"), "cli_c")
	require.NoError(t, err)
	require.Equal(t, "om_new", sent.MessageID)
	require.NotNil(t, sent.Message)
	require.Equal(t, "post", sent.Message.MsgType)
	require.EqualValues(t, 1700000000000, sent.Message.CreateTime)
	require.Contains(t, string(sent.Message.Raw), `"om_new"`, "the answer is what raw_json stores")
	args, err := os.ReadFile(filepath.Join(c.Dir, "args"))
	require.NoError(t, err)
	require.Equal(t, []string{
		"api", "POST", "/open-apis/im/v1/messages", "--params", `{"receive_id_type":"chat_id"}`,
		"--data", `{"content":"{\"zh_cn\":{\"content\":[[{\"tag\":\"md\",\"text\":\"# 发布说明\"}]]}}","msg_type":"post","receive_id":"oc_quiet","uuid":"cli_c"}`,
		"--as", "user", "--json",
	}, strings.Split(strings.TrimSpace(string(args)), "\n"), "the heading goes out as written, not demoted")
}

func TestExecClient_SendMarkdownCarriesAnEmojiNameAsItsEmotion(t *testing.T) {
	t.Parallel()
	require.Equal(t,
		[]string{"post", `{"zh_cn":{"content":[[{"tag":"text","text":"收到 "},{"tag":"emotion","emoji_type":"DONE"}]]}}`},
		wireOf(Markdown("收到 [Done]")))
}

func TestExecClient_SendToAPersonNamesThemByOpenID(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
printf '%s\n' "$@" > "$(dirname "$0")/args"
echo '{"ok":true,"identity":"user","data":{"message_id":"om_new","chat_id":"oc_p2p_ou_a"}}'`)
	_, err := c.Send(t.Context(), Target{UserID: "ou_a"}, Image("img_shot"), "")
	require.NoError(t, err)
	args, err := os.ReadFile(filepath.Join(c.Dir, "args"))
	require.NoError(t, err)
	require.Equal(t, []string{
		"api", "POST", "/open-apis/im/v1/messages", "--params", `{"receive_id_type":"open_id"}`,
		"--data", `{"content":"{\"image_key\":\"img_shot\"}","msg_type":"image","receive_id":"ou_a"}`,
		"--as", "user", "--json",
	}, strings.Split(strings.TrimSpace(string(args)), "\n"))
}

func TestExecClient_ReplyInThreadPostsToTheReplyEndpoint(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
printf '%s\n' "$@" > "$(dirname "$0")/args"
echo '{"ok":true,"identity":"user","data":{"message_id":"om_new","chat_id":"oc_quiet"}}'`)
	_, err := c.Reply(t.Context(), "om_elsewhere", Text("ok"), true, "cli_c")
	require.NoError(t, err)
	args, err := os.ReadFile(filepath.Join(c.Dir, "args"))
	require.NoError(t, err)
	require.Equal(t, []string{
		"api", "POST", "/open-apis/im/v1/messages/om_elsewhere/reply",
		"--data", `{"content":"{\"text\":\"ok\"}","msg_type":"text","reply_in_thread":true,"uuid":"cli_c"}`,
		"--as", "user", "--json",
	}, strings.Split(strings.TrimSpace(string(args)), "\n"))
}

func TestExecClient_UploadImagePassesTheAbsolutePath(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" > "$(dirname "$0")/args"
echo '{"ok":true,"identity":"user","data":{"image_key":"img_v3_shot"}}'`)
	// Inside the working directory, so the path reaches lark-cli untouched:
	// the raw images create command is what takes an absolute path at all.
	shot := filepath.Join(c.Dir, "shot.png")
	require.NoError(t, os.WriteFile(shot, []byte("png"), 0o600))
	key, err := c.UploadImage(t.Context(), shot)
	require.NoError(t, err)
	require.Equal(t, "img_v3_shot", key)
	args, err := os.ReadFile(filepath.Join(c.Dir, "args"))
	require.NoError(t, err)
	require.Equal(t,
		`im images create --data {"image_type":"message"} --file `+shot+` --as user --json`,
		strings.TrimSpace(string(args)))
}

func TestExecClient_UploadImageRefusesAnEmptyKey(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `echo '{"ok":true,"identity":"user","data":{}}'`)
	shot := filepath.Join(c.Dir, "shot.png")
	require.NoError(t, os.WriteFile(shot, []byte("png"), 0o600))
	_, err := c.UploadImage(t.Context(), shot)
	require.ErrorContains(t, err, "no key")
}

func TestMGetRaw_AsksForTheRealCardBody(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" > "$(dirname "$0")/args"
echo '{"ok":true,"identity":"user","data":{"items":[]}}'`)
	_, err := c.MGetRaw(t.Context(), []string{"om_elsewhere"})
	require.NoError(t, err)

	args, err := os.ReadFile(filepath.Join(c.Dir, "args"))
	require.NoError(t, err)
	// Without it an interactive message arrives as a placeholder telling the
	// reader to upgrade, carrying neither the card nor its attachment table.
	require.Contains(t, string(args), `"card_msg_content_type":"raw_card_content"`)
}

func TestChatMembers_SplitsUsersFromBots(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" > "$(dirname "$0")/argv"
echo "[page 1] fetching..." >&2
echo "Found 1 user(s) and 1 bot(s)" >&2
cat <<'JSON'
{"ok":true,"identity":"user","data":{"chat_id":"oc_team","user_total":1,"bot_total":1,
 "users":[{"member_id":"ou_a","member_id_type":"open_id","name":"张三","tenant_key":"t1"}],
 "bots":[{"member_id":"ou_bot","app_id":"cli_c","name":"构建机器人","tenant_key":"t1"}],
 "truncations":[],"has_more":false}}
JSON`)

	members, truncated, err := c.ChatMembers(t.Context(), "oc_team")

	require.NoError(t, err)
	require.False(t, truncated)
	require.Len(t, members, 2)
	require.Equal(t, ChatMember{MemberID: "ou_a", MemberType: "open_id", Name: "张三"}, members[0])
	require.Equal(t, ChatMember{MemberID: "ou_bot", Name: "构建机器人", IsBot: true}, members[1])

	argv, err := os.ReadFile(filepath.Join(c.Dir, "argv"))
	require.NoError(t, err)
	require.Contains(t, string(argv), "im +chat-members-list --chat-id oc_team --member-types user,bot")
}

// The shortcut exists to report the cap, so dropping truncations[] would let a
// capped roster pass for a complete one.
func TestChatMembers_ReportsAServerCappedRoster(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
cat <<'JSON'
{"ok":true,"identity":"user","data":{"chat_id":"oc_team",
 "users":[{"member_id":"ou_a","member_id_type":"open_id","name":"张三"}],
 "bots":[],
 "truncations":[{"member_type":"user","limit":100}],"has_more":false}}
JSON`)

	members, truncated, err := c.ChatMembers(t.Context(), "oc_team")

	require.NoError(t, err)
	require.True(t, truncated)
	require.Len(t, members, 1)
}

// echoFile is a stand-in lark-cli that refuses a --file it may not read, the
// way the real one does: it only opens a path inside its working directory,
// /tmp or ~/files.
const echoFile = `
file=""
while [ $# -gt 0 ]; do
  if [ "$1" = "--file" ]; then file="$2"; fi
  shift
done
case "$file" in
  "$PWD"/*|/tmp/*|/private/tmp/*) ;;
  *) echo "cannot open file: $file" >&2; exit 2 ;;
esac
echo "{\"ok\":true,\"identity\":\"user\",\"data\":{\"image_key\":\"img_v3_x\",\"file_key\":\"file_v3_x\",\"seen\":\"$file\"}}"
`

func TestUploadImage_StagesAFileLarkCLIMayNotRead(t *testing.T) {
	// Serial: it asserts the shared upload directory is empty, and the tests
	// that stage a copy there run in parallel.
	c := fakeBinary(t, echoFile)
	// ~/.larkim holds the pasted pictures, the fetched ones and the emoji cut
	// out of the sprite; none of them sit under the resources directory.
	outside := filepath.Join(t.TempDir(), "FOLLOWME.png")
	require.NoError(t, os.WriteFile(outside, []byte("png"), 0o600))

	key, err := c.UploadImage(t.Context(), outside)
	require.NoError(t, err)
	require.Equal(t, "img_v3_x", key)

	left, err := filepath.Glob(filepath.Join(uploadRoot, "larkim-upload-*"))
	require.NoError(t, err)
	require.Empty(t, left, "the copy goes once the call is over")
}

func TestUploadImage_HandsOverAFileInsideTheWorkingDirectoryAsItStands(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, echoFile)
	inside := filepath.Join(c.Dir, "shot.png")
	require.NoError(t, os.WriteFile(inside, []byte("png"), 0o600))

	staged, drop, err := c.stageForUpload(inside)
	require.NoError(t, err)
	defer drop()
	require.Equal(t, inside, staged, "lark-cli opens its own working directory, so nothing is copied")
}

func TestUploadFile_StagesTheContentButKeepsTheReadersName(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, echoFile)
	outside := filepath.Join(t.TempDir(), "季度复盘.pdf")
	require.NoError(t, os.WriteFile(outside, []byte("pdf"), 0o600))

	key, err := c.UploadFile(t.Context(), outside)
	require.NoError(t, err)
	require.Equal(t, "file_v3_x", key)
}

func TestUploadImage_SaysSoWhenThePictureIsGone(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, echoFile)
	_, err := c.UploadImage(t.Context(), filepath.Join(t.TempDir(), "missing.png"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestEmotion_CarriesOneEmojiAsThePostElementFeishuSpellsItWith(t *testing.T) {
	t.Parallel()
	require.Equal(t,
		[]string{"post", `{"zh_cn":{"content":[[{"tag":"emotion","emoji_type":"PursueUltimate"}]]}}`},
		wireOf(Emotion("PursueUltimate")))
}

func TestOutgoing_WireSendsAPostBodyThroughUntouched(t *testing.T) {
	t.Parallel()
	// The markdown path wraps every paragraph in an md element; a body already
	// in Feishu's own shape must reach Feishu as it was written.
	body := `{"zh_cn":{"content":[[{"tag":"emotion","emoji_type":"Get"}]]}}`
	require.Equal(t, []string{"post", body}, wireOf(Outgoing{Post: body}))
}

func TestOlderMessagesRaw_AsksOneDescendingPageEndingAtTheFloor(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" > "$(dirname "$0")/args.txt"
echo '{"ok":true,"identity":"user","data":{"items":[{"message_id":"om_1","chat_id":"oc_a","create_time":"1700000000000"}],"has_more":true}}'`)
	msgs, more, err := c.OlderMessagesRaw(t.Context(), "oc_a", time.Unix(1700000000, 0))
	require.NoError(t, err)
	require.True(t, more, "whether history is exhausted is the server's answer, not a guess from the page length")
	require.Len(t, msgs, 1)

	args, err := os.ReadFile(filepath.Join(c.Dir, "args.txt"))
	require.NoError(t, err)
	require.Contains(t, string(args), `"sort_type":"ByCreateTimeDesc"`)
	require.Contains(t, string(args), `"end_time":"1700000000"`)
	require.NotContains(t, string(args), `"start_time"`)
	require.NotContains(t, string(args), "--page-all", "a reader waits for one page, not the archive")
}

func TestOlderMessagesRaw_ExhaustedHistoryReportsNoMore(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `echo '{"ok":true,"identity":"user","data":{"items":[],"has_more":false}}'`)
	msgs, more, err := c.OlderMessagesRaw(t.Context(), "oc_a", time.Unix(1700000000, 0))
	require.NoError(t, err)
	require.False(t, more)
	require.Empty(t, msgs)
}

func TestFormTitleAndMinuteTitle_AskTheEndpointsTheBatchQueryCannot(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" > "$(dirname "$0")/argv.$1"
case "$1" in
base) cat <<'JSON'
{"ok":true,"identity":"user","data":{"base_token":"AbC123","name":"评论收集表","questions":[]}}
JSON
;;
minutes) cat <<'JSON'
{"ok":true,"identity":"user","data":{"minute":{"token":"obcnMin1","title":"周会妙记","duration":"2278000"}}}
JSON
;;
*) echo "unexpected: $*" >&2; exit 5;;
esac`)
	form, err := c.FormTitle(t.Context(), "shrcnForm1")
	require.NoError(t, err)
	require.Equal(t, "评论收集表", form)
	argv, err := os.ReadFile(filepath.Join(c.Dir, "argv.base"))
	require.NoError(t, err)
	require.Equal(t, "base +form-detail --share-token shrcnForm1 --as user --json\n", string(argv),
		"a form's title tracks the reader's own view, so the identity is the user")

	minute, err := c.MinuteTitle(t.Context(), "obcnMin1")
	require.NoError(t, err)
	require.Equal(t, "周会妙记", minute)
	argv, err = os.ReadFile(filepath.Join(c.Dir, "argv.minutes"))
	require.NoError(t, err)
	require.Equal(t, "minutes minutes get --minute-token obcnMin1 --as user --json\n", string(argv))
}

// lark-cli 1.0.97 reads a flag value opening with @ as a file path and a lone
// - as stdin, so a message that starts with a mention or is a single dash has
// to travel inside the request JSON, which always opens with {.
func TestSend_TextLarkCLIWouldReinterpretTravelsInsideTheBody(t *testing.T) {
	t.Parallel()
	for _, text := range []string{"@张三 看一下", "-"} {
		data := jsonArg(Text(text).request(""))
		require.True(t, strings.HasPrefix(data, "{"), data)
		require.Contains(t, data, text)
	}
}

func TestError_CarriesTheCallAndItsWholeOutput(t *testing.T) {
	t.Parallel()
	long := strings.Repeat("lark-cli said so. ", 40)
	c := fakeBinary(t, `echo '`+long+`' >&2; exit 1`)

	_, err := c.Send(t.Context(), Target{ChatID: "oc_quiet"}, Text("@张三 看一下"), "key-1")

	e, ok := errors.AsType[*Error](err)
	require.True(t, ok)
	require.Contains(t, e.Stderr, long, "the whole refusal is kept; nothing is clipped")
	require.Contains(t, e.Error(), long)
	require.Equal(t, []string{
		"api", "POST", "/open-apis/im/v1/messages", "--params", `{"receive_id_type":"chat_id"}`,
		"--data", `{"content":"{\"text\":\"@张三 看一下\"}","msg_type":"text","receive_id":"oc_quiet","uuid":"key-1"}`,
		"--as", "user", "--json",
	}, e.Argv, "the call is what the failure is replayed from, message body included")
}

// lark-cli rates the recall high-risk and refuses it without --yes; the flag
// has to be on the wire or every recall comes back as exit 10.
func TestExecClient_RecallConfirmsTheHighRiskCall(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
echo "$*" > "$(dirname "$0")/args"
echo '{"ok":true,"identity":"user","data":{}}'`)
	require.NoError(t, c.Recall(t.Context(), "om_mine"))
	args, err := os.ReadFile(filepath.Join(c.Dir, "args"))
	require.NoError(t, err)
	require.Equal(t, "im messages delete --message-id om_mine --yes --as user --json", strings.TrimSpace(string(args)))
}

func TestRecognizeText_SendsThePictureOnStdinAsTheAppIdentity(t *testing.T) {
	c := fakeBinary(t, `
cat > "$TMPDIR/body.json"
echo "$*" > "$TMPDIR/argv.txt"
cat <<'JSON'
{"ok":true,"identity":"bot","data":{"text_list":["NullPointerException","at Foo.java:42"]}}
JSON`)
	img := filepath.Join(t.TempDir(), "shot.png")
	require.NoError(t, os.WriteFile(img, []byte("\x89PNG\r\n\x1a\n"), 0o644))
	t.Setenv("TMPDIR", c.Dir)

	text, err := c.RecognizeText(t.Context(), img)
	require.NoError(t, err)
	require.Equal(t, []string{"NullPointerException", "at Foo.java:42"}, text)

	argv, err := os.ReadFile(filepath.Join(c.Dir, "argv.txt"))
	require.NoError(t, err)
	require.Contains(t, string(argv), "api POST "+ocrPath+" --data - --as bot --json")
	require.NotContains(t, string(argv), base64.StdEncoding.EncodeToString([]byte("\x89PNG\r\n\x1a\n")),
		"the picture never rides on argv, which ArgvLine renders into every log line and crash report")

	body, err := os.ReadFile(filepath.Join(c.Dir, "body.json"))
	require.NoError(t, err)
	var sent struct {
		Image []byte `json:"image"`
	}
	require.NoError(t, json.Unmarshal(body, &sent))
	require.Equal(t, []byte("\x89PNG\r\n\x1a\n"), sent.Image, "the bytes arrive base64 under image")
}

func TestRecognizeText_APictureWithNothingInItIsNotAFailure(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `cat > /dev/null
echo '{"ok":true,"identity":"bot","data":{"text_list":[]}}'`)
	img := filepath.Join(t.TempDir(), "photo.jpg")
	require.NoError(t, os.WriteFile(img, []byte("jpg"), 0o644))

	text, err := c.RecognizeText(t.Context(), img)
	require.NoError(t, err)
	require.Empty(t, text)
}

func TestRecognizeText_AMissingFileNeverSpendsACall(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `echo "called" >&2; exit 5`)
	_, err := c.RecognizeText(t.Context(), filepath.Join(t.TempDir(), "gone.png"))
	require.ErrorIs(t, err, os.ErrNotExist)
}

func TestExecClient_ListTasksPagesUntilTheListEnds(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `
printf '%s\n' "$@" >> "$(dirname "$0")/argv"
if [ "$6" = "--page-token" ]; then
  echo '{"ok":true,"identity":"user","data":{"items":[{"guid":"task_b","status":"done"}],"has_more":false}}'
else
  echo '{"ok":true,"identity":"user","data":{"items":[{"guid":"task_a","status":"todo"}],"has_more":true,"page_token":"tok_2"}}'
fi`)
	tasks, err := c.ListTasks(t.Context())
	require.NoError(t, err)
	require.Equal(t, []Task{{GUID: "task_a"}, {GUID: "task_b", Done: true}}, tasks)
	argv, err := os.ReadFile(filepath.Join(c.Dir, "argv"))
	require.NoError(t, err)
	require.Equal(t, []string{
		"task", "tasks", "list", "--page-size", "100", "--as", "user", "--json",
		"task", "tasks", "list", "--page-size", "100", "--page-token", "tok_2", "--as", "user", "--json",
	}, strings.Split(strings.TrimSpace(string(argv)), "\n"))
}

func TestExecClient_CompleteAndReopenTaskAddressTheTaskByGuid(t *testing.T) {
	t.Parallel()
	c := fakeBinary(t, `printf '%s\n' "$@" > "$(dirname "$0")/argv"; echo '{"ok":true}'`)
	require.NoError(t, c.CompleteTask(t.Context(), "task_a"))
	argv, err := os.ReadFile(filepath.Join(c.Dir, "argv"))
	require.NoError(t, err)
	require.Equal(t, []string{"task", "+complete", "--task-id", "task_a", "--as", "user", "--json"},
		strings.Split(strings.TrimSpace(string(argv)), "\n"))
	require.NoError(t, c.ReopenTask(t.Context(), "task_a"))
	argv, err = os.ReadFile(filepath.Join(c.Dir, "argv"))
	require.NoError(t, err)
	require.Equal(t, []string{"task", "+reopen", "--task-id", "task_a", "--as", "user", "--json"},
		strings.Split(strings.TrimSpace(string(argv)), "\n"))
}

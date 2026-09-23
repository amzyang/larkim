package larkcli

import (
	"context"
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
func fakeBinary(t *testing.T, script string) *ExecClient {
	t.Helper()
	dir := t.TempDir()
	path := filepath.Join(dir, "lark-cli")
	require.NoError(t, os.WriteFile(path, []byte("#!/bin/sh\n"+script), 0o755))
	return &ExecClient{Path: path, Dir: dir, Timeout: 10 * time.Second}
}

func TestSearchMessageIDs_DecodesMetaAndTruncation(t *testing.T) {
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
	hits, truncated, err := c.SearchMessageIDs(context.Background(), time.Now().Add(-time.Hour), time.Now())
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
	c := fakeBinary(t, `cat <<'JSON'
{"ok":true,"identity":"user","data":{"items":[{"message_id":"om_1","chat_id":"oc_a","msg_type":"text","create_time":"1790078010746","update_time":"1790078010746","message_position":"4745","deleted":false,"updated":false,"sender":{"id":"ou_x","id_type":"open_id","sender_type":"user","sender_name":"Alice"},"body":{"content":"{\"text\":\"hi\"}"},"mentions":[{"key":"@_user_1","id":"ou_y","name":"Bob"}]}]}}
JSON`)
	msgs, err := c.MGetRaw(context.Background(), []string{"om_1"})
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
	c := fakeBinary(t, `
echo "[page 1] fetching..." >&2
echo '{"ok":false,"identity":"user","error":{"type":"api","subtype":"rate_limit","code":99991400,"message":"too many requests","retry_after_seconds":4}}' >&2
exit 1`)
	_, _, err := c.SearchMessageIDs(context.Background(), time.Now(), time.Now())
	var lerr *Error
	require.ErrorAs(t, err, &lerr)
	require.Equal(t, 1, lerr.ExitCode)
	require.True(t, lerr.IsRateLimit())
	require.Equal(t, 4*time.Second, lerr.RetryAfter)
	require.False(t, lerr.IsAuth())
}

func TestRun_DecodesPrettyPrintedErrorEnvelope(t *testing.T) {
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
	_, err := c.ListMessagesRaw(context.Background(), "chat", "oc_1", time.Time{}, time.Time{})
	var lerr *Error
	require.ErrorAs(t, err, &lerr)
	require.Equal(t, 231203, lerr.Code)
	require.Equal(t, "The chat type is not supported", lerr.Message)
	require.True(t, lerr.IsPermanent())
}

func TestRun_AuthExitCode(t *testing.T) {
	c := fakeBinary(t, `
echo '{"ok":false,"identity":"user","error":{"type":"auth","subtype":"token_missing","message":"no token"}}' >&2
exit 3`)
	_, err := c.MGetRaw(context.Background(), []string{"om_1"})
	var lerr *Error
	require.ErrorAs(t, err, &lerr)
	require.True(t, lerr.IsAuth())
	require.Equal(t, "token_missing", lerr.Subtype)
}

func TestWhoami_UserIdentity(t *testing.T) {
	c := fakeBinary(t, `cat <<'JSON'
{"profile":"cli_x","appId":"cli_x","identity":"user","available":true,"tokenStatus":"ready","onBehalfOf":{"userName":"A","openId":"ou_me"}}
JSON`)
	id, err := c.Whoami(context.Background())
	require.NoError(t, err)
	require.Equal(t, Identity{AppID: "cli_x", UserOpenID: "ou_me"}, id)
}

func TestWhoami_BotFallbackIsAuthError(t *testing.T) {
	c := fakeBinary(t, `echo '{"appId":"cli_x","identity":"bot","available":true}'`)
	_, err := c.Whoami(context.Background())
	var lerr *Error
	require.ErrorAs(t, err, &lerr)
	require.True(t, lerr.IsAuth())
}

func TestListMessagesRaw_PassesEpochSecondsAndContainer(t *testing.T) {
	c := fakeBinary(t, `
echo "$*" > "$(dirname "$0")/args.txt"
echo '{"ok":true,"identity":"user","data":{"items":[]}}'`)
	start := time.Unix(1700000000, 0)
	_, err := c.ListMessagesRaw(context.Background(), "thread", "omt_1", start, time.Time{})
	require.NoError(t, err)
	args, err := os.ReadFile(filepath.Join(c.Dir, "args.txt"))
	require.NoError(t, err)
	require.Contains(t, string(args), `"container_id_type":"thread"`)
	require.Contains(t, string(args), `"start_time":"1700000000"`)
	require.NotContains(t, string(args), `"end_time"`)
	require.Contains(t, string(args), "--page-limit 0 --as user --json")
}

func TestLarkTimeLayout_NeverRendersZ(t *testing.T) {
	require.Equal(t, "2026-09-22T12:00:00+00:00", time.Date(2026, 9, 22, 12, 0, 0, 0, time.UTC).Format(larkTimeLayout))
}

func TestRealBinary_PrefersGoBinaryBehindNpmWrapper(t *testing.T) {
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
	env := childEnv([]string{"HOME=/x", "PATH=/usr/bin"})
	require.Contains(t, env, "PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin")
	env = childEnv([]string{"HOME=/x"})
	require.Contains(t, env, "PATH=/opt/homebrew/bin:/usr/local/bin:/usr/bin:/bin")
}

func TestSearchUsers_SplitsIDsIntoServerSizedBatches(t *testing.T) {
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
	users, err := c.SearchUsers(context.Background(), "", ids)
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
	c := fakeBinary(t, `
echo "$*" >> "$(dirname "$0")/calls"
echo '{"ok":true,"identity":"user","data":{"users":[{"open_id":"ou_1","localized_name":"陈建伟","enterprise_email":"chenjianwei01@gaotu.cn","department":"产品部"}]}}'`)
	users, err := c.SearchUsers(context.Background(), "陈建伟", nil)
	require.NoError(t, err)
	require.Len(t, users, 1)
	require.Equal(t, "产品部", users[0].Department)

	calls, err := os.ReadFile(filepath.Join(c.Dir, "calls"))
	require.NoError(t, err)
	require.Contains(t, string(calls), "--query 陈建伟")
}

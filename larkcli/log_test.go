package larkcli

import (
	"bytes"
	"context"
	"log/slog"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// logged attaches a logger at level to c and returns what it writes.
func logged(c *ExecClient, level slog.Level) *bytes.Buffer {
	var buf bytes.Buffer
	c.Log = slog.New(slog.NewTextHandler(&buf, &slog.HandlerOptions{Level: level}))
	return &buf
}

// lines returns the records whose message is msg.
func lines(buf *bytes.Buffer, msg string) []string {
	var out []string
	for _, l := range strings.Split(buf.String(), "\n") {
		if strings.Contains(l, `msg="`+msg+`"`) {
			out = append(out, l)
		}
	}
	return out
}

func TestExec_LogsARequestAndAResponseForEveryCall(t *testing.T) {
	c := fakeBinary(t, `echo '{"ok":true,"identity":"user","data":{"items":[]}}'`)
	buf := logged(c, slog.LevelDebug)

	_, err := c.ListChats(context.Background(), false)
	require.NoError(t, err)

	req := lines(buf, "lark-cli request")
	resp := lines(buf, "lark-cli response")
	require.Len(t, req, 1)
	require.Len(t, resp, 1)
	require.Contains(t, req[0], "call=1")
	require.Contains(t, resp[0], "call=1", "the pair carries the same id so interleaved lanes stay readable")
	require.Contains(t, req[0], "/open-apis/im/v1/chats", "the argv is the request detail")
	require.Contains(t, req[0], "lane=background")
	require.Contains(t, req[0], "queued=")
	require.Contains(t, resp[0], "dur=")
	require.Contains(t, resp[0], "bytes=")
}

func TestExec_LogsARefusalWhenDebugIsOff(t *testing.T) {
	c := fakeBinary(t, `
cat >&2 <<'JSON'
{"ok":false,"identity":"user","error":{"type":"api","subtype":"rate_limit","code":99991400,"message":"too many requests","log_id":"lg_1","retry_after_seconds":30}}
JSON
exit 1`)
	buf := logged(c, slog.LevelInfo)

	_, err := c.ListChats(context.Background(), false)
	require.Error(t, err)

	require.Empty(t, lines(buf, "lark-cli request"), "the request detail is debug-only")
	resp := lines(buf, "lark-cli response")
	require.Len(t, resp, 1, "a failure is recorded whether or not debug is on")
	require.Contains(t, resp[0], "level=WARN")
	require.Contains(t, resp[0], "code=99991400")
	require.Contains(t, resp[0], "log_id=lg_1")
	require.Contains(t, resp[0], "subtype=rate_limit")
	require.Contains(t, resp[0], "retry_after=30s")
	require.Contains(t, resp[0], `error="too many requests"`)
	require.Equal(t, 1, strings.Count(resp[0], " msg="), "one msg= per record, or nothing can parse the log")
	require.Contains(t, resp[0], "/open-apis/im/v1/chats", "the line stands alone, so it repeats the argv")
}

func TestExec_LogsTheLarkLogIDOntoTheError(t *testing.T) {
	c := fakeBinary(t, `
cat >&2 <<'JSON'
{"ok":false,"identity":"user","error":{"type":"api","subtype":"unknown","code":231203,"message":"nope","log_id":"lg_2"}}
JSON
exit 1`)
	_, err := c.ListChats(context.Background(), false)
	var lerr *Error
	require.ErrorAs(t, err, &lerr)
	require.Equal(t, "lg_2", lerr.LogID)
}

func TestExec_CountsThePagesLarkCLIReports(t *testing.T) {
	c := fakeBinary(t, `
echo "[page 1] fetching..." >&2
echo "[page 2] fetching..." >&2
echo '{"ok":true,"identity":"user","data":{"items":[]}}'`)
	buf := logged(c, slog.LevelDebug)

	_, err := c.ListChats(context.Background(), false)
	require.NoError(t, err)
	require.Contains(t, lines(buf, "lark-cli response")[0], "pages=2",
		"one invocation is as many requests as it paginated")
}

func TestExec_LogsAMissingBinaryWithoutTakingALane(t *testing.T) {
	c := &ExecClient{Path: "/nonexistent/lark-cli"}
	buf := logged(c, slog.LevelInfo)

	_, err := c.ListChats(context.Background(), false)
	require.Error(t, err)
	failed := lines(buf, "lark-cli failed")
	require.Len(t, failed, 1)
	require.Contains(t, failed[0], "level=WARN")
}

func TestExec_KeepsACancelledCallOutOfTheWarnings(t *testing.T) {
	c := fakeBinary(t, `sleep 5`)
	buf := logged(c, slog.LevelInfo)
	// Hold the only background lane so the next call can do nothing but wait.
	require.NoError(t, c.background().acquire(context.Background()))

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := c.ListChats(ctx, false)
	require.Error(t, err)
	require.Empty(t, lines(buf, "lark-cli failed"), "leaving is not a fault")
}

func TestExec_NilLoggerDiscards(t *testing.T) {
	c := fakeBinary(t, `echo '{"ok":true,"identity":"user","data":{"items":[]}}'`)
	_, err := c.ListChats(context.Background(), false)
	require.NoError(t, err)
}

func TestArgvLine_QuotesWhatAShellWouldNeedQuoted(t *testing.T) {
	require.Equal(t, `api GET /open-apis/im/v1/messages`,
		argvLine([]string{"api", "GET", "/open-apis/im/v1/messages"}))
	require.Equal(t, `im +messages-send --text 'hello there'`,
		argvLine([]string{"im", "+messages-send", "--text", "hello there"}))
	require.Equal(t, `--text 'it'\''s fine'`, argvLine([]string{"--text", "it's fine"}))
	require.Equal(t, `--text ''`, argvLine([]string{"--text", ""}))
}

func TestPages_IgnoresTheOtherProgressLines(t *testing.T) {
	require.Equal(t, 0, pages(nil))
	require.Equal(t, 2, pages([]byte("[page 1] fetching...\n[page 2] fetching...\n[pagination] streamed 2 pages\n")))
}

func TestLane_Stringer(t *testing.T) {
	require.Equal(t, "background", LaneBackground.String())
	require.Equal(t, "interactive", LaneInteractive.String())
}

func TestIsPermanent_CoversBothWaysLarkCLIReportsARefusal(t *testing.T) {
	cases := []struct {
		name string
		err  *Error
		want bool
	}{
		{"api rejection", &Error{ExitCode: ExitAPI, Code: 234003}, true},
		{"api rate limit", &Error{ExitCode: ExitAPI, Subtype: "rate_limit"}, false},
		{"download of a deleted resource", &Error{ExitCode: ExitNetwork, Code: 400}, true},
		{"download of a missing resource", &Error{ExitCode: ExitNetwork, Code: 404}, true},
		{"download of a gone resource", &Error{ExitCode: ExitNetwork, Code: 410}, true},
		{"unauthorized may be a token refresh", &Error{ExitCode: ExitNetwork, Code: 401}, false},
		{"forbidden may be a token refresh", &Error{ExitCode: ExitNetwork, Code: 403}, false},
		{"request timeout asks to be retried", &Error{ExitCode: ExitNetwork, Code: 408}, false},
		{"too many requests asks to be retried", &Error{ExitCode: ExitNetwork, Code: 429}, false},
		{"server error", &Error{ExitCode: ExitNetwork, Code: 503}, false},
		{"connection reset carries no status", &Error{ExitCode: ExitNetwork}, false},
		{"auth", &Error{ExitCode: ExitAuth}, false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			require.Equal(t, c.want, c.err.IsPermanent())
		})
	}
}

func TestDecodeError_LiftsFeishuCodeOutOfADownloadFailure(t *testing.T) {
	// What lark-cli prints when a resource is served outside the API
	// envelope: a transport error carrying the raw HTTP body.
	stderr := []byte(`{"ok":false,"identity":"user","error":{"type":"network","subtype":"transport","code":400,` +
		`"message":"HTTP 400: {\"code\":14005,\"error\":{\"log_id\":\"lg_9\"},\"msg\":\"Resource Has Been Deleted\"}"}}`)

	e := decodeError(4, stderr)
	require.Equal(t, 400, e.Code, "Code stays the HTTP status, which is what IsPermanent reads")
	require.Equal(t, 14005, e.APICode)
	require.Equal(t, "Resource Has Been Deleted", e.APIMessage)
	require.Equal(t, "lg_9", e.LogID, "the body's log_id is taken when the envelope carries none")
	require.Equal(t, "lark-cli network/transport (exit 4): 14005 Resource Has Been Deleted", e.Error())
	require.True(t, e.IsPermanent())
}

func TestDecodeError_LeavesAnOrdinaryEnvelopeAlone(t *testing.T) {
	stderr := []byte(`{"ok":false,"identity":"user","error":{"type":"api","subtype":"unknown","code":231203,` +
		`"message":"The chat type is not supported","log_id":"lg_1"}}`)

	e := decodeError(1, stderr)
	require.Zero(t, e.APICode, "there is no nested body to lift")
	require.Equal(t, 231203, e.Code)
	require.Equal(t, "lg_1", e.LogID)
	require.Equal(t, "lark-cli api/unknown (exit 1): The chat type is not supported", e.Error())
}

func TestDecodeError_SurvivesABodyLarkCLITruncated(t *testing.T) {
	stderr := []byte(`{"ok":false,"identity":"user","error":{"type":"network","subtype":"transport","code":400,` +
		`"message":"HTTP 400: {\"code\":14005,\"msg\":\"Resource Has Be"}}`)

	e := decodeError(4, stderr)
	require.Zero(t, e.APICode, "half a body is not a verdict")
	require.Contains(t, e.Error(), "HTTP 400", "the text is still reported as it came")
}

func TestExec_LogsTheFeishuCodeOnADownloadFailure(t *testing.T) {
	c := fakeBinary(t, `
cat >&2 <<'JSON'
{"ok":false,"identity":"user","error":{"type":"network","subtype":"transport","code":400,"message":"HTTP 400: {\"code\":14005,\"error\":{\"log_id\":\"lg_9\"},\"msg\":\"Resource Has Been Deleted\"}"}}
JSON
exit 4`)
	buf := logged(c, slog.LevelInfo)

	_, err := c.DownloadResource(context.Background(), "om_a", "img_gone", "image")
	require.Error(t, err)

	resp := lines(buf, "lark-cli response")
	require.Len(t, resp, 1)
	require.Contains(t, resp[0], "api_code=14005")
	require.Contains(t, resp[0], `api_msg="Resource Has Been Deleted"`)
	require.Contains(t, resp[0], "code=400", "the HTTP status is still there; it is what decides")
	require.Contains(t, resp[0], "log_id=lg_9")
}

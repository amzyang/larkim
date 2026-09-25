package larkcli

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"regexp"
	"strings"
	"time"
)

// logRequest opens a call's pair of lines. It is emitted once the call holds a
// lane, so queued measures how long it waited for one: with the lanes in
// place, that wait is the difference between a slow gateway and a busy sweep.
func (c *ExecClient) logRequest(ctx context.Context, call uint64, args []string, l Lane, queued time.Duration) {
	c.logger().DebugContext(ctx, "lark-cli request", "call", call, "argv", ArgvLine(args),
		"lane", l, "queued", queued)
}

// logResponse closes the pair. A refusal is logged whether or not debug is on,
// and repeats the argv because without the request line it stands alone.
func (c *ExecClient) logResponse(ctx context.Context, call uint64, args []string, dur time.Duration,
	stdout, stderr []byte, exitCode int, err error) {
	if err != nil {
		c.logFailure(ctx, call, args, err)
		return
	}
	if exitCode != 0 {
		e := decodeError(exitCode, stderr)
		attrs := []any{"call", call, "argv", ArgvLine(args), "dur", dur, "exit", exitCode,
			"type", e.Type, "subtype", e.Subtype, "code", e.Code, "log_id", e.LogID,
			"retry_after", e.RetryAfter}
		if e.APICode != 0 {
			attrs = append(attrs, "api_code", e.APICode, "api_msg", e.APIMessage)
		} else {
			attrs = append(attrs, "error", e.Message)
		}
		c.logger().WarnContext(ctx, "lark-cli response", attrs...)
		return
	}
	attrs := []any{"call", call, "cmd", args[0], "dur", dur, "bytes", len(stdout)}
	if n := pages(stderr); n > 0 {
		attrs = append(attrs, "pages", n)
	}
	c.logger().DebugContext(ctx, "lark-cli response", attrs...)
}

// logFailure records a call that never reached an envelope: the binary was
// missing, the lane wait ended, or the process died. It returns err so a
// caller can log and return in one line.
func (c *ExecClient) logFailure(ctx context.Context, call uint64, args []string, err error) error {
	level := slog.LevelWarn
	// A cancelled call is the caller leaving, not a fault; a deadline is.
	if errors.Is(err, context.Canceled) {
		level = slog.LevelDebug
	}
	c.logger().Log(ctx, level, "lark-cli failed", "call", call, "argv", ArgvLine(args), "err", err)
	return err
}

// ArgvLine renders argv for a log line. Nothing is elided and anything that
// needs it is quoted, so the line can be pasted back into a shell as it
// stands.
func ArgvLine(args []string) string {
	quoted := make([]string, len(args))
	for i, a := range args {
		quoted[i] = shellQuote(a)
	}
	return strings.Join(quoted, " ")
}

var shellBare = regexp.MustCompile(`^[A-Za-z0-9_@%+=:,./-]+$`)

func shellQuote(s string) string {
	if shellBare.MatchString(s) {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// pages counts lark-cli's "[page N]" progress lines on stderr, which is the
// closest thing to a request count there is: one invocation auto-paginates as
// many times as the result needs.
func pages(stderr []byte) int {
	n := 0
	for _, line := range bytes.Split(stderr, []byte("\n")) {
		if bytes.HasPrefix(bytes.TrimSpace(line), []byte("[page ")) {
			n++
		}
	}
	return n
}

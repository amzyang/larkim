package larkweb

import (
	"bytes"
	"cmp"
	"context"
	"io"
	"log/slog"
	"math"
	"net/http"
	"strconv"
	"time"
	"uuid"
)

// gatewayHost serves every tenant. A tenant's own host (gaotu.feishu.cn and
// the like) carries the document products; IM is this one, which is why no
// tenant name is configured anywhere in this package.
const gatewayHost = "internal-api-lark-api.feishu.cn"

const gatewayURL = "https://" + gatewayHost + "/im/gateway/"

// Pace is the gap between two mark-reads in a sweep. The applink gap exists
// for a desktop client that draws slowly; a POST needs none, but a sweep of a
// few dozen chats fired back to back is a burst the gateway's own limits were
// not measured against.
const Pace = 100 * time.Millisecond

// appID identifies the web client to the gateway. The web app reads it
// out of the page it was served from; the value is stable and the page fetch it
// would cost buys nothing but another way to fail.
const appID = "161471"

// webOrigin is the origin the web client presents. The gateway checks it, and
// it is not the host a reader would guess from the URL bar.
const webOrigin = "https://open-dev.feishu.cn"

// userAgent matches the browser whose session is being borrowed.
const userAgent = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/154.0.0.0 Safari/537.36"

// Client calls the web client's gateway.
//
// It has no timeout of its own: one caller's idea of too long is not another's,
// so the bound is the context's.
type Client struct {
	HTTP    *http.Client
	Cookies CookieSource
	Log     *slog.Logger
}

// MarkRead settles a chat's unread messages up to maxPosition.
//
// maxPosition is a watermark, so one call answers a whole chat however far
// behind it is. A negative position is larkim's thread-reply sentinel, which
// this call cannot settle and must not silently round up to something it can.
func (c *Client) MarkRead(ctx context.Context, chatID string, maxPosition int64) error {
	const op = "mark read"
	if chatID == "" {
		return &Error{Op: op, Reason: "no chat id"}
	}
	if maxPosition < 0 || maxPosition > math.MaxInt32 {
		return &Error{Op: op, Reason: "position out of range"}
	}
	_, err := c.do(ctx, op, readRequest{
		chatID:      chatID,
		maxPosition: int32(maxPosition),
	})
	return err
}

// maxReply bounds one reply. A chat listing is the largest thing asked for,
// and it carries each chat's last message along with it.
const maxReply = 16 << 20

// loggedBody bounds how much of a reply the debug line carries. A refusal fits
// whole; a chat listing would put every chat's last message in the log.
const loggedBody = 2 << 10

// do sends one payload and returns the reply's own payload once the gateway
// has taken it.
func (c *Client) do(ctx context.Context, op string, p Payload) ([]byte, error) {
	cookies, err := c.Cookies.Cookies(ctx, gatewayHost)
	if err != nil {
		return nil, err
	}
	body := encodePacket(uuid.New().String(), p)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, gatewayURL, bytes.NewReader(body))
	if err != nil {
		return nil, &Error{Op: op, Err: err}
	}
	cmd := strconv.Itoa(int(p.Cmd()))
	req.Header.Set("content-type", "application/x-protobuf")
	req.Header.Set("x-appid", appID)
	// The gateway routes on this header before it parses the body, so the
	// command travels twice and the two have to agree.
	req.Header.Set("x-command", cmd)
	req.Header.Set("x-command-version", "5.7.0")
	req.Header.Set("x-web-version", "3.9.32")
	req.Header.Set("x-lgw-os-type", "1")
	req.Header.Set("x-lgw-terminal-type", "2")
	req.Header.Set("x-source", "web")
	req.Header.Set("x-request-id", uuid.New().String())
	req.Header.Set("origin", webOrigin)
	req.Header.Set("referer", webOrigin+"/")
	req.Header.Set("user-agent", userAgent)
	for _, ck := range cookies {
		req.AddCookie(ck)
	}
	// Cookie names only. A value here is a live credential and answers no
	// question the names do not.
	c.log().Debug("gateway call", "op", op, "cmd", cmd, "cookies", cookieNames(cookies))

	resp, err := c.http().Do(req)
	if err != nil {
		return nil, &Error{Op: op, Err: err}
	}
	defer resp.Body.Close()
	// One byte past the bound tells an oversized reply from one that fits: a
	// cut on a field boundary would otherwise decode as a shorter listing.
	reply, err := io.ReadAll(io.LimitReader(resp.Body, maxReply+1))
	if err != nil {
		return nil, &Error{Op: op, HTTPStatus: resp.StatusCode, Err: err}
	}
	if len(reply) > maxReply {
		return nil, &Error{Op: op, HTTPStatus: resp.StatusCode, Reason: "reply larger than " + strconv.Itoa(maxReply) + " bytes"}
	}
	// Quoted: a protobuf reply is binary, and the escapes keep it one line.
	c.log().Debug("gateway reply", "op", op, "cmd", cmd, "http", resp.StatusCode, "bytes", len(reply),
		"body", strconv.Quote(string(reply[:min(len(reply), loggedBody)])))
	if resp.StatusCode != http.StatusOK {
		// The body is the only place the gateway says which part it refused;
		// it echoes the request, never the cookies.
		// Bounded like the debug line: this text reaches the log at warn on
		// every refusal, and a refusal fits whole in far less.
		return nil, &Error{Op: op, HTTPStatus: resp.StatusCode, Reason: "gateway refused: " + strconv.Quote(string(reply[:min(len(reply), loggedBody)]))}
	}
	status, payload, err := decodeReply(reply)
	if err != nil {
		return nil, &Error{Op: op, HTTPStatus: resp.StatusCode, Err: err}
	}
	if status != 0 {
		return nil, &Error{Op: op, HTTPStatus: resp.StatusCode, Status: status, Reason: "gateway refused"}
	}
	return payload, nil
}

func (c *Client) http() *http.Client {
	return cmp.Or(c.HTTP, noRedirects)
}

// noRedirects follows no redirect. The gateway answers in place; a redirect
// would carry the session cookie wherever it pointed, including from https
// to http on the same host, which Go's own redirect policy allows.
var noRedirects = &http.Client{
	CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
}

func (c *Client) log() *slog.Logger {
	return cmp.Or(c.Log, discardLog)
}

// discardLog is what a nil logger logs to.
var discardLog = slog.New(slog.DiscardHandler)

func cookieNames(cookies []*http.Cookie) []string {
	names := make([]string, 0, len(cookies))
	for _, ck := range cookies {
		names = append(names, ck.Name)
	}
	return names
}

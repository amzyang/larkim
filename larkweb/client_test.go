package larkweb

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/amzyang/larkim/internal/oplog"
	"github.com/stretchr/testify/require"
	"google.golang.org/protobuf/encoding/protowire"
)

// fakeJar stands in for a browser. Tests never read a real jar: that would want
// a Keychain prompt and a logged-in browser to pass.
type fakeJar struct {
	cookies []*http.Cookie
	err     error
}

func (j fakeJar) Cookies(context.Context, string) ([]*http.Cookie, error) {
	return j.cookies, j.err
}

func sessionJar() fakeJar {
	return fakeJar{cookies: []*http.Cookie{{Name: sessionCookie, Value: "s"}}}
}

// clientTo points a Client at a stub gateway, since the real one is reached
// only with a live session.
func clientTo(t *testing.T, h http.HandlerFunc, jar CookieSource) *Client {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	return &Client{
		HTTP:    &http.Client{Transport: redirectTo(t, srv.URL)},
		Cookies: jar,
	}
}

// redirectTo sends the client's fixed gateway URL at the stub instead.
type redirect struct {
	base *url.URL
	next http.RoundTripper
}

func redirectTo(t *testing.T, base string) http.RoundTripper {
	t.Helper()
	u, err := url.Parse(base)
	require.NoError(t, err)
	return redirect{base: u, next: http.DefaultTransport}
}

func (r redirect) RoundTrip(req *http.Request) (*http.Response, error) {
	req.URL.Scheme, req.URL.Host = r.base.Scheme, r.base.Host
	return r.next.RoundTrip(req)
}

func TestClient_MarkReadPostsTheWatermarkToTheGateway(t *testing.T) {
	var got *http.Request
	var body []byte
	c := clientTo(t, func(w http.ResponseWriter, r *http.Request) {
		got, body = r, readAll(t, r.Body)
	}, sessionJar())

	require.NoError(t, c.MarkRead(t.Context(), "oc_quiet", 4096))

	require.Equal(t, http.MethodPost, got.Method)
	require.Equal(t, "/im/gateway/", got.URL.Path)
	require.Equal(t, "application/x-protobuf", got.Header.Get("content-type"))
	// The header the gateway routes on has to agree with the envelope.
	require.Equal(t, "40", got.Header.Get("x-command"))
	require.Equal(t, appID, got.Header.Get("x-appid"))
	require.Equal(t, webOrigin, got.Header.Get("origin"))
	require.NotEmpty(t, got.Header.Get("x-request-id"))
	session, err := got.Cookie(sessionCookie)
	require.NoError(t, err)
	require.Equal(t, "s", session.Value)

	require.Equal(t, "oc_quiet", fieldString(t, payloadOf(t, body), fieldReadChatID))
	require.Equal(t, uint64(4096), fieldVarint(t, payloadOf(t, body), fieldReadMaxPosition))
}

func TestClient_AMissingSessionSendsNothing(t *testing.T) {
	jar := fakeJar{err: &Error{Op: "read cookies", Reason: "no Feishu web session"}}
	c := clientTo(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("no session should mean no request")
	}, jar)

	err := c.MarkRead(t.Context(), "oc_quiet", 1)

	require.ErrorContains(t, err, "no Feishu web session")
}

func TestClient_AnExpiredSessionIsAFailure(t *testing.T) {
	c := clientTo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
	}, sessionJar())

	err := c.MarkRead(t.Context(), "oc_quiet", 1)

	e, ok := errors.AsType[*Error](err)
	require.True(t, ok)
	require.Equal(t, http.StatusUnauthorized, e.HTTPStatus)
}

func TestClient_ANonZeroGatewayStatusIsAFailure(t *testing.T) {
	// HTTP 200 with a status inside is the shape a refusal takes, so reading
	// only the HTTP code would report this one as read.
	c := clientTo(t, func(w http.ResponseWriter, _ *http.Request) {
		reply := protowire.AppendTag(nil, fieldPacketStatus, protowire.VarintType)
		w.Write(protowire.AppendVarint(reply, 6))
	}, sessionJar())

	err := c.MarkRead(t.Context(), "oc_quiet", 1)

	e, ok := errors.AsType[*Error](err)
	require.True(t, ok)
	require.Equal(t, uint32(6), e.Status)
}

func TestClient_RefusesAReplyPastTheBound(t *testing.T) {
	// A cut that lands on a field boundary decodes as a shorter listing, so
	// an oversized reply has to be an error rather than read as far as it
	// fits.
	c := clientTo(t, func(w http.ResponseWriter, _ *http.Request) {
		w.Write(make([]byte, maxReply+1))
	}, sessionJar())

	_, err := c.do(t.Context(), "list chats", feedRequest{})

	require.ErrorContains(t, err, "reply larger than")
}

func TestClient_FollowsNoRedirect(t *testing.T) {
	// A redirect would carry the session cookie wherever it pointed.
	var followed bool
	c := &Client{Cookies: sessionJar()}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/elsewhere" {
			followed = true
			return
		}
		http.Redirect(w, r, "/elsewhere", http.StatusFound)
	}))
	t.Cleanup(srv.Close)
	c.HTTP = &http.Client{Transport: redirectTo(t, srv.URL), CheckRedirect: noRedirects.CheckRedirect}

	err := c.MarkRead(t.Context(), "oc_quiet", 1)

	require.Error(t, err)
	require.False(t, followed)
}

func TestClient_RejectsAThreadReplyPosition(t *testing.T) {
	// larkim writes thread replies at a negative position. This call cannot
	// settle one, and rounding it up to something it can would settle the
	// wrong messages.
	c := clientTo(t, func(http.ResponseWriter, *http.Request) {
		t.Fatal("an out-of-range position should not reach the gateway")
	}, sessionJar())

	require.Error(t, c.MarkRead(t.Context(), "oc_quiet", -3))
	require.Error(t, c.MarkRead(t.Context(), "oc_quiet", 1<<40))
	require.Error(t, c.MarkRead(t.Context(), "", 1))
}

func readAll(t *testing.T, r io.Reader) []byte {
	t.Helper()
	b, err := io.ReadAll(r)
	require.NoError(t, err)
	return b
}

func fieldString(t *testing.T, b []byte, want protowire.Number) string {
	t.Helper()
	v, _ := field(t, b, want)
	return string(v)
}

func fieldVarint(t *testing.T, b []byte, want protowire.Number) uint64 {
	t.Helper()
	_, v := field(t, b, want)
	return v
}

// field is one field's value, as bytes or as a varint by its wire type.
func field(t *testing.T, b []byte, want protowire.Number) ([]byte, uint64) {
	t.Helper()
	var bs []byte
	var v uint64
	found := false
	require.NoError(t, eachField(b, func(num protowire.Number, typ protowire.Type, raw []byte) error {
		if num != want {
			return nil
		}
		found = true
		switch typ {
		case protowire.BytesType:
			bs, _ = protowire.ConsumeBytes(raw)
		case protowire.VarintType:
			v, _ = protowire.ConsumeVarint(raw)
		}
		return nil
	}))
	require.True(t, found, "field %d absent", want)
	return bs, v
}

func TestClient_LogsTheCallUnderTheOperationWithItsRequestID(t *testing.T) {
	var sent string
	c := clientTo(t, func(w http.ResponseWriter, r *http.Request) {
		sent = r.Header.Get("x-request-id")
		w.Header().Set("X-Tt-Logid", "lg_web")
	}, sessionJar())
	var buf bytes.Buffer
	c.Log = oplog.NewText(&buf, slog.LevelDebug)
	ctx := oplog.With(t.Context(), "clear-badge")
	op, _ := oplog.From(ctx)

	require.NoError(t, c.MarkRead(ctx, "oc_quiet", 4096))

	var call, reply string
	for l := range strings.Lines(buf.String()) {
		switch {
		case strings.Contains(l, `msg="gateway call"`):
			call = l
		case strings.Contains(l, `msg="gateway reply"`):
			reply = l
		}
	}
	for _, l := range []string{call, reply} {
		require.Contains(t, l, "req_id="+sent, "the id the gateway saw is the one the log carries")
		require.Contains(t, l, "op_id="+op.ID)
	}
	require.Regexp(t, `dur_ms=\d+ `, reply)
	require.Contains(t, reply, "log_id=lg_web")
}

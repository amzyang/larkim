// Package oplog ties every record an operation logs to that operation. An
// operation is one thing somebody or something set going — a sweep, a
// discovery pass, a keypress — and it fans out into lark-cli and HTTP calls on
// lanes and goroutines whose lines interleave with every other operation's in
// the shared log. With names it once, at its root; Handler stamps the name
// and id onto every record logged under that ctx, so one grep follows it.
package oplog

import (
	"cmp"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"time"
)

// Op is an operation's name and the id that tells two of one kind apart.
type Op struct {
	Name string
	ID   string
}

type key struct{}

// With starts an operation named name, unless ctx is already inside one: a
// sweep somebody asked for by hand is their keypress, not a second operation.
func With(ctx context.Context, name string) context.Context {
	if _, ok := From(ctx); ok {
		return ctx
	}
	// Eight characters are ~40 bits: unique across the processes that share
	// the log file, which a per-process counter would not be.
	return context.WithValue(ctx, key{}, Op{Name: name, ID: rand.Text()[:8]})
}

// From returns the operation ctx is inside, if any.
func From(ctx context.Context) (Op, bool) {
	op, ok := ctx.Value(key{}).(Op)
	return op, ok
}

// NewText is a text logger to w that stamps operations. A logger built around
// a bare handler drops op and op_id without a word.
func NewText(w io.Writer, level slog.Leveler) *slog.Logger {
	return slog.New(Handler{slog.NewTextHandler(w, &slog.HandlerOptions{Level: level})})
}

// Handler adds op and op_id to every record logged with a ctx that is inside
// an operation. Only the *Context logging calls carry one.
type Handler struct{ slog.Handler }

func (h Handler) Handle(ctx context.Context, r slog.Record) error {
	if op, ok := From(ctx); ok {
		r.AddAttrs(slog.String("op", op.Name), slog.String("op_id", op.ID))
	}
	return h.Handler.Handle(ctx, r)
}

func (h Handler) WithAttrs(attrs []slog.Attr) slog.Handler {
	return Handler{h.Handler.WithAttrs(attrs)}
}

func (h Handler) WithGroup(name string) slog.Handler {
	return Handler{h.Handler.WithGroup(name)}
}

// Transport logs one line per HTTP round trip, under the request's ctx so it
// carries the operation. It names the endpoint by method, host and path only:
// a query or a header is where a token rides.
type Transport struct {
	Base http.RoundTripper
	Log  *slog.Logger
	// Name says which client made the call; several share a host.
	Name string
}

func (t Transport) RoundTrip(req *http.Request) (*http.Response, error) {
	started := time.Now()
	resp, err := cmp.Or[http.RoundTripper](t.Base, http.DefaultTransport).RoundTrip(req)
	// Until the headers arrive: the body is the caller's to read.
	dur := time.Since(started).Milliseconds()
	attrs := []any{"name", t.Name, "method", req.Method, "host", req.URL.Host, "path", req.URL.Path, "dur_ms", dur}
	level := slog.LevelDebug
	if err != nil {
		attrs = append(attrs, "err", err)
		// A cancelled call is the caller leaving, not a fault.
		if !errors.Is(err, context.Canceled) {
			level = slog.LevelWarn
		}
	} else {
		attrs = append(attrs, "status", resp.StatusCode)
		if resp.StatusCode >= http.StatusBadRequest {
			level = slog.LevelWarn
		}
	}
	cmp.Or(t.Log, discard).Log(req.Context(), level, "http call", attrs...)
	return resp, err
}

var discard = slog.New(slog.DiscardHandler)

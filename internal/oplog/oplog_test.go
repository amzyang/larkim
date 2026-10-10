package oplog

import (
	"bytes"
	"context"
	"errors"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// capture returns a NewText logger and what it writes.
func capture(level slog.Level) (*slog.Logger, *bytes.Buffer) {
	var buf bytes.Buffer
	return NewText(&buf, level), &buf
}

func TestWith_NamesAnOperationAndGivesItAnID(t *testing.T) {
	t.Parallel()
	ctx := With(t.Context(), "tick")
	op, ok := From(ctx)
	require.True(t, ok)
	require.Equal(t, "tick", op.Name)
	require.Len(t, op.ID, 8)

	other, _ := From(With(t.Context(), "tick"))
	require.NotEqual(t, op.ID, other.ID, "two operations of one kind are still two")
}

func TestWith_KeepsTheOperationAlreadyUnderway(t *testing.T) {
	t.Parallel()
	outer := With(t.Context(), "sync")
	inner := With(outer, "tick")
	a, _ := From(outer)
	b, _ := From(inner)
	require.Equal(t, a, b, "a sweep somebody asked for by hand stays their keypress")
}

func TestFrom_ReportsNoOperationOnAPlainContext(t *testing.T) {
	t.Parallel()
	_, ok := From(t.Context())
	require.False(t, ok)
}

func TestHandler_StampsEveryRecordLoggedUnderTheOperation(t *testing.T) {
	t.Parallel()
	log, buf := capture(slog.LevelDebug)
	ctx := With(t.Context(), "send")
	op, _ := From(ctx)

	log.InfoContext(ctx, "first")
	log.With("chat_id", "oc_a").WarnContext(ctx, "second")
	log.WithGroup("g").DebugContext(ctx, "third", "k", "v")
	log.Info("unrelated")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	require.Len(t, lines, 4)
	for _, l := range lines[:3] {
		require.Contains(t, l, "op=send")
		require.Contains(t, l, "op_id="+op.ID)
	}
	require.Contains(t, lines[1], "chat_id=oc_a", "attrs bound before survive the wrap")
	require.NotContains(t, lines[3], "op_id=", "a record outside every operation carries none")
}

func TestTransport_LogsACallUnderTheOperation(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {}))
	defer srv.Close()
	log, buf := capture(slog.LevelDebug)
	c := &http.Client{Transport: Transport{Log: log, Name: "todoist"}}
	ctx := With(t.Context(), "add-task")
	op, _ := From(ctx)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, srv.URL+"/api/v1/tasks?token=secret", nil)
	require.NoError(t, err)
	req.Header.Set("Authorization", "Bearer secret")
	resp, err := c.Do(req)
	require.NoError(t, err)
	resp.Body.Close()

	l := strings.TrimSpace(buf.String())
	require.Contains(t, l, "level=DEBUG")
	require.Contains(t, l, `msg="http call"`)
	require.Contains(t, l, "name=todoist")
	require.Contains(t, l, "method=POST")
	require.Contains(t, l, "path=/api/v1/tasks")
	require.Contains(t, l, "status=200")
	require.Contains(t, l, "dur_ms=")
	require.Contains(t, l, "op_id="+op.ID)
	require.NotContains(t, l, "secret", "neither the query nor a header reaches the log")
}

func TestTransport_WarnsOnARefusal(t *testing.T) {
	t.Parallel()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer srv.Close()
	log, buf := capture(slog.LevelInfo)
	c := &http.Client{Transport: Transport{Log: log, Name: "jev"}}

	resp, err := c.Get(srv.URL)
	require.NoError(t, err)
	resp.Body.Close()
	require.Contains(t, buf.String(), "level=WARN")
	require.Contains(t, buf.String(), "status=500")
}

func TestTransport_WarnsOnATransportError(t *testing.T) {
	t.Parallel()
	log, buf := capture(slog.LevelInfo)
	boom := errors.New("connection refused")
	c := &http.Client{Transport: Transport{Log: log, Name: "fetch", Base: failing{boom}}}

	_, err := c.Get("http://example.com/a.png")
	require.ErrorIs(t, err, boom)
	require.Contains(t, buf.String(), "level=WARN")
	require.Contains(t, buf.String(), `err="connection refused"`)
}

func TestTransport_KeepsACancelledCallOutOfTheWarnings(t *testing.T) {
	t.Parallel()
	log, buf := capture(slog.LevelInfo)
	c := &http.Client{Transport: Transport{Log: log, Name: "fetch", Base: failing{context.Canceled}}}

	_, err := c.Get("http://example.com/a.png")
	require.Error(t, err)
	require.Empty(t, buf.String(), "leaving is not a fault")
}

func TestTransport_NilLoggerDiscards(t *testing.T) {
	t.Parallel()
	c := &http.Client{Transport: Transport{Name: "fetch", Base: failing{errors.New("x")}}}
	_, err := c.Get("http://example.com/")
	require.Error(t, err)
}

type failing struct{ err error }

func (f failing) RoundTrip(*http.Request) (*http.Response, error) { return nil, f.err }

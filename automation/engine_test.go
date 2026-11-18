package automation

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

type recordingExecutor struct{ events []Event }

func (x *recordingExecutor) Execute(_ context.Context, _ Callback, event Event) Outcome {
	x.events = append(x.events, event)
	return Outcome{Status: "succeeded", Result: "confirmed"}
}

func setupEngine(t *testing.T) (*Engine, *recordingExecutor) {
	t.Helper()
	st, err := storetest.Open(t, filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	x := &recordingExecutor{}
	e := &Engine{Store: st, Rules: []Rule{testRule()}, Executor: x, Now: func() time.Time { return time.UnixMilli(100) }, Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	require.NoError(t, e.Initialize(t.Context()))
	return e, x
}

func ingest(t *testing.T, e *Engine, m store.Message) {
	t.Helper()
	require.NoError(t, e.Store.UpsertChats(t.Context(), []store.Chat{{ChatID: m.ChatID}}, 200))
	_, err := e.Store.UpsertMessages(t.Context(), []store.Message{m}, 200)
	require.NoError(t, err)
}

func TestEngine_RenderThenDispatchOnceAcrossRestart(t *testing.T) {
	t.Parallel()
	e, x := setupEngine(t)
	m := testMessage()
	ingest(t, e, m)
	require.NoError(t, e.Evaluate(t.Context()))
	jobs, err := e.Store.AutomationJobs(t.Context(), 20)
	require.NoError(t, err)
	require.Empty(t, jobs)
	require.NoError(t, e.Store.UpdateRendered(t.Context(), m.MessageID, m.Content, "", 201))
	require.NoError(t, e.Evaluate(t.Context()))
	require.NoError(t, e.Dispatch(t.Context()))
	require.Len(t, x.events, 1)
	require.Equal(t, "quota: claim", x.events[0].Text)
	require.NoError(t, e.Initialize(t.Context()))
	require.NoError(t, e.Store.UpdateRendered(t.Context(), m.MessageID, m.Content+" now", "", 202))
	require.NoError(t, e.Evaluate(t.Context()))
	require.NoError(t, e.Dispatch(t.Context()))
	require.Len(t, x.events, 1)
}

func TestEngine_SQLiteToSubprocessRoundTrip(t *testing.T) {
	t.Setenv("LARKIM_CALLBACK_TEST", "1")
	e, _ := setupEngine(t)
	e.Rules[0].Callback = Callback{Executable: os.Args[0], Args: []string{"-test.run=^TestCallbackProcess$", "--", "event"}, Timeout: 5 * time.Second}
	e.Executor = Exec{}
	require.NoError(t, e.Initialize(t.Context()))
	m := testMessage()
	m.Content = "quota: claim $(not-a-command)"
	ingest(t, e, m)
	require.NoError(t, e.Store.UpdateRendered(t.Context(), m.MessageID, m.Content, "", 201))
	require.NoError(t, e.Evaluate(t.Context()))
	require.NoError(t, e.Dispatch(t.Context()))
	jobs, err := e.Store.AutomationJobs(t.Context(), 10)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "succeeded", jobs[0].Status)
	require.Contains(t, jobs[0].Result, "message=om_quota text=quota: claim $(not-a-command)")
	t.Logf("callback evidence: job=%d status=%s result=%s", jobs[0].ID, jobs[0].Status, jobs[0].Result)
}

type blockingExecutor struct{ started chan Event }

func (x blockingExecutor) Execute(ctx context.Context, _ Callback, event Event) Outcome {
	x.started <- event
	<-ctx.Done()
	return Outcome{Status: "unknown", Result: "cancelled during execution"}
}

func TestEngine_EvaluatesWhileCallbackRunsAndJoinsOnCancel(t *testing.T) {
	t.Parallel()
	e, _ := setupEngine(t)
	x := blockingExecutor{started: make(chan Event, 1)}
	e.Executor = x
	m := testMessage()
	ingest(t, e, m)
	require.NoError(t, e.Store.UpdateRendered(t.Context(), m.MessageID, m.Content, "", 201))
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})
	go func() { defer close(done); e.Run(ctx) }()
	t.Cleanup(func() { cancel(); <-done })
	select {
	case <-x.started:
	case <-time.After(5 * time.Second):
		t.Fatal("callback never started")
	}
	m.MessageID = "om_second"
	ingest(t, e, m)
	require.NoError(t, e.Store.UpdateRendered(t.Context(), m.MessageID, m.Content, "", 201))
	require.Eventually(t, func() bool {
		jobs, err := e.Store.AutomationJobs(t.Context(), 10)
		return err == nil && len(jobs) == 2
	}, 5*time.Second, 10*time.Millisecond)
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("worker did not stop")
	}
	jobs, err := e.Store.AutomationJobs(t.Context(), 10)
	require.NoError(t, err)
	require.Equal(t, "pending", jobs[0].Status)
	require.Equal(t, "unknown", jobs[1].Status)
}

func TestEngine_ExcludesHistoricalMessagesAndCancelsRecalledJobs(t *testing.T) {
	t.Parallel()
	e, x := setupEngine(t)
	m := testMessage()
	m.CreateMs = 99
	ingest(t, e, m)
	require.NoError(t, e.Store.UpdateRendered(t.Context(), m.MessageID, m.Content, "", 201))
	require.NoError(t, e.Evaluate(t.Context()))
	jobs, err := e.Store.AutomationJobs(t.Context(), 20)
	require.NoError(t, err)
	require.Empty(t, jobs)
	m.CreateMs = 200
	ingest(t, e, m)
	require.NoError(t, e.Evaluate(t.Context()))
	m.Deleted = true
	ingest(t, e, m)
	require.NoError(t, e.Dispatch(t.Context()))
	require.Empty(t, x.events)
	jobs, err = e.Store.AutomationJobs(t.Context(), 20)
	require.NoError(t, err)
	require.Len(t, jobs, 1)
	require.Equal(t, "cancelled", jobs[0].Status)
}

package cli

import (
	"bytes"
	"context"
	"encoding/json/v2"
	"path/filepath"
	"testing"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/store/storetest"
	"github.com/stretchr/testify/require"
)

func automationApp(t *testing.T) (*App, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, err := storetest.Open(t, filepath.Join(dir, "larkim.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return &App{Out: new(bytes.Buffer), Err: new(bytes.Buffer), cfg: config.Config{DataDir: dir}}, st
}

func TestAutomationJobs_ListsJobsAsTextAndJSON(t *testing.T) {
	t.Parallel()
	a, st := automationApp(t)
	for i, status := range []string{"succeeded", "unknown"} {
		_, err := st.DB().ExecContext(t.Context(), `INSERT INTO automation_jobs
			(rule_id, message_id, fingerprint, payload, callback, status, result, created_ms)
			VALUES (?, ?, 'fp', '{}', '{}', ?, '', ?)`, "build-alert", []string{"om_first", "om_second"}[i], status, i+1)
		require.NoError(t, err)
	}
	cmd := a.automationCmd()
	cmd.SetArgs([]string{"jobs", "--limit", "1"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	text := a.Out.(*bytes.Buffer).String()
	for _, want := range []string{"job_id", "rule_id", "message_id", "status", "created", "om_second", "unknown"} {
		require.Contains(t, text, want)
	}
	require.NotContains(t, text, "om_first")

	a.Out.(*bytes.Buffer).Reset()
	a.jsonOut = true
	cmd = a.automationCmd()
	cmd.SetArgs([]string{"jobs"})
	require.NoError(t, cmd.ExecuteContext(t.Context()))
	var jobs []store.AutomationJob
	require.NoError(t, json.Unmarshal(a.Out.(*bytes.Buffer).Bytes(), &jobs))
	require.Len(t, jobs, 2)
	require.Equal(t, "om_second", jobs[0].MessageID)
	require.Equal(t, "unknown", jobs[0].Status)
}

func TestAutomationJobs_RejectsNonpositiveLimit(t *testing.T) {
	t.Parallel()
	for _, limit := range []string{"0", "-1"} {
		a, _ := automationApp(t)
		cmd := a.automationCmd()
		cmd.SetArgs([]string{"jobs", "--limit", limit})
		require.ErrorContains(t, cmd.ExecuteContext(t.Context()), "limit must be positive")
	}
}

func TestAutomationJobs_RegisteredWithCompletion(t *testing.T) {
	t.Parallel()
	root := New("test", "")
	cmd, _, err := root.Find([]string{"automation", "jobs"})
	require.NoError(t, err)
	require.Equal(t, "jobs", cmd.Name())
	require.Equal(t, "50", cmd.Flags().Lookup("limit").DefValue)
	require.NotNil(t, cmd.ValidArgsFunction)
}

func TestStartAutomation_StopJoinsAndCanceledStartupFails(t *testing.T) {
	t.Parallel()
	a, st := automationApp(t)
	stop, err := a.startAutomation(t.Context(), st)
	require.NoError(t, err)
	done := make(chan struct{})
	go func() {
		stop()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("automation did not stop")
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = a.startAutomation(ctx, st)
	require.ErrorIs(t, err, context.Canceled)
}

func TestFrozenKeys_AutomationNeedsRestart(t *testing.T) {
	t.Parallel()
	was := config.Default()
	now := config.Default()
	require.NoError(t, now.Set("automation", `[{id: build-alert, enabled: false}]`))
	require.Equal(t, []string{"automation"}, frozenKeys(was, now))
	require.Empty(t, frozenKeys(now, now))
}

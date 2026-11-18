package automation

import (
	"context"
	"encoding/json/v2"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func TestCallbackProcess(t *testing.T) {
	if os.Getenv("LARKIM_CALLBACK_TEST") != "1" {
		return
	}
	switch os.Args[len(os.Args)-1] {
	case "event":
		var event Event
		if err := json.UnmarshalRead(os.Stdin, &event); err != nil {
			os.Exit(2)
		}
		if err := json.MarshalWrite(os.Stdout, Outcome{Status: "succeeded", Result: fmt.Sprintf("job=%d message=%s text=%s", event.JobID, event.Message.MessageID, event.Text)}); err != nil {
			os.Exit(2)
		}
	case "success":
		fmt.Print(`{"status":"succeeded","result":"verified"}`)
	case "failure":
		fmt.Print(`{"status":"failed","result":"no action attempted"}`)
	case "malformed":
		fmt.Print(`not json`)
	case "exit":
		fmt.Print(`{"status":"succeeded"}`)
		os.Exit(3)
	case "timeout":
		time.Sleep(time.Minute)
	}
	os.Exit(0)
}

func TestExec_RequiresVerifiedStructuredOutcome(t *testing.T) {
	t.Setenv("LARKIM_CALLBACK_TEST", "1")
	for _, tc := range []struct{ mode, status string }{
		{"success", "succeeded"}, {"failure", "failed"}, {"malformed", "unknown"}, {"exit", "unknown"}, {"timeout", "unknown"},
	} {
		t.Run(tc.mode, func(t *testing.T) {
			cb := Callback{Executable: os.Args[0], Args: []string{"-test.run=^TestCallbackProcess$", "--", tc.mode}, Timeout: 2 * time.Second}
			if tc.mode == "timeout" {
				cb.Timeout = 20 * time.Millisecond
			}
			out := (Exec{}).Execute(t.Context(), cb, Event{Version: 1, JobID: 1})
			require.Equal(t, tc.status, out.Status)
		})
	}
}

func TestExec_StartFailureIsKnownNotExecuted(t *testing.T) {
	t.Parallel()
	out := (Exec{}).Execute(t.Context(), Callback{Executable: "/no-such-larkim-callback", Timeout: time.Second}, Event{})
	require.Equal(t, "failed", out.Status)
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	out = (Exec{}).Execute(ctx, Callback{Executable: "/usr/bin/true", Timeout: time.Second}, Event{})
	require.Equal(t, "failed", out.Status)
}

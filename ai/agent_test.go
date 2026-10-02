package ai

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"strings"
	"testing"
	"time"

	acp "github.com/coder/acp-go-sdk"
	"github.com/stretchr/testify/require"
)

// fakeAgentEnv, when set, turns the test binary into the agent the client
// launches, playing the scenario it names.
const fakeAgentEnv = "LARKIM_FAKE_ACP_AGENT"

func TestMain(m *testing.M) {
	if mode := os.Getenv(fakeAgentEnv); mode != "" {
		runFakeAgent(mode)
		return
	}
	os.Exit(m.Run())
}

// runFakeAgent serves one ACP connection on stdio. It reports what the client
// did — the model it chose, how it answered a permission request — as reply
// text, so a test reads it off the stream.
func runFakeAgent(mode string) {
	a := &fakeAgent{mode: mode, model: "fake/default"}
	a.conn = acp.NewAgentSideConnection(a, os.Stdout, os.Stdin)
	a.conn.SetLogger(slog.New(slog.DiscardHandler))
	<-a.conn.Done()
	os.Exit(0)
}

type fakeAgent struct {
	mode  string
	model string
	conn  *acp.AgentSideConnection
}

func (a *fakeAgent) Initialize(context.Context, acp.InitializeRequest) (acp.InitializeResponse, error) {
	if a.mode == "crash" {
		fmt.Fprintln(os.Stderr, "not logged in: run omp login")
		os.Exit(3)
	}
	return acp.InitializeResponse{ProtocolVersion: acp.ProtocolVersionNumber}, nil
}

func (a *fakeAgent) NewSession(context.Context, acp.NewSessionRequest) (acp.NewSessionResponse, error) {
	resp := acp.NewSessionResponse{SessionId: "s1"}
	if a.mode != "nomodels" {
		opts := acp.SessionConfigSelectOptionsUngrouped{
			{Value: "fake/default", Name: "Default"},
			{Value: "cursor/composer-2.5-fast", Name: "Composer 2.5 Fast"},
			{Value: "cursor/claude-opus-5-5", Name: "Opus 5.5"},
			{Value: "cursor/claude-sonnet-5-high", Name: "Sonnet 5 High"},
		}
		resp.ConfigOptions = []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{
			Id: "model", Name: "Model", Type: "select", Category: new(acp.SessionConfigOptionCategoryModel),
			CurrentValue: "fake/default", Options: acp.SessionConfigSelectOptions{Ungrouped: &opts},
		}}}
	}
	return resp, nil
}

func (a *fakeAgent) SetSessionConfigOption(_ context.Context, p acp.SetSessionConfigOptionRequest) (acp.SetSessionConfigOptionResponse, error) {
	a.model = string(p.ValueId.Value)
	return acp.SetSessionConfigOptionResponse{ConfigOptions: []acp.SessionConfigOption{}}, nil
}

func (a *fakeAgent) Prompt(ctx context.Context, p acp.PromptRequest) (acp.PromptResponse, error) {
	say := func(u acp.SessionUpdate) {
		_ = a.conn.SessionUpdate(ctx, acp.SessionNotification{SessionId: p.SessionId, Update: u})
	}
	switch a.mode {
	case "hang":
		say(acp.UpdateAgentMessageText("thinking about it"))
		<-ctx.Done()
		return acp.PromptResponse{}, ctx.Err()
	case "refuse":
		return acp.PromptResponse{StopReason: acp.StopReasonRefusal}, nil
	case "tool":
		say(acp.UpdateAgentMessageText("let me look at "))
		say(acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: "t1", Title: "$ cat ~/.ssh/id_rsa", Kind: acp.ToolKindExecute, Status: acp.ToolCallStatusPending}})
		<-ctx.Done()
		return acp.PromptResponse{}, ctx.Err()
	case "hist-ok":
		// The taught command, answered as the shell would: the call runs, its
		// result comes back, and the answer goes on.
		cmd := "larkim --config /Users/linlan/dev.yaml messages list --chat oc_quiet --json --limit 40"
		say(acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: "t1", Title: "$ " + cmd, Kind: acp.ToolKindExecute,
			RawInput: map[string]any{"command": cmd, "timeout": 30},
			Status:   acp.ToolCallStatusInProgress}})
		done := acp.ToolCallStatusCompleted
		say(acp.SessionUpdate{ToolCallUpdate: &acp.SessionToolCallUpdate{
			ToolCallId: "t1", Status: &done,
			RawOutput: map[string]any{"output": "[{},{},{},{}]"}}})
		say(acp.UpdateAgentMessageText("read four rows"))
		return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
	case "hist-bad", "hist-otherchat", "hist-read", "hist-shell":
		cmd := map[string]string{
			"hist-bad":       "rm -rf ~",
			"hist-otherchat": "larkim messages list --chat oc_elsewhere --json",
			"hist-read":      "larkim messages list --chat oc_quiet --json",
			"hist-shell":     "larkim messages list --chat oc_quiet --json; say pwned",
		}[a.mode]
		kind := acp.ToolKind(acp.ToolKindExecute)
		if a.mode == "hist-read" {
			kind = acp.ToolKindRead
		}
		say(acp.SessionUpdate{ToolCall: &acp.SessionUpdateToolCall{
			ToolCallId: "t1", Title: "$ " + cmd, Kind: kind,
			RawInput: map[string]any{"command": cmd}, Status: acp.ToolCallStatusInProgress}})
		<-ctx.Done()
		return acp.PromptResponse{}, ctx.Err()
	}
	say(acp.UpdateAgentThoughtText("private working"))
	perm, err := a.conn.RequestPermission(ctx, acp.RequestPermissionRequest{SessionId: p.SessionId,
		ToolCall: acp.ToolCallUpdate{ToolCallId: "t1"},
		Options: []acp.PermissionOption{
			{OptionId: "yes", Kind: acp.PermissionOptionKindAllowOnce, Name: "Allow"},
			{OptionId: "no", Kind: acp.PermissionOptionKindRejectOnce, Name: "Reject"},
		}})
	if err != nil {
		return acp.PromptResponse{}, err
	}
	outcome := "cancelled"
	if s := perm.Outcome.Selected; s != nil {
		outcome = string(s.OptionId)
	}
	prompt := p.Prompt[0].Text.Text
	say(acp.UpdateAgentMessageText("model=" + a.model))
	say(acp.UpdateAgentMessageText(" permission=" + outcome))
	say(acp.UpdateAgentMessageText(fmt.Sprintf(" system=%t transcript=%t",
		strings.HasPrefix(prompt, system), strings.Contains(prompt, "<data>\nchat: 平台组\n</data>"))))
	return acp.PromptResponse{StopReason: acp.StopReasonEndTurn}, nil
}

func (*fakeAgent) Authenticate(context.Context, acp.AuthenticateRequest) (acp.AuthenticateResponse, error) {
	return acp.AuthenticateResponse{}, nil
}

func (*fakeAgent) Logout(context.Context, acp.LogoutRequest) (acp.LogoutResponse, error) {
	return acp.LogoutResponse{}, nil
}

func (*fakeAgent) Cancel(context.Context, acp.CancelNotification) error { return nil }

func (*fakeAgent) CloseSession(context.Context, acp.CloseSessionRequest) (acp.CloseSessionResponse, error) {
	return acp.CloseSessionResponse{}, nil
}

func (*fakeAgent) ListSessions(context.Context, acp.ListSessionsRequest) (acp.ListSessionsResponse, error) {
	return acp.ListSessionsResponse{}, nil
}

func (*fakeAgent) ResumeSession(context.Context, acp.ResumeSessionRequest) (acp.ResumeSessionResponse, error) {
	return acp.ResumeSessionResponse{}, nil
}

func (*fakeAgent) SetSessionMode(context.Context, acp.SetSessionModeRequest) (acp.SetSessionModeResponse, error) {
	return acp.SetSessionModeResponse{}, nil
}

// fakeClient is a Client whose agent is this test binary playing mode.
func fakeClient(t *testing.T, mode, model string) *Client {
	t.Setenv(fakeAgentEnv, mode)
	exe, err := os.Executable()
	require.NoError(t, err)
	return New([]string{exe}, model, t.TempDir(), slog.New(slog.DiscardHandler))
}

// drain reads a stream to its end, returning the text and the closing chunk.
func drain(t *testing.T, ch <-chan Chunk) (string, Chunk) {
	var b strings.Builder
	timeout := time.After(20 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			require.True(t, ok, "the stream closes only after its Done chunk")
			b.WriteString(c.Text)
			if c.Done {
				return b.String(), c
			}
		case <-timeout:
			t.Fatal("stream did not finish")
		}
	}
}

func TestStream_AnswersFromTheChosenModelWithToolsRefused(t *testing.T) {
	c := fakeClient(t, "ok", "composer-2.5")
	text, end := drain(t, c.Stream(t.Context(), "chat: 平台组", "Summarize"))
	require.NoError(t, end.Err)
	require.Equal(t, "model=cursor/composer-2.5-fast permission=no system=true transcript=true", text,
		"the thought stays out of the answer and the tool call is turned down")
}

func TestStream_AnEmptyModelLeavesTheAgentsDefault(t *testing.T) {
	c := fakeClient(t, "nomodels", "")
	text, end := drain(t, c.Stream(t.Context(), "chat: 平台组", "Summarize"))
	require.NoError(t, end.Err)
	require.True(t, strings.HasPrefix(text, "model=fake/default"))
}

func TestStream_AModelTheAgentLacksIsNamedWithTheChoices(t *testing.T) {
	c := fakeClient(t, "ok", "gemini")
	text, end := drain(t, c.Stream(t.Context(), "chat: 平台组", "Summarize"))
	require.Empty(t, text, "nothing is asked of a model nobody chose")
	require.ErrorContains(t, end.Err, `model "gemini" is not offered; have: fake/default, cursor/composer-2.5-fast`)
}

func TestStream_AStopOtherThanEndTurnIsAnError(t *testing.T) {
	c := fakeClient(t, "refuse", "")
	_, end := drain(t, c.Stream(t.Context(), "chat: 平台组", "Summarize"))
	require.ErrorContains(t, end.Err, "stopped: refusal")
}

// The permission refusals are not the gate: omp runs read and fetch tools
// without asking, so the tool call itself has to end the answer.
func TestStream_AToolCallEndsTheAnswer(t *testing.T) {
	c := fakeClient(t, "tool", "")
	text, end := drain(t, c.Stream(t.Context(), "chat: 平台组", "Summarize"))
	require.Equal(t, "let me look at ", text, "what arrived before the tool call is kept")
	require.ErrorContains(t, end.Err, "stopped: agent used $ cat ~/.ssh/id_rsa")
}

// A reader cancelling mid-answer is a stop, not a failure: the turn shows
// "stopped" and offers Retry rather than an error.
func TestStream_CancellingEndsAsAStop(t *testing.T) {
	c := fakeClient(t, "hang", "")
	ctx, cancel := context.WithCancel(t.Context())
	ch := c.Stream(ctx, "chat: 平台组", "Summarize")
	require.Equal(t, "thinking about it", (<-ch).Text)
	cancel()
	end := drainRest(t, ch)
	require.NoError(t, end.Err)
	require.True(t, end.Stopped)
}

// drainRest reads what is left of a stream whose first chunk was taken.
func drainRest(t *testing.T, ch <-chan Chunk) Chunk {
	t.Helper()
	timeout := time.After(10 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			if c.Done || !ok {
				return c
			}
		case <-timeout:
			t.Fatal("stream did not finish")
		}
	}
}

func TestStream_AnAgentThatDiesSaysWhyThroughItsStderr(t *testing.T) {
	c := fakeClient(t, "crash", "")
	_, end := drain(t, c.Stream(t.Context(), "chat: 平台组", "Summarize"))
	require.Error(t, end.Err)
	require.Contains(t, end.Err.Error(), "not logged in: run omp login")
}

func TestStream_CancellingStopsTheAgent(t *testing.T) {
	c := fakeClient(t, "hang", "")
	ctx, cancel := context.WithCancel(t.Context())
	ch := c.Stream(ctx, "chat: 平台组", "Summarize")
	first := <-ch
	require.Equal(t, "thinking about it", first.Text)
	cancel()
	done := make(chan struct{})
	go func() {
		for range ch {
		}
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("a cancelled stream kept its agent running")
	}
}

func TestPickModel_ASubstringNamingSeveralModelsListsThem(t *testing.T) {
	opts := acp.SessionConfigSelectOptionsGrouped{{Group: "cursor", Name: "Cursor", Options: []acp.SessionConfigSelectOption{
		{Value: "cursor/claude-opus-5-5", Name: "Opus 5.5"},
		{Value: "cursor/claude-sonnet-5-high", Name: "Sonnet 5 High"},
	}}}
	sel := []acp.SessionConfigOption{{Select: &acp.SessionConfigOptionSelect{Id: "model",
		Category: new(acp.SessionConfigOptionCategoryModel), Options: acp.SessionConfigSelectOptions{Grouped: &opts}}}}

	id, v, err := pickModel(sel, "OPUS")
	require.NoError(t, err)
	require.Equal(t, acp.SessionConfigId("model"), id)
	require.Equal(t, acp.SessionConfigValueId("cursor/claude-opus-5-5"), v, "names match case-insensitively")

	_, _, err = pickModel(sel, "claude")
	require.EqualError(t, err, `model "claude" matches several models; have: cursor/claude-opus-5-5, cursor/claude-sonnet-5-high`)

	_, _, err = pickModel(nil, "claude")
	require.EqualError(t, err, `model "claude": the agent offers no model choice`)
}

func TestTail_KeepsTheEnd(t *testing.T) {
	tl := &tail{max: 4}
	_, _ = io.WriteString(tl, "abc")
	_, _ = io.WriteString(tl, "defg")
	require.Equal(t, "defg", tl.String())
}

// drainTraces is drain with the trace lines the history gate leaves.
func drainTraces(t *testing.T, ch <-chan Chunk) (string, []string, Chunk) {
	t.Helper()
	var b strings.Builder
	var traces []string
	timeout := time.After(20 * time.Second)
	for {
		select {
		case c, ok := <-ch:
			require.True(t, ok, "the stream closes only after its Done chunk")
			b.WriteString(c.Text)
			if c.Trace != "" {
				traces = append(traces, c.Trace)
			}
			if c.Done {
				return b.String(), traces, c
			}
		case <-timeout:
			t.Fatal("stream did not finish")
		}
	}
}

// The taught command runs, its result leaves its trace, and the answer goes
// on to finish.
func TestStreamHistory_TheTaughtCommandRunsAndTracesItsRows(t *testing.T) {
	c := fakeClient(t, "hist-ok", "")
	text, traces, end := drainTraces(t, c.StreamHistory(t.Context(), "chat: 平台组", "再早的呢",
		History{ChatID: "oc_quiet", ConfigPath: "/Users/linlan/dev.yaml"}))
	require.NoError(t, end.Err)
	require.Equal(t, "read four rows", text, "the turn was not cancelled by the call")
	require.Equal(t, []string{"⌕ larkim --config /Users/linlan/dev.yaml messages list --chat oc_quiet --json --limit 40 · 4 rows"}, traces)
}

// The gate passes only the taught command for this chat: anything else —
// another command, another chat, a tool that is not the shell, a shell that
// would run more than the one command — cancels the turn.
func TestStreamHistory_AnythingElseCancelsTheTurn(t *testing.T) {
	for _, mode := range []string{"hist-bad", "hist-otherchat", "hist-read", "hist-shell"} {
		t.Run(mode, func(t *testing.T) {
			c := fakeClient(t, mode, "")
			_, _, end := drainTraces(t, c.StreamHistory(t.Context(), "chat: 平台组", "看看",
				History{ChatID: "oc_quiet"}))
			require.Error(t, end.Err)
			require.Contains(t, end.Err.Error(), "stopped: agent used",
				"the call itself is what the reader sees stopped the answer")
		})
	}
}

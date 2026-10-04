package tui

import (
	"context"
	"testing"
	"time"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/jev"
	"github.com/amzyang/larkim/markread"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// setModel is a model with a live key at something other than its default.
func setModel(t *testing.T) Model {
	t.Helper()
	m := pickerModel(t)
	m.cfg.AI.Context = 40
	return m
}

func TestRunSet_RebuildsTheBadgeClearerForTheNewBrowser(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	var built []config.MarkRead
	m.deps.NewClearBadge = func(cfg config.MarkRead) markread.Clear {
		built = append(built, cfg)
		return func(context.Context, store.ChatUnread) error { return nil }
	}

	m = m.runSet("mark_read.browser=edge")

	require.Equal(t, []config.MarkRead{{Browser: "edge"}}, built)
}

func TestRunSet_KeepsAnInjectedBadgeClearer(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	calls := 0
	m.deps.ClearBadge = func(context.Context, store.ChatUnread) error { calls++; return nil }
	m.deps.NewClearBadge = nil

	m = m.runSet("mark_read.browser=edge")

	require.NoError(t, m.deps.ClearBadge(t.Context(), store.ChatUnread{ChatID: "oc_quiet"}))
	require.Equal(t, 1, calls)
}

func TestRunSet_ReportsWhatItWasAskedWithoutWriting(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"ai.context?", "ai.context"} {
		m := setModel(t).runSet(line)
		require.Equal(t, "ai.context=40", m.notice, ":set %s", line)
		require.Equal(t, 40, m.cfg.AI.Context, ":set %s wrote something", line)
	}
}

func TestRunSet_RestoresTheDefault(t *testing.T) {
	t.Parallel()
	m := setModel(t).runSet("ai.context&")
	require.Equal(t, config.Default().AI.Context, m.cfg.AI.Context)
}

func TestRunSet_ListsEveryOptionWhenGivenNothing(t *testing.T) {
	t.Parallel()
	m := setModel(t).runSet("")
	require.Equal(t, "mark_read.browser=chrome  ai.agent=omp --mode acp  ai.model=cursor/composer-2.5-fast  ai.context=40"+
		"  ai.jev_key_env=TYPESAFE_API_KEY  ai.jev_endpoint=https://api.typesafe.ai/v1/systemone  ai.history=false  todoist.token=  todoist.project_id=", m.notice)
	require.NotContains(t, m.notice, "poll_interval_ms", "a sweep key is out of reach while a daemon owns the sweep")
	require.NotContains(t, m.notice, "data_dir", "and a key read once at startup never comes into reach")
}

func TestRunSet_RebuildsTheAssistantOnANewModel(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	var asked []string
	m.deps.NewAI = func(agent, model string) AIStreamer {
		asked = append(asked, agent+" | "+model)
		return nil
	}
	m = m.runSet("ai.model=cursor/claude-opus-5-5")
	require.Equal(t, []string{"omp --mode acp | cursor/claude-opus-5-5"}, asked)
	require.Equal(t, "cursor/claude-opus-5-5", m.cfg.AI.Model)
}

func TestRunSet_AnEmptyAgentIsTurnedDown(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	m.deps.NewAI = func(string, string) AIStreamer {
		t.Fatal("no assistant is built from an empty command")
		return nil
	}
	m = m.runSet("ai.agent=")
	require.Equal(t, "omp --mode acp", m.cfg.AI.Agent)
}

func TestRunSet_RebuildsTheSuggesterOnANewKeyVariable(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	var asked []string
	m.deps.NewSuggest = func(keyEnv, endpoint string) ReactSuggester {
		asked = append(asked, keyEnv+" "+endpoint)
		return nil
	}
	m = m.runSet("ai.jev_key_env=OTHER_KEY")
	require.Equal(t, []string{"OTHER_KEY " + jev.DefaultEndpoint}, asked)
	require.Equal(t, "OTHER_KEY", m.cfg.AI.JevKeyEnv)
}

func TestRunSet_RefusesADurationSpelling(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	m.deps.Embedded = true
	m = m.runSet("poll_interval_ms=1500ms")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "milliseconds")
	require.Equal(t, 3000, m.cfg.PollIntervalMS)
}

func TestRunSet_RefusesAGapOfNothing(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	m.deps.Embedded = true
	was := m.cfg.PollIntervalMS
	m = m.runSet("poll_interval_ms=0")
	require.True(t, m.noticeErr)
	require.Equal(t, was, m.cfg.PollIntervalMS)
}

func TestRunSet_LeavesASweepKeyAloneWhereADaemonOwnsTheSweep(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	require.False(t, m.deps.Embedded)
	was := m.deps.Syncer.Opt().PollInterval
	m = m.runSet("poll_interval_ms=5000")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "poll_interval_ms")
	require.Equal(t, was, m.deps.Syncer.Opt().PollInterval)
}

func TestRunSet_RetunesTheSweepRunningHere(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	m.deps.Embedded = true
	m = m.runSet("poll_interval_ms=5000")
	require.False(t, m.noticeErr, m.notice)
	require.Equal(t, 5*time.Second, m.deps.Syncer.Opt().PollInterval)
	require.Equal(t, 5000, m.cfg.PollIntervalMS)
}

func TestRunSet_RaisesAPaceUnderTheFloorTheFileWouldRaise(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	m.deps.Embedded = true
	m = m.runSet("poll_interval_ms=1")
	require.Equal(t, 100, m.cfg.PollIntervalMS)
	require.Equal(t, 100*time.Millisecond, m.deps.Syncer.Opt().PollInterval)
}

func TestRunSet_RebuildsTheTaskFilerOnANewToken(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	var asked []string
	m.deps.NewTodoist = func(token, projectID string) TaskAdder {
		asked = append(asked, token+" "+projectID)
		return nil
	}
	m = m.runSet("todoist.token=tok_a")
	require.Equal(t, []string{"tok_a "}, asked)
	require.Equal(t, "tok_a", m.cfg.Todoist.Token)
}

func TestRunSet_PutsTheSettleLeverOnTheSweepRunningHere(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	m.deps.Embedded = true
	m.deps.NewClearBadge = func(config.MarkRead) markread.Clear {
		return func(context.Context, store.ChatUnread) error { return nil }
	}
	require.Nil(t, m.deps.Syncer.SettleSilenced(), "silence_sync is still off")

	m = m.runSet("silence_sync=true")
	require.False(t, m.noticeErr, m.notice)
	require.NotNil(t, m.deps.Syncer.SettleSilenced())

	m = m.runSet("silence_sync=false")
	require.Nil(t, m.deps.Syncer.SettleSilenced(), "and off again takes it back off")
}

func TestSettings_NameEveryConfigKeyInOrder(t *testing.T) {
	t.Parallel()
	var keys []string
	for _, s := range settings {
		keys = append(keys, s.key)
	}
	require.Equal(t, config.Keys(), keys)
	for _, s := range settings {
		require.NotEmpty(t, s.help, s.key)
	}
}

package tui

import (
	"context"
	"testing"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/jev"
	"github.com/amzyang/larkim/markread"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// setModel is a model with the pace at something other than the default, so a
// report has a value to report and a restore has a change to show.
func setModel(t *testing.T) Model {
	t.Helper()
	m := pickerModel(t)
	m.cfg.ApplinkPaceMS = 40
	return m
}

func TestRunSet_RebuildsTheBadgeClearerForTheNewMode(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	var built []config.MarkRead
	m.deps.NewClearBadge = func(cfg config.MarkRead) markread.Clear {
		built = append(built, cfg)
		return func(context.Context, store.ChatUnread) error { return nil }
	}

	m = m.runSet("mark_read.mode=web")

	require.Equal(t, []config.MarkRead{{Mode: config.MarkReadWeb, Browser: "chrome"}}, built)
	require.Equal(t, markread.Pace(m.cfg), m.applinkPace(), "the queue takes the web lever's pace")
}

func TestRunSet_KeepsAnInjectedBadgeClearer(t *testing.T) {
	t.Parallel()
	// A test that injects a fake clearer and flips the mode must not end up
	// reading the browser's cookies or posting to the gateway.
	m := setModel(t)
	calls := 0
	m.deps.ClearBadge = func(context.Context, store.ChatUnread) error { calls++; return nil }
	m.deps.NewClearBadge = nil

	m = m.runSet("mark_read.mode=web")

	require.NoError(t, m.deps.ClearBadge(t.Context(), store.ChatUnread{ChatID: "oc_quiet"}))
	require.Equal(t, 1, calls)
}

func TestRunSet_RefusesAMarkReadModeNothingImplements(t *testing.T) {
	t.Parallel()
	m := setModel(t).runSet("mark_read.mode=webb")
	require.Contains(t, m.notice, `"webb" is not applink or web`)
	require.Equal(t, config.MarkReadApplink, m.cfg.MarkRead.Mode)
}

func TestRunSet_RetunesTheGapTheQueueTicksOn(t *testing.T) {
	t.Parallel()
	m := setModel(t).runSet("applink_pace_ms=1500")
	require.Equal(t, 1500*time.Millisecond, m.applinkPace())
	require.Equal(t, "applink_pace_ms=1500", m.notice)
	require.False(t, m.noticeErr)
}

func TestRunSet_ReportsWhatItWasAskedWithoutWriting(t *testing.T) {
	t.Parallel()
	for _, line := range []string{"applink_pace_ms?", "applink_pace_ms"} {
		m := setModel(t).runSet(line)
		require.Equal(t, "applink_pace_ms=40", m.notice, ":set %s", line)
		require.Equal(t, 40*time.Millisecond, m.applinkPace(), ":set %s wrote something", line)
	}
}

func TestRunSet_RestoresTheDefault(t *testing.T) {
	t.Parallel()
	m := setModel(t).runSet("applink_pace_ms&")
	require.Equal(t, applink.DefaultPace, m.applinkPace())
}

func TestRunSet_ListsEveryOptionWhenGivenNothing(t *testing.T) {
	t.Parallel()
	m := setModel(t).runSet("")
	require.Equal(t, "applink_pace_ms=40  mark_read.mode=applink  mark_read.browser=chrome  ai.agent=omp --mode acp  ai.model=cursor/composer-2.5-fast  ai.context=10"+
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
	// The unit is in the name, so 1500ms is the reader writing it twice.
	m := setModel(t).runSet("applink_pace_ms=1500ms")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "milliseconds")
	require.Equal(t, 40*time.Millisecond, m.applinkPace())
}

func TestRunSet_RefusesAGapOfNothing(t *testing.T) {
	t.Parallel()
	// Zero is not pacing; it is the bug this setting exists to fix.
	m := setModel(t).runSet("applink_pace_ms=0")
	require.True(t, m.noticeErr)
	require.Equal(t, 40*time.Millisecond, m.applinkPace())
}

func TestRunSet_LeavesASweepKeyAloneWhereADaemonOwnsTheSweep(t *testing.T) {
	t.Parallel()
	// The options the daemon ticks on are another process's; answering the
	// keystroke with a value that reaches nothing is worse than refusing it.
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
	// The floor is what keeps the loop from spinning over lark-cli, so a
	// session value has to pass through it like a written one.
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

func TestRunSet_RefusesSilenceSyncWithoutTheLeverThatCanSettle(t *testing.T) {
	t.Parallel()
	// The pair is what the next start would refuse, so it is refused here
	// rather than written into a file that will not load.
	m := setModel(t)
	m.deps.Embedded = true
	m = m.runSet("silence_sync=true")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "mark_read.mode: web")
	require.False(t, m.cfg.SilenceSync)
}

func TestRunSet_PutsTheSettleLeverOnTheSweepRunningHere(t *testing.T) {
	t.Parallel()
	m := setModel(t)
	m.deps.Embedded = true
	m.deps.NewClearBadge = func(config.MarkRead) markread.Clear {
		return func(context.Context, store.ChatUnread) error { return nil }
	}
	m = m.runSet("mark_read.mode=web")
	require.Nil(t, m.deps.Syncer.SettleSilenced(), "silence_sync is still off")

	m = m.runSet("silence_sync=true")
	require.False(t, m.noticeErr, m.notice)
	require.NotNil(t, m.deps.Syncer.SettleSilenced())

	m = m.runSet("silence_sync=false")
	require.Nil(t, m.deps.Syncer.SettleSilenced(), "and off again takes it back off")
}

func TestSettings_NameEveryConfigKeyInOrder(t *testing.T) {
	t.Parallel()
	// The panel walks the registry rather than config.Keys(), so a key added
	// to the file without a line of prose here would go unnamed there.
	var keys []string
	for _, s := range settings {
		keys = append(keys, s.key)
	}
	require.Equal(t, config.Keys(), keys)
	for _, s := range settings {
		require.NotEmpty(t, s.help, s.key)
	}
}

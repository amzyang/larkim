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
	m := setModel(t).runSet("mark_read.mode=webb")
	require.Contains(t, m.notice, `"webb" is not applink or web`)
	require.Equal(t, config.MarkReadApplink, m.cfg.MarkRead.Mode)
}

func TestRunSet_RetunesTheGapTheQueueTicksOn(t *testing.T) {
	m := setModel(t).runSet("applink_pace_ms=1500")
	require.Equal(t, 1500*time.Millisecond, m.applinkPace())
	require.Equal(t, "applink_pace_ms=1500", m.notice)
	require.False(t, m.noticeErr)
}

func TestRunSet_ReportsWhatItWasAskedWithoutWriting(t *testing.T) {
	for _, line := range []string{"applink_pace_ms?", "applink_pace_ms"} {
		m := setModel(t).runSet(line)
		require.Equal(t, "applink_pace_ms=40", m.notice, ":set %s", line)
		require.Equal(t, 40*time.Millisecond, m.applinkPace(), ":set %s wrote something", line)
	}
}

func TestRunSet_RestoresTheDefault(t *testing.T) {
	m := setModel(t).runSet("applink_pace_ms&")
	require.Equal(t, applink.DefaultPace, m.applinkPace())
}

func TestRunSet_ListsEveryOptionWhenGivenNothing(t *testing.T) {
	m := setModel(t).runSet("")
	require.Equal(t, "applink_pace_ms=40  mark_read.mode=applink  mark_read.browser=chrome  ai.model=claude-opus-5  ai.api_key_env=ANTHROPIC_API_KEY  ai.context=80"+
		"  ai.jev_key_env=TYPESAFE_API_KEY  ai.jev_endpoint=https://api.typesafe.ai/v1/systemone", m.notice)
	require.NotContains(t, m.notice, "poll_interval_ms", "a key read once at startup is not listed here")
}

func TestRunSet_RebuildsTheAssistantOnANewModel(t *testing.T) {
	m := setModel(t)
	var asked []string
	m.deps.NewAI = func(model, keyEnv string) AIStreamer {
		asked = append(asked, model+" "+keyEnv)
		return nil
	}
	m = m.runSet("ai.model=claude-sonnet-5")
	require.Equal(t, []string{"claude-sonnet-5 ANTHROPIC_API_KEY"}, asked)
	require.Equal(t, "claude-sonnet-5", m.cfg.AI.Model)
}

func TestRunSet_RebuildsTheSuggesterOnANewKeyVariable(t *testing.T) {
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
	// The unit is in the name, so 1500ms is the reader writing it twice.
	m := setModel(t).runSet("applink_pace_ms=1500ms")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "milliseconds")
	require.Equal(t, 40*time.Millisecond, m.applinkPace())
}

func TestRunSet_RefusesAGapOfNothing(t *testing.T) {
	// Zero is not pacing; it is the bug this setting exists to fix.
	m := setModel(t).runSet("applink_pace_ms=0")
	require.True(t, m.noticeErr)
	require.Equal(t, 40*time.Millisecond, m.applinkPace())
}

func TestRunSet_NamesAnOptionItHasNot(t *testing.T) {
	m := setModel(t).runSet("poll_interval_ms=5000")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "poll_interval_ms", "a config key that takes effect only at startup is not an option here")
}

func TestSettings_NameEveryConfigKeyInOrder(t *testing.T) {
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

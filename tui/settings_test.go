package tui

import (
	"testing"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/config"
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
	require.Equal(t, "applink_pace_ms=40  ai.model=claude-opus-5  ai.api_key_env=ANTHROPIC_API_KEY  ai.context=80", m.notice)
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

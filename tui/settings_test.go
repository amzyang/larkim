package tui

import (
	"testing"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/config"
	"github.com/stretchr/testify/require"
)

// setModel is a model on the : line, with the pace at something other than
// the default so a restore has a change to show.
func setModel(t *testing.T) Model {
	t.Helper()
	m := pickerModel(t)
	m.pace = 40 * time.Millisecond
	return m
}

func TestRunSet_RetunesTheGapTheQueueTicksOn(t *testing.T) {
	m := setModel(t).runSet("applink_pace_ms=1500")
	require.Equal(t, 1500*time.Millisecond, m.pace)
	require.Equal(t, "applink_pace_ms=1500", m.notice)
	require.False(t, m.noticeErr)
}

func TestRunSet_ReportsWhatItWasAskedWithoutWriting(t *testing.T) {
	for _, line := range []string{"applink_pace_ms?", "applink_pace_ms"} {
		m := setModel(t).runSet(line)
		require.Equal(t, "applink_pace_ms=40", m.notice, ":set %s", line)
		require.Equal(t, 40*time.Millisecond, m.pace, ":set %s wrote something", line)
	}
}

func TestRunSet_RestoresTheDefault(t *testing.T) {
	m := setModel(t).runSet("applink_pace_ms&")
	require.Equal(t, applink.DefaultPace, m.pace)
}

func TestRunSet_ListsEveryOptionWhenGivenNothing(t *testing.T) {
	m := setModel(t).runSet("")
	require.Equal(t, "applink_pace_ms=40", m.notice)
}

func TestRunSet_RefusesADurationSpelling(t *testing.T) {
	// The unit is in the name, so 1500ms is the reader writing it twice.
	m := setModel(t).runSet("applink_pace_ms=1500ms")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "milliseconds")
	require.Equal(t, 40*time.Millisecond, m.pace)
}

func TestRunSet_RefusesAGapOfNothing(t *testing.T) {
	// Zero is not pacing; it is the bug this setting exists to fix.
	m := setModel(t).runSet("applink_pace_ms=0")
	require.True(t, m.noticeErr)
	require.Equal(t, 40*time.Millisecond, m.pace)
}

func TestRunSet_NamesAnOptionItHasNot(t *testing.T) {
	m := setModel(t).runSet("poll_interval=5s")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "poll_interval", "a config key that takes effect only at startup is not an option here")
}

func TestSettings_NameTheirConfigKeys(t *testing.T) {
	// A value found worth keeping is moved into the config file under the
	// spelling it was tried with, so the two vocabularies are one.
	for _, s := range settings {
		require.Contains(t, config.Keys(), s.name, "option %s names no config key", s.name)
	}
}

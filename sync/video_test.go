package sync

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestParseVideoChat_ReadsTheCallOffTheBody(t *testing.T) {
	v, ok := ParseVideoChat(videoChatBody(1_000, 33_000))
	require.True(t, ok)
	assert.Equal(t, VideoChat{Topic: "站会的视频会议", MeetNumber: "100000000", StartMs: 1_000, EndMs: 33_000}, v)
	assert.False(t, v.Live())
	span, ended := v.Span()
	assert.True(t, ended)
	assert.Equal(t, int64(32_000), span)
}

func TestParseVideoChat_ACallWithoutAnEndIsStillRunning(t *testing.T) {
	// Feishu stamps end_time in the update that closes the call, so the
	// invite carries none for as long as the call is worth joining.
	v, ok := ParseVideoChat(`{"topic":"站会的视频会议","meet_number":"100000000","start_time":"1000"}`)
	require.True(t, ok)
	assert.True(t, v.Live())
	_, ended := v.Span()
	assert.False(t, ended)

	v, ok = ParseVideoChat(videoChatBody(1_000, 0))
	require.True(t, ok)
	assert.True(t, v.Live())
}

func TestParseVideoChat_RefusesABodyThatNamesNoCall(t *testing.T) {
	for _, raw := range []string{"", "not json", "{}", `{"text":"hi"}`, `{"topic":42}`} {
		_, ok := ParseVideoChat(raw)
		assert.False(t, ok, raw)
	}
}

func TestParseVideoChat_DropsAMeetingNumberThatIsNotDigits(t *testing.T) {
	// The number goes into a join link, so a value that cannot be one is
	// dropped rather than dialled.
	v, ok := ParseVideoChat(`{"topic":"站会的视频会议","meet_number":"852-073-321"}`)
	require.True(t, ok)
	assert.Empty(t, v.MeetNumber)
}

func TestVideoChatText_SpellsOutWhichCallItWas(t *testing.T) {
	assert.Equal(t, "[Video call] 站会的视频会议 · 100000000 · 32s", videoChatText(videoChatBody(1_000, 33_000)))
	assert.Equal(t, "[Video call] 站会的视频会议 · 100000000",
		videoChatText(`{"topic":"站会的视频会议","meet_number":"100000000","start_time":"1000"}`),
		"a running call has no length yet")
	assert.Equal(t, "[Video call] 100000000 · 32s",
		videoChatText(`{"meet_number":"100000000","start_time":"1000","end_time":"33000"}`))
	assert.Equal(t, "[Video call]", videoChatText("not json"),
		"a body larkim cannot read says what lark-cli would have said")
}

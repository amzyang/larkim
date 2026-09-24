package sync

import (
	"fmt"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// blankTemplate is a body that fills to nothing: a template of one space,
// which so far only ever marks a call ending.
const blankTemplate = `{"template":" ","from_user":[],"to_chatters":[],"divider_text":{}}`

func videoChatBody(startMs, endMs int64) string {
	return fmt.Sprintf(`{"topic":"站会的视频会议","meet_number":"100000000","start_time":"%d","end_time":"%d"}`, startMs, endMs)
}

func marker(contentRaw, callRaw string, createMs int64) store.PendingSystemMessage {
	return store.PendingSystemMessage{MessageID: "om_1", ContentRaw: contentRaw, CallRaw: callRaw, CreateMs: createMs}
}

// fill renders a body the way systemText does, asserting larkim can read it.
func fill(t *testing.T, contentRaw string) string {
	tmpl, body, ok := systemBody(contentRaw)
	require.True(t, ok)
	return fillSlots(tmpl, body)
}

func TestFillSlots_JoinsTheValuesTheBodyCarries(t *testing.T) {
	assert.Equal(t, "李明 invited 林岚, 孙琪 to the group.",
		fill(t, `{"template":"{from_user} invited {to_chatters} to the group.","from_user":["李明"],"to_chatters":["林岚","孙琪"],"divider_text":{}}`))
	assert.Equal(t, "2026年1月1日",
		fill(t, `{"template":"{divider_text}","divider_text":{"text":"2026年1月1日"}}`))
}

func TestFillSlots_UnfillableSlotReadsAsAnEllipsis(t *testing.T) {
	assert.Equal(t, `何静 updated the group name from "…" to "…".`,
		fill(t, `{"template":"{from_user} updated the group name from \"{old_group_name}\" to \"{group_name}\".","from_user":["何静"],"to_chatters":[],"divider_text":{}}`))
	assert.Equal(t, "段明轩 invited … to the group.",
		fill(t, `{"template":"{from_user} invited {to_chatters} to the group.","from_user":["段明轩"],"to_chatters":[]}`))
	assert.Equal(t, "… 同步了 … 条消息到本群组",
		fill(t, `{"template":"{} 同步了 {count} 条消息到本群组"}`))
}

func TestFillSlots_ABlankTemplateFillsToNothing(t *testing.T) {
	assert.Empty(t, fill(t, blankTemplate))
}

func TestSystemText_KeepsAMessageThatSpeaksForItself(t *testing.T) {
	// A template with text of its own is never a call marker, whatever call
	// happens to sit above it.
	assert.Equal(t, "何静 started the group chat.",
		systemText(marker(`{"template":"{from_user} started the group chat.","from_user":["何静"]}`,
			videoChatBody(1_000, 33_000), 33_909)))
}

func TestSystemText_TimesTheCallTheMarkerCloses(t *testing.T) {
	assert.Equal(t, "Meeting ended: 32s", systemText(marker(blankTemplate, videoChatBody(1_000, 33_000), 33_909)))
}

func TestSystemText_SaysOnlyThatACallEndedWhenNothingDatesIt(t *testing.T) {
	// A p2p call leaves no video_chat message, so its length is unknowable.
	assert.Equal(t, "Call ended", systemText(marker(blankTemplate, "", 33_909)))
	assert.Equal(t, "Call ended", systemText(marker(blankTemplate, `not json`, 33_909)))
	assert.Equal(t, "Call ended", systemText(marker(blankTemplate, videoChatBody(1_000, 0), 33_909)))
}

func TestSystemText_IgnoresACallThatIsNotTheOneClosing(t *testing.T) {
	// Feishu stamps the marker about a second after end_time; an older call
	// in the same chat must not lend it a duration, nor one still running.
	assert.Equal(t, "Call ended", systemText(marker(blankTemplate, videoChatBody(1_000, 33_000), 9_999_999)))
	assert.Equal(t, "Call ended", systemText(marker(blankTemplate, videoChatBody(1_000, 33_000), 32_000)))
	assert.Equal(t, "Meeting ended: 32s", systemText(marker(blankTemplate, videoChatBody(1_000, 33_000), 33_000)),
		"a marker stamped on the very millisecond still closes the call")
}

func TestSystemText_StaysSilentOnABodyItCannotRead(t *testing.T) {
	// Only a template that is present and blank marks a call ending. A body
	// larkim cannot read says nothing, and must not be guessed into one.
	assert.Empty(t, systemText(marker(`not json`, "", 33_909)))
	assert.Empty(t, systemText(marker(`{"from_user":["何静"]}`, "", 33_909)))
	assert.Empty(t, systemText(marker(`{"template":42}`, videoChatBody(1_000, 33_000), 33_909)))
}

func TestCallLength_ShowsTheTwoUnitsThatMatter(t *testing.T) {
	for _, c := range []struct {
		ms   int64
		want string
	}{
		{1_000, "1s"},
		{32_000, "32s"},
		{59_999, "59s"},
		{60_000, "1m"},
		{1_468_000, "24m28s"},
		{3_600_000, "1h"},
		{6_729_000, "1h52m"},
	} {
		assert.Equal(t, c.want, callLength(c.ms), "%dms", c.ms)
	}
}

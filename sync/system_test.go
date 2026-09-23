package sync

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestSystemText_JoinsTheValuesTheBodyCarries(t *testing.T) {
	assert.Equal(t, "李明 invited 林岚, 孙琪 to the group.",
		systemText(`{"template":"{from_user} invited {to_chatters} to the group.","from_user":["李明"],"to_chatters":["林岚","孙琪"],"divider_text":{}}`))
	assert.Equal(t, "2026年1月1日",
		systemText(`{"template":"{divider_text}","divider_text":{"text":"2026年1月1日"}}`))
}

func TestSystemText_UnfillableSlotReadsAsAnEllipsis(t *testing.T) {
	assert.Equal(t, `何静 updated the group name from "…" to "…".`,
		systemText(`{"template":"{from_user} updated the group name from \"{old_group_name}\" to \"{group_name}\".","from_user":["何静"],"to_chatters":[],"divider_text":{}}`))
	assert.Equal(t, "段明轩 invited … to the group.",
		systemText(`{"template":"{from_user} invited {to_chatters} to the group.","from_user":["段明轩"],"to_chatters":[]}`))
	assert.Equal(t, "… 同步了 … 条消息到本群组",
		systemText(`{"template":"{} 同步了 {count} 条消息到本群组"}`))
}

func TestSystemText_NothingToSayRendersEmpty(t *testing.T) {
	assert.Empty(t, systemText(`{"template":" ","from_user":[],"to_chatters":[],"divider_text":{}}`))
	assert.Empty(t, systemText(`{"from_user":["何静"]}`))
	assert.Empty(t, systemText(`not json`))
}

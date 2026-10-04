package sync

import (
	"testing"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func TestMiscText_SpellsEachKindTheWayLarkCLIDoes(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ name, msgType, raw, want string }{
		{"shared chat", "share_chat", `{"chat_id":"oc_quiet"}`, "[Chat card: oc_quiet]"},
		{"shared chat with no id", "share_chat", `{}`, "[Chat card]"},
		{"shared contact", "share_user", `{"user_id":"ou_a"}`, "[User card: ou_a]"},
		{"shared contact with no id", "share_user", `{}`, "[User card]"},
		{"location", "location", `{"name":"上海"}`, "[Location: 上海]"},
		{"location with no name", "location", `{}`, "[Location]"},
		{"folder", "folder", `{"file_key":"file_a","file_name":"周报"}`, `<folder key="file_a" name="周报"/>`},
		{"folder with no name", "folder", `{"file_key":"file_a"}`, `<folder key="file_a"/>`},
		{"folder with no key", "folder", `{}`, "[Folder]"},
		{"a folder name carrying a quote", "folder", `{"file_key":"file_a","file_name":"say \"hi\""}`,
			`<folder key="file_a" name="say \"hi\""/>`},
		{"red packet", "hongbao", `{"text":"恭喜发财"}`, `<hongbao text="恭喜发财"/>`},
		{"red packet with no greeting", "hongbao", `{}`, "<hongbao/>"},
		{"open poll", "vote", `{"topic":"午饭","options":["A","B"]}`, "<vote>\n午饭\n• A\n• B\n</vote>"},
		{"closed poll", "vote", `{"topic":"午饭","options":["A"],"status":1}`, "<vote>\n午饭\n• A\n(Closed)\n</vote>"},
		{"poll with no topic", "vote", `{"options":["A"]}`, "<vote>\n• A\n</vote>"},
		{"poll with nothing in it", "vote", `{}`, "<vote>\nvote\n</vote>"},
		{"a poll topic carrying a bracket", "vote", `{"topic":"a < b & c"}`, "<vote>\na &lt; b &amp; c\n</vote>"},
		{"a shared chat that will not parse", "share_chat", `not json`, "[Invalid chat card JSON]"},
		{"a shared contact that will not parse", "share_user", `not json`, "[Invalid user card JSON]"},
		{"a location that will not parse", "location", `not json`, "[Invalid location JSON]"},
		{"a folder that will not parse", "folder", `not json`, "[Invalid folder JSON]"},
		{"a red packet that will not parse", "hongbao", `not json`, "[Invalid hongbao JSON]"},
		{"a poll that will not parse", "vote", `not json`, "[Invalid vote JSON]"},
		// LocalMisc and this switch are one list read twice, so a type added
		// to the queue with no renderer here has to say so.
		{"a type this switch has not learned", "brand_new", `{}`, "[brand_new]"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.want, miscText(tc.msgType, tc.raw))
		})
	}
}

func TestTodoText_DatesTheDeadlineInTheGivenZone(t *testing.T) {
	t.Parallel()
	loc := time.FixedZone("CST", 8*60*60)
	// Seconds and milliseconds arrive in the same field, told apart by length.
	require.Equal(t, "☐ 写周报\nDue: 2026-09-29 18:00:00",
		todoText(`{"summary":{"title":"写周报"},"due_time":"1790676000"}`, false, loc))
	require.Equal(t, "☐ 写周报\nDue: 2026-09-29 18:00:00",
		todoText(`{"summary":{"title":"写周报"},"due_time":"1790676000000"}`, false, loc))
	// A deadline that reads as nothing leaves the line out rather than dating
	// the epoch.
	require.Equal(t, "☐ 写周报",
		todoText(`{"summary":{"title":"写周报"},"due_time":"0"}`, false, loc))
	require.Equal(t, "☐ 写周报",
		todoText(`{"summary":{"title":"写周报"},"due_time":""}`, false, loc))
}

func TestTodoText_MarksTheCompletionInTheLeadingBox(t *testing.T) {
	t.Parallel()
	body := `{"task_id":"task_a","summary":{"title":"写周报"},"due_time":"1790676000"}`
	require.Equal(t, "☐ 写周报\nDue: 2026-09-29 18:00:00", todoText(body, false, time.Local))
	require.Equal(t, "☑ 写周报\nDue: 2026-09-29 18:00:00", todoText(body, true, time.Local))
}

func TestTodoText_SpellsTheCard(t *testing.T) {
	t.Parallel()
	require.Equal(t, "☐ 写周报\n先收数",
		todoText(`{"task_id":"task_a","summary":{"title":"写周报","content":[[{"tag":"text","text":"先收数"}]]}}`, false, time.Local))
	require.Equal(t, "☐ 写周报", todoText(`{"summary":{"title":"写周报"}}`, false, time.Local))
	require.Equal(t, "☐ todo", todoText(`{}`, false, time.Local))
	require.Equal(t, "☐ a < b", todoText(`{"summary":{"title":"a < b"}}`, false, time.Local))
	require.Equal(t, "[Invalid todo JSON]", todoText(`not json`, false, time.Local))
}

func TestLocalMisc_CoversTheTypesMiscTextRenders(t *testing.T) {
	t.Parallel()
	for _, m := range []string{"share_chat", "share_user", "location", "folder", "vote", "hongbao", "todo"} {
		require.True(t, LocalMisc(m), m)
		require.True(t, store.LocallyRendered(m), m)
	}
	require.False(t, LocalMisc("text"))
	require.False(t, LocalMisc("merge_forward"))
}

func TestLocalText_NamesATypeNothingRenders(t *testing.T) {
	t.Parallel()
	// The render queue is split on one list, so a type added to it without a
	// renderer below would otherwise be stored as an empty rendering.
	require.Equal(t, "[brand_new]", localText(store.PendingLocalMessage{MessageID: "om_elsewhere", MsgType: "brand_new"}))
}

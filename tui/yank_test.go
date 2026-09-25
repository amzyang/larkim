package tui

import (
	"encoding/json"
	"fmt"
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// yankMsgs replaces the fixture's messages with ones carrying every column
// the y family reads.
func yankMsgs(m Model) Model {
	m.msgs = []store.Message{
		{MessageID: "om_a", ChatID: "oc_1", SenderName: "李四", SenderID: "ou_b",
			Content: "发布单 #4412 合了吗", ContentRaw: `{"text":"发布单 #4412 合了吗"}`,
			RawJSON:    `{"message_id":"om_a","body":{"content":"{\"text\":\"x\"}"}}`,
			RenderedAt: 1, CreateMs: 1000},
		{MessageID: "om_b", ChatID: "oc_1", SenderName: "张三", SenderID: "ou_a",
			Content: "panic: runtime error", ContentRaw: `{"text":"panic"}`,
			RawJSON: `{"message_id":"om_b"}`, RenderedAt: 1, CreateMs: 2000},
		{MessageID: "om_c", ChatID: "oc_1", SenderName: "林岚", SenderID: "ou_me",
			Content: "我看下", ContentRaw: `{"text":"我看下"}`,
			RawJSON: `{"message_id":"om_c"}`, RenderedAt: 1, CreateMs: 3000},
	}
	m.msgIdx = 0
	m.layout()
	return m
}

// pressYank drives the two keys the way the terminal delivers them, so the
// prefix state machine is under test and not just its payload.
func pressYank(m Model, second string) (Model, tea.Cmd) {
	mm, _ := m.onNormalKey("y")
	out, cmd := mm.(Model).onNormalKey(second)
	return out.(Model), cmd
}

// clipboard reads back what a copy put on the clipboard. Bubble Tea's message
// type is unexported, but it is a string underneath.
func clipboard(t *testing.T, cmd tea.Cmd) string {
	t.Helper()
	require.NotNil(t, cmd, "the copy produced no clipboard command")
	return fmt.Sprint(cmd())
}

func TestOnYankKey_CopiesOneStoredFieldPerKey(t *testing.T) {
	base := yankMsgs(sized(120, 36))
	base.focus = paneMessages

	out, cmd := pressYank(base, "y")
	require.Equal(t, "om_a", clipboard(t, cmd))
	require.Equal(t, "copied om_a", out.notice, "an id is short enough to report in full")

	out, cmd = pressYank(base, "c")
	require.Equal(t, "发布单 #4412 合了吗", clipboard(t, cmd))
	require.Contains(t, out.notice, "copied content ·")
}

func TestOnYankKey_RawJSONIsIndentedButUnchanged(t *testing.T) {
	m := yankMsgs(sized(120, 36))
	m.focus = paneMessages

	out, cmd := pressYank(m, "r")
	got := clipboard(t, cmd)
	require.Contains(t, got, "\n  ", "the payload is indented so it reads without a trip through jq")

	var want, have any
	require.NoError(t, json.Unmarshal([]byte(m.msgs[0].RawJSON), &want))
	require.NoError(t, json.Unmarshal([]byte(got), &have))
	require.Equal(t, want, have, "indenting must not change the payload")
	require.Contains(t, out.notice, "copied raw json ·")
}

func TestOnNormalKey_UnboundSecondKeyCancelsTheYPrefix(t *testing.T) {
	m := yankMsgs(sized(120, 36))
	m.focus, m.msgIdx = paneMessages, 1

	mm, cmd := m.onNormalKey("y")
	require.Nil(t, cmd)
	require.True(t, mm.(Model).pendingY)
	require.Contains(t, mm.(Model).notice, "y id · r json · c content")

	out, cmd := mm.(Model).onNormalKey("j")
	require.Nil(t, cmd, "the clipboard is left alone")
	require.False(t, out.(Model).pendingY)
	require.Empty(t, out.(Model).notice)
	require.Equal(t, 1, out.(Model).msgIdx, "the key the prefix ate must not also move the cursor")
}

func TestOnVisualKey_TheYPrefixEatsTheExtendKey(t *testing.T) {
	m := yankMsgs(sized(120, 36))
	m.focus, m.msgIdx = paneMessages, 0
	mm, _ := m.onNormalKey("v")
	mm, _ = mm.(Model).onVisualKey("j")
	require.Equal(t, 1, mm.(Model).msgIdx)

	mm, _ = mm.(Model).onVisualKey("y")
	out, cmd := mm.(Model).onVisualKey("j")
	require.Nil(t, cmd, "j copies nothing, so the clipboard keeps what was on it")
	require.Equal(t, 1, out.(Model).msgIdx, "the key the prefix ate must not also extend the range")
	require.Equal(t, modeVisual, out.(Model).mode, "a cancelled prefix leaves the selection standing")
}

func TestOnVisualKey_YFamilyCopiesTheWholeRangeAndLeaves(t *testing.T) {
	base := yankMsgs(sized(120, 36))
	base.focus, base.msgIdx = paneMessages, 0
	mm, _ := base.onNormalKey("v")
	sel := mm.(Model)
	for range 2 {
		mm, _ = sel.onVisualKey("j")
		sel = mm.(Model)
	}
	require.Equal(t, 2, sel.msgIdx)

	yank := func(t *testing.T, second string) (Model, tea.Cmd) {
		t.Helper()
		mm, _ := sel.onVisualKey("y")
		out, cmd := mm.(Model).onVisualKey(second)
		require.Equal(t, modeNormal, out.(Model).mode, "a copy leaves VISUAL")
		return out.(Model), cmd
	}

	t.Run("ids go one per line", func(t *testing.T) {
		out, cmd := yank(t, "y")
		require.Equal(t, "om_a\nom_b\nom_c", clipboard(t, cmd))
		require.Equal(t, "copied 3 ids", out.notice)
	})

	t.Run("bodies carry their senders", func(t *testing.T) {
		out, cmd := yank(t, "c")
		require.Equal(t, "李四: 发布单 #4412 合了吗\n\n张三: panic: runtime error\n\n林岚: 我看下",
			clipboard(t, cmd))
		require.Contains(t, out.notice, "copied 3 msgs ·")
	})

	t.Run("payloads become one array", func(t *testing.T) {
		out, cmd := yank(t, "r")
		var got []map[string]any
		require.NoError(t, json.Unmarshal([]byte(clipboard(t, cmd)), &got), "the range must parse as one array")
		require.Len(t, got, 3)
		require.Equal(t, "om_b", got[1]["message_id"])
		require.Contains(t, out.notice, "copied 3 json ·")
	})
}

func TestYank_SystemMessageBodyCarriesNoSender(t *testing.T) {
	text, _ := yank(yankContent, []yankSource{
		{content: "韩立 invited 柳依依 to the group.", rendered: true},
		{content: "收到", rendered: true, sender: "周舟"},
	})
	require.Equal(t, "韩立 invited 柳依依 to the group.\n\n周舟: 收到", text)
}

func TestYank_SearchHitsCopyButAnAgentContextStillNeedsTheChat(t *testing.T) {
	m := yankMsgs(sized(120, 36))
	m.focus, m.searching = paneMessages, true
	m.searchResults = []store.Message{{MessageID: "om_hit", ChatID: "oc_9", SenderName: "王五",
		Content: "搜到的这条", RenderedAt: 1, RawJSON: `{"message_id":"om_hit"}`}}
	m.msgIdx = 0

	_, cmd := pressYank(m, "y")
	require.Equal(t, "om_hit", clipboard(t, cmd), "the hit's own row carries every field the family copies")
	_, cmd = pressYank(m, "c")
	require.Equal(t, "搜到的这条", clipboard(t, cmd))
	_, cmd = pressYank(m, "r")
	require.Contains(t, clipboard(t, cmd), "om_hit")

	out, cmd := m.onNormalKey("Y")
	require.Nil(t, cmd, "an agent context needs one chat's profile, which a cross-chat hit has not got")
	require.True(t, out.(Model).noticeErr, "%q", out.(Model).notice)

	out, cmd = m.onNormalKey("v")
	require.Nil(t, cmd)
	require.True(t, out.(Model).noticeErr, "%q", out.(Model).notice)
}

func TestYank_ChatsPaneCopiesTheChatUnderTheCursor(t *testing.T) {
	m := sized(120, 36)
	m.focus, m.chatIdx = paneChats, 0
	m.chats = slices.Clone(m.chats)
	m.chats[0] = store.Chat{ChatID: "oc_9f3a", Name: "平台组", ChatMode: "group",
		RawJSON:     `{"chat_id":"oc_9f3a","name":"平台组"}`,
		LastContent: "发布单 #4412 合了吗", LastRenderedAt: 1}

	_, cmd := pressYank(m, "y")
	require.Equal(t, "oc_9f3a", clipboard(t, cmd))

	_, cmd = pressYank(m, "c")
	require.Equal(t, "发布单 #4412 合了吗", clipboard(t, cmd),
		"a chat has no body of its own, so yc takes the message its preview line shows")

	_, cmd = pressYank(m, "r")
	require.Contains(t, clipboard(t, cmd), `"chat_id": "oc_9f3a"`)
}

func TestYankContent_DegradesWithTheMessage(t *testing.T) {
	base := yankMsgs(sized(120, 36))
	base.focus = paneMessages

	t.Run("unrendered falls back to the stored body", func(t *testing.T) {
		m := base
		m.msgs = slices.Clone(m.msgs)
		m.msgs[0].Content, m.msgs[0].RenderedAt = "", 0
		out, cmd := pressYank(m, "c")
		require.Equal(t, `{"text":"发布单 #4412 合了吗"}`, clipboard(t, cmd))
		require.Contains(t, out.notice, "unrendered", "the status bar must say this is not what a person wrote")
	})

	t.Run("a recall has no body left", func(t *testing.T) {
		m := base
		m.msgs = slices.Clone(m.msgs)
		m.msgs[0].Deleted = true
		out, cmd := pressYank(m, "c")
		require.Nil(t, cmd, "the clipboard keeps whatever was on it")
		require.Equal(t, "nothing to copy", out.notice)
	})

	t.Run("a range skips the recalled", func(t *testing.T) {
		m := base
		m.msgs = slices.Clone(m.msgs)
		m.msgs[1].Deleted = true
		mm, _ := m.onNormalKey("v")
		sel := mm.(Model)
		for range 2 {
			mm, _ = sel.onVisualKey("j")
			sel = mm.(Model)
		}
		mm, _ = sel.onVisualKey("y")
		out, cmd := mm.(Model).onVisualKey("c")
		require.Equal(t, "李四: 发布单 #4412 合了吗\n\n林岚: 我看下", clipboard(t, cmd))
		require.Contains(t, out.(Model).notice, "copied 2 msgs ·",
			"the count reports what landed, not what was picked")
	})
}

func TestOnYankKey_RefusesWhereThereIsNoObjectUnderTheCursor(t *testing.T) {
	for _, tc := range []struct {
		name  string
		model func() Model
	}{
		{"assistant pane", func() Model {
			m := sized(120, 36)
			m.focus, m.aiOpen = paneThread, true
			return m
		}},
		{"composer", func() Model { m := sized(120, 36); m.focus = paneInput; return m }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			out, cmd := pressYank(tc.model(), "y")
			require.Nil(t, cmd)
			require.True(t, out.noticeErr, "%q", out.notice)
		})
	}
}

func TestRenderRaw_AMalformedPayloadDoesNotTakeTheArrayDown(t *testing.T) {
	text, notice := renderRaw([]yankSource{{raw: `{"a":1}`}, {raw: "not json"}})
	require.Contains(t, text, `"a": 1`)
	require.Contains(t, text, "not json", "one payload the API sent badly must not cost the others")
	require.Contains(t, notice, "copied 2 json ·")

	require.Equal(t, "not json", indentJSON("not json", ""))
	require.Empty(t, indentJSON("", ""), "a message stored without a payload has nothing to pretty-print")
}

func TestYankContent_CopiesACardAsTheDocumentItIs(t *testing.T) {
	card := weeklyCard
	card.Content = "<card title=\"旧渲染\">\n待认领账号：13 个\n</card>"
	text, notice := yank(yankContent, []yankSource{messageYank(card)})
	require.Equal(t, "设备版本周报 最新 1211 「兜底」\n\n待认领账号：13 个", text)
	require.Contains(t, notice, "copied content")
}

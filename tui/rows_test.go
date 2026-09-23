package tui

import (
	"strings"
	"testing"
	"time"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// msgAt is a September 2026 timestamp, read against testNow (the 23rd).
func msgAt(day, hour, minute int) int64 {
	return time.Date(2026, 9, day, hour, minute, 0, 0, time.Local).UnixMilli()
}

func rowText(rows []msgRow) string {
	var b strings.Builder
	for _, r := range rows {
		b.WriteString(ansi.Strip(r.text))
		b.WriteString("\n")
	}
	return b.String()
}

func baseStyle() msgStyle {
	return msgStyle{width: 60, self: "ou_me", now: testNow}
}

func TestRenderRows_SplitsDaysAndDropsMessageIDs(t *testing.T) {
	msgs := []store.Message{
		{MessageID: "om_1", SenderName: "王将", Content: "前天", CreateMs: msgAt(21, 9, 0), RenderedAt: 1},
		{MessageID: "om_2", SenderName: "王将", Content: "昨天上午", CreateMs: msgAt(22, 9, 0), RenderedAt: 1},
		{MessageID: "om_3", SenderName: "王将", Content: "昨天下午", CreateMs: msgAt(22, 18, 0), RenderedAt: 1},
	}
	out := rowText(renderRows(msgs, baseStyle()))
	require.Equal(t, 2, strings.Count(out, "─ "), "one separator per day, not per message: %q", out)
	require.Contains(t, out, "周一")
	require.Contains(t, out, "昨天")
	require.NotContains(t, out, "om_", "message ids are not part of the list any more")
}

func TestRenderRows_SpellsOutOnlyTheSelectedTime(t *testing.T) {
	msgs := []store.Message{
		{MessageID: "om_1", SenderName: "王将", Content: "a", CreateMs: msgAt(23, 9, 5), RenderedAt: 1},
		{MessageID: "om_2", SenderName: "王将", Content: "b", CreateMs: msgAt(22, 16, 46), RenderedAt: 1},
	}
	st := baseStyle()
	require.NotContains(t, rowText(renderRows(msgs, st)), "09:05", "no time until a message is selected")

	st.selected = "om_1"
	require.Contains(t, rowText(renderRows(msgs, st)), "09:05", "today keeps the clock alone")
	st.selected = "om_2"
	require.Contains(t, rowText(renderRows(msgs, st)), "昨天 16:46", "an older day is named before the clock")
}

func TestRenderRows_SystemMessageHasNoSenderAndSitsCentred(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", MsgType: "system", SenderName: "王将",
		Content: "邹洋 invited Factory to the group.", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	rows := renderRows(msgs, baseStyle())
	out := rowText(rows)
	require.NotContains(t, out, "王将", "a system message is not attributed to a sender")
	require.Contains(t, out, "invited Factory")
	for _, r := range rows {
		if r.plain {
			continue
		}
		require.True(t, strings.HasPrefix(r.text, "  "), "system lines are indented towards the centre: %q", r.text)
	}
}

func TestRenderRows_SenderCarriesTheAccountSuffix(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", SenderID: "ou_x", SenderName: "陈建伟", Content: "a", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	st := baseStyle()
	st.suffix = map[string]string{"ou_x": "01"}
	require.Contains(t, rowText(renderRows(msgs, st)), "陈建伟(01)")
}

func TestRenderRows_UnrenderedAndRecalledStaySpelledOut(t *testing.T) {
	msgs := []store.Message{
		{MessageID: "om_1", SenderName: "王将", ContentRaw: `{"text":"x"}`, CreateMs: msgAt(23, 9, 0)},
		{MessageID: "om_2", SenderName: "王将", Content: "gone", Deleted: true, CreateMs: msgAt(23, 9, 1), RenderedAt: 1},
	}
	out := rowText(renderRows(msgs, baseStyle()))
	require.Contains(t, out, "(rendering…)")
	require.Contains(t, out, "(recalled) gone")
}

func TestBodyRows_ImageWithoutGraphicsFallsBackToAStandIn(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", MsgType: "image", SenderName: "王将",
		Content: "[Image: img_v3_abc]", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	require.Contains(t, rowText(renderRows(msgs, baseStyle())), "[图片]")
}

func TestBodyRows_ImageReservesTheCellsItWillFill(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", MsgType: "post", SenderName: "王将",
		Content: "看这个\n![Image](img_v3_abc)", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	st := baseStyle()
	st.res = map[string][]store.Resource{"om_1": {{FileKey: "img_v3_abc", LocalPath: "a.png", Status: "done"}}}
	st.place = func(path string, maxCols int) picture {
		require.Equal(t, "a.png", path)
		return picture{path: path, cols: 8, rows: 4}
	}
	rows := renderRows(msgs, st)
	var pics []msgRow
	for _, r := range rows {
		if r.pic.cols > 0 {
			pics = append(pics, r)
		}
	}
	require.Len(t, pics, 4, "one row per cell row of the picture")
	require.Equal(t, []int{0, 1, 2, 3}, []int{pics[0].picRow, pics[1].picRow, pics[2].picRow, pics[3].picRow})
	require.Contains(t, rowText(rows), "看这个", "the text around the picture stays")
}

func TestSplitImages_KeepsTheTextAroundTheReference(t *testing.T) {
	keys, rest := splitImages("before ![Image](img_a) after")
	require.Equal(t, []string{"img_a"}, keys)
	require.Equal(t, "before  after", rest)

	keys, rest = splitImages("[Image: img_b]")
	require.Equal(t, []string{"img_b"}, keys)
	require.Empty(t, strings.TrimSpace(rest))

	keys, _ = splitImages("no picture here")
	require.Empty(t, keys)
}

func TestMsgDay_BucketsLikeTheChatList(t *testing.T) {
	require.Equal(t, "今天", msgDay(msgAt(23, 0, 1), testNow))
	require.Equal(t, "昨天", msgDay(msgAt(22, 23, 59), testNow))
	require.Equal(t, "周五", msgDay(msgAt(18, 9, 0), testNow))
	require.Equal(t, "09-10", msgDay(msgAt(10, 9, 0), testNow))
	require.Equal(t, "2025-09-10", msgDay(time.Date(2025, 9, 10, 9, 0, 0, 0, time.Local).UnixMilli(), testNow))
	require.Equal(t, "15:04", chatTime(time.Date(2026, 9, 23, 15, 4, 0, 0, time.Local).UnixMilli(), testNow),
		"the chat list still shows today's clock time")
}

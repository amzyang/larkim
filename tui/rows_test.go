package tui

import (
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
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
	return msgStyle{width: 60, height: 20, self: "ou_me", now: testNow}
}

func TestRenderRows_SplitsDaysAndDropsMessageIDs(t *testing.T) {
	msgs := []store.Message{
		{MessageID: "om_1", SenderName: "孙琪", Content: "前天", CreateMs: msgAt(21, 9, 0), RenderedAt: 1},
		{MessageID: "om_2", SenderName: "孙琪", Content: "昨天上午", CreateMs: msgAt(22, 9, 0), RenderedAt: 1},
		{MessageID: "om_3", SenderName: "孙琪", Content: "昨天下午", CreateMs: msgAt(22, 18, 0), RenderedAt: 1},
	}
	out := rowText(renderRows(msgs, baseStyle()))
	require.Equal(t, 2, strings.Count(out, "─ "), "one separator per day, not per message: %q", out)
	require.Contains(t, out, "周一")
	require.Contains(t, out, "昨天")
	require.NotContains(t, out, "om_", "message ids are not part of the list any more")
}

func TestRenderRows_LeavesTheClockToTheStatusBar(t *testing.T) {
	msgs := []store.Message{
		{MessageID: "om_1", SenderName: "孙琪", Content: "a", CreateMs: msgAt(23, 9, 5), RenderedAt: 1},
		{MessageID: "om_2", SenderName: "孙琪", Content: "b", CreateMs: msgAt(23, 9, 30), RenderedAt: 1},
	}
	// A merged message has no sender line to spell a time out on, so no row
	// carries one and the whole list stays put as the cursor moves.
	require.NotContains(t, rowText(renderRows(msgs, baseStyle())), "09:05")
}

func TestRenderRows_SystemMessageHasNoSenderAndSitsCentred(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", MsgType: "system", SenderName: "孙琪",
		Content: "林岚 invited Factory to the group.", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	rows := renderRows(msgs, baseStyle())
	out := rowText(rows)
	require.NotContains(t, out, "孙琪", "a system message is not attributed to a sender")
	require.Contains(t, out, "invited Factory")
	for _, r := range rows {
		if r.plain {
			continue
		}
		require.True(t, strings.HasPrefix(r.text, "  "), "system lines are indented towards the centre: %q", r.text)
	}
}

func TestRenderRows_SenderCarriesTheAccountSuffix(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", SenderID: "ou_x", SenderName: "李明", Content: "a", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	st := baseStyle()
	st.suffix = map[string]string{"ou_x": "01"}
	require.Contains(t, rowText(renderRows(msgs, st)), "李明01")
}

func TestRenderRows_UnrenderedAndRecalledStaySpelledOut(t *testing.T) {
	msgs := []store.Message{
		{MessageID: "om_1", SenderName: "孙琪", MsgType: "text", ContentRaw: `{"text":"x"}`, CreateMs: msgAt(23, 9, 0)},
		{MessageID: "om_2", SenderName: "孙琪", MsgType: "interactive", ContentRaw: `{"json_card":"{}"}`, CreateMs: msgAt(23, 9, 1)},
		{MessageID: "om_3", SenderName: "孙琪", Content: "gone", Deleted: true, CreateMs: msgAt(23, 9, 2), RenderedAt: 1},
	}
	out := rowText(renderRows(msgs, baseStyle()))
	require.Contains(t, out, "x", "text carries its own body while the rendering is pending")
	require.Contains(t, out, "[卡片]", "a body only lark-cli can read is named by its type")
	require.NotContains(t, out, "json_card", "raw OpenAPI JSON never reaches the screen")
	require.Contains(t, out, "(Recalled) gone")
}

func TestRenderRows_BadgesOnlyAnObservedEdit(t *testing.T) {
	// Feishu sets updated on its own post-send patches (mention resolution,
	// link and time-phrase enrichment), which the client never badges.
	msgs := []store.Message{
		{MessageID: "om_1", SenderName: "孙琪", Content: "@李四 hi", CreateMs: msgAt(23, 9, 0), RenderedAt: 1, Updated: true},
		{MessageID: "om_2", SenderName: "孙琪", Content: "4", CreateMs: msgAt(23, 9, 1), RenderedAt: 1, Updated: true, EditedAt: 7},
	}
	out := rowText(renderRows(msgs, baseStyle()))
	require.Equal(t, 1, strings.Count(out, "(Edited)"), "only the message whose body we watched change: %q", out)
}

func TestBodyRows_ImageWithoutGraphicsFallsBackToAStandIn(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", MsgType: "image", SenderName: "孙琪",
		Content: "[Image: img_v3_abc]", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	require.Contains(t, rowText(renderRows(msgs, baseStyle())), "[图片]")
}

func TestBodyRows_ImageReservesTheCellsItWillFill(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", MsgType: "post", SenderName: "孙琪",
		Content: "看这个\n![Image](img_v3_abc)", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	st := baseStyle()
	st.res = map[string][]store.Resource{"om_1": {{FileKey: "img_v3_abc", LocalPath: "a.png", Status: "done"}}}
	st.place = func(path string, maxCols, maxRows int) picture {
		require.Equal(t, "a.png", path)
		require.Equal(t, st.height, maxRows, "the pane's height bounds the picture too")
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

func TestRenderRows_QuotesTheMessageAReplyAnswers(t *testing.T) {
	parent := store.Message{MessageID: "om_1", SenderID: "ou_her", SenderName: "孙琪",
		Content: "失败任务链接发一下，我看看", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}
	msgs := []store.Message{
		parent,
		{MessageID: "om_2", SenderName: "沈知远", Content: "别的事", CreateMs: msgAt(23, 9, 5), RenderedAt: 1},
		{MessageID: "om_3", SenderName: "沈知远", Content: "任务id HTK_36", ReplyTo: "om_1",
			CreateMs: msgAt(23, 9, 9), RenderedAt: 1},
	}
	st := baseStyle()
	st.parents = map[string]store.Message{"om_1": parent}
	st.suffix = map[string]string{"ou_her": "01"}
	out := rowText(renderRows(msgs, st))
	require.Contains(t, out, "▏孙琪01: 失败任务链接发一下，我看看", "the reply quotes who it answers and what they said")
	require.NotContains(t, out, "om_1", "the quote names a message the way a person does")
	require.Less(t, strings.Index(out, "▏孙琪01"), strings.Index(out, "任务id HTK_36"), "the quote sits above the body")
}

func TestRenderRows_SkipsTheQuoteForTheMessageJustAbove(t *testing.T) {
	parent := store.Message{MessageID: "om_1", SenderName: "孙琪", Content: "问题", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}
	msgs := []store.Message{
		parent,
		{MessageID: "om_2", SenderName: "沈知远", Content: "答案", ReplyTo: "om_1", CreateMs: msgAt(23, 9, 1), RenderedAt: 1},
	}
	st := baseStyle()
	st.parents = map[string]store.Message{"om_1": parent}
	require.NotContains(t, rowText(renderRows(msgs, st)), "▏", "quoting the line right above adds nothing")
}

func TestRenderRows_QuoteSaysWhenTheParentIsMissing(t *testing.T) {
	msgs := []store.Message{
		{MessageID: "om_a", SenderName: "孙琪", Content: "无关", CreateMs: msgAt(23, 9, 0), RenderedAt: 1},
		{MessageID: "om_b", SenderName: "沈知远", Content: "答案", ReplyTo: "om_old", CreateMs: msgAt(23, 9, 1), RenderedAt: 1},
	}
	require.Contains(t, rowText(renderRows(msgs, baseStyle())), "▏↩ (not synced)",
		"a reply whose target never synced still reads as a reply")
}

func TestRenderRows_QuoteIsCutToTheWidth(t *testing.T) {
	parent := store.Message{MessageID: "om_1", SenderName: "孙琪", Content: strings.Repeat("很长的原文 ", 20),
		CreateMs: msgAt(23, 9, 0), RenderedAt: 1}
	msgs := []store.Message{
		parent,
		{MessageID: "om_x", SenderName: "李四", Content: "无关", CreateMs: msgAt(23, 9, 1), RenderedAt: 1},
		{MessageID: "om_2", SenderName: "沈知远", Content: "答案", ReplyTo: "om_1", CreateMs: msgAt(23, 9, 2), RenderedAt: 1},
	}
	st := baseStyle()
	st.parents = map[string]store.Message{"om_1": parent}
	for _, r := range renderRows(msgs, st) {
		require.LessOrEqual(t, lipgloss.Width(ansi.Strip(r.text)), st.width, "the quote stays on one line inside the pane")
	}
}

// noticeCard is a bot card whose whole body is one picture, the shape a
// scheduled notice takes.
const noticeCard = `<card title="9月费用计提通知">
🖼️ image(img_key:img_v3_0215q_75a20b7d)


</card>`

func cardMessage() []store.Message {
	return []store.Message{{MessageID: "om_1", MsgType: "interactive", SenderName: "财务AI助手",
		Content: noticeCard, CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
}

func TestBodyRows_CardPictureDrawsInsideTheFrame(t *testing.T) {
	st := baseStyle()
	st.res = map[string][]store.Resource{"om_1": {{FileKey: "img_v3_0215q_75a20b7d", LocalPath: "a.png", Status: "done"}}}
	st.place = func(path string, maxCols, maxRows int) picture {
		require.Equal(t, "a.png", path)
		require.Less(t, maxCols, st.width, "the frame and the gutter take their columns first")
		return picture{path: path, cols: 8, rows: 3}
	}
	rows := renderRows(cardMessage(), st)
	var pics []msgRow
	for _, r := range rows {
		if r.pic.cols > 0 {
			pics = append(pics, r)
		}
	}
	require.Len(t, pics, 3, "one row per cell row of the picture")
	require.Contains(t, ansi.Strip(pics[0].prefix), cardRule, "a picture inside a card keeps the card's edge")
	out := rowText(rows)
	require.Contains(t, out, "9月费用计提通知")
	require.NotContains(t, out, "img_key:", "the picture is drawn, not spelled out")
}

func TestBodyRows_CardPictureStandsInInsideTheFrame(t *testing.T) {
	out := rowText(renderRows(cardMessage(), baseStyle()))
	require.Contains(t, out, cardRule+" [图片]", "the stand-in is still part of the card")
	require.NotContains(t, out, "img_key:")
}

func TestHeadLine_BadgesAnAppsTurn(t *testing.T) {
	st := msgStyle{width: 60, self: "ou_me", now: testNow}

	bot := store.Message{MessageID: "om_1", SenderID: "ou_bot", SenderName: "Factory", SenderType: "app"}
	require.Contains(t, ansi.Strip(headLine(bot, st)), "Factory"+botBadge)

	human := store.Message{MessageID: "om_2", SenderID: "ou_them", SenderName: "周舟", SenderType: "user"}
	require.NotContains(t, ansi.Strip(headLine(human, st)), botBadge)
}

func TestHeadLine_MarksAMessageOnItsWayAndOneThatFailed(t *testing.T) {
	pending := store.Message{MessageID: "local-1", SenderID: "ou_me", SenderName: "林岚"}
	st := msgStyle{width: 60, self: "ou_me", now: testNow,
		outbox: map[string]outboxState{"local-1": outSending}}

	require.Contains(t, headLine(pending, st), "(sending)")

	st.outbox["local-1"] = outFailed
	require.Contains(t, headLine(pending, st), "(failed)")
	require.NotContains(t, headLine(store.Message{MessageID: "om_1", SenderID: "ou_me"}, st), "(sending)",
		"a message the store returned carries no send state")
}

// gutterOf is the two columns a rendered row opens with, styles stripped.
func gutterOf(r msgRow) string {
	s := r.text
	if r.pic.cols > 0 {
		s = r.prefix
	}
	g := []rune(ansi.Strip(s))
	if len(g) < gutterWidth {
		return ""
	}
	return string(g[:gutterWidth])
}

// blocks counts the sender lines a rendered list opens its blocks with. In
// these fixtures the name appears nowhere but there.
func blocks(rows []msgRow, name string) int {
	n := 0
	for _, r := range rows {
		if strings.Contains(ansi.Strip(r.text), name) {
			n++
		}
	}
	return n
}

func said(id, sender, body string, day, hour, minute int) store.Message {
	return store.Message{MessageID: id, SenderID: "ou_" + sender, SenderName: sender,
		ChatID: "oc_1", Content: body, CreateMs: msgAt(day, hour, minute), RenderedAt: 1}
}

func TestRenderRows_MergesAConsecutiveSenderWithinTheDay(t *testing.T) {
	msgs := []store.Message{
		said("om_1", "孙琪", "早", 23, 9, 0),
		said("om_2", "孙琪", "方案我看了", 23, 9, 1),
		said("om_3", "李四", "我也看了", 23, 9, 2),
	}
	rows := renderRows(msgs, baseStyle())
	out := rowText(rows)
	require.Equal(t, 1, strings.Count(out, "孙琪"), "the second message hides under the name above it: %q", out)
	require.Contains(t, out, "李四", "a new sender heads a block of its own")
	require.Equal(t, 1, blocks(rows, "李四"), "one sender line per block")
}

func TestRenderRows_SplitsABlockAfterAQuietGap(t *testing.T) {
	msgs := []store.Message{
		said("om_1", "孙琪", "早", 23, 9, 0),
		said("om_2", "孙琪", "在吗", 23, 9, 4),
		said("om_3", "孙琪", "还在吗", 23, 9, 6),
	}
	rows := renderRows(msgs, baseStyle())
	require.Equal(t, 2, blocks(rows, "孙琪"), "a block spans one burst, measured from the message that opened it")
}

func TestRenderRows_SplitsABlockAtASystemMessage(t *testing.T) {
	sys := said("om_2", "孙琪", "林岚 invited Factory to the group.", 23, 9, 1)
	sys.MsgType = "system"
	msgs := []store.Message{said("om_1", "孙琪", "早", 23, 9, 0), sys, said("om_3", "孙琪", "回来了", 23, 9, 2)}
	rows := renderRows(msgs, baseStyle())
	require.Equal(t, 2, blocks(rows, "孙琪"), "the system line between them ends the block")
}

func TestRenderRows_SplitsABlockWhenTheReadStateDiffers(t *testing.T) {
	msgs := []store.Message{said("om_1", "孙琪", "早", 23, 9, 0), said("om_2", "孙琪", "在吗", 23, 9, 1)}
	st := baseStyle()
	st.dots = map[string]bool{"om_2": true}
	rows := renderRows(msgs, st)
	require.Equal(t, 2, blocks(rows, "孙琪"), "the unread dot heads a block, so a block is all read or all unread")
	require.Equal(t, "● ", gutterOf(rows[3]), "the dot opens the unread block")
}

func TestRenderRows_SplitsABlockForAMessageCarryingItsOwnBadge(t *testing.T) {
	for _, tc := range []struct {
		name  string
		badge func(*store.Message)
		want  string
	}{
		{"thread", func(x *store.Message) { x.ThreadID, x.MessagePosition = "omt_1", 3 }, "⤷thread"},
		{"edited", func(x *store.Message) { x.EditedAt = 7 }, "(Edited)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			msgs := []store.Message{said("om_1", "孙琪", "早", 23, 9, 0), said("om_2", "孙琪", "在吗", 23, 9, 1)}
			tc.badge(&msgs[1])
			rows := renderRows(msgs, baseStyle())
			require.Equal(t, 2, blocks(rows, "孙琪"), "a badge needs a sender line to sit on")
			require.Contains(t, rowText(rows), tc.want)
		})
	}
}

func TestRenderRows_RailsTheReadersOwnMessages(t *testing.T) {
	mine := said("om_1", "me", "好的", 23, 9, 0)
	mine.SenderID = "ou_me"
	msgs := []store.Message{mine, said("om_2", "孙琪", "收到", 23, 9, 1)}
	rows := renderRows(msgs, baseStyle())
	require.Equal(t, rail+" ", gutterOf(rows[1]), "own messages run down a rail of their own")
	require.Contains(t, rows[1].text, stOK.Render(rail), "a delivered send rails in the sent colour")
	require.Equal(t, "  ", gutterOf(rows[3]), "someone else's message has no rail")
}

func TestRenderRows_RailShadesASendOnItsWay(t *testing.T) {
	msgs := []store.Message{{MessageID: "local-1", SenderID: "ou_me", SenderName: "林岚",
		ChatID: "oc_1", Content: "在路上", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	st := baseStyle()

	st.outbox = map[string]outboxState{"local-1": outSending}
	rows := renderRows(msgs, st)
	require.Contains(t, rows[1].text, stDim.Render(rail), "a send still on its way shades its rail")
	require.Equal(t, 1, blocks(rows, "你"), "a send in flight still merges; the rail carries its state")

	st.outbox["local-1"] = outFailed
	rows = renderRows(msgs, st)
	require.Contains(t, rows[1].text, stErr.Render(rail))
	require.Contains(t, rowText(rows), "(failed)", "a failed send is loud enough to head its own block")
}

func TestRenderRows_SplitsABlockForAFailedSend(t *testing.T) {
	msgs := []store.Message{
		{MessageID: "local-1", SenderID: "ou_me", SenderName: "林岚", ChatID: "oc_1",
			Content: "第一条", CreateMs: msgAt(23, 9, 0), RenderedAt: 1},
		{MessageID: "local-2", SenderID: "ou_me", SenderName: "林岚", ChatID: "oc_1",
			Content: "没发出去", CreateMs: msgAt(23, 9, 1), RenderedAt: 1},
	}
	st := baseStyle()
	st.outbox = map[string]outboxState{"local-2": outFailed}
	require.Equal(t, 2, blocks(renderRows(msgs, st), "你"))
}

func TestRenderRows_MarksTheReplyTargetInTheGutter(t *testing.T) {
	msgs := []store.Message{said("om_1", "孙琪", "早", 23, 9, 0), said("om_2", "孙琪", "在吗", 23, 9, 1)}
	st := baseStyle()
	st.quoted = "om_2"
	rows := renderRows(msgs, st)
	require.Equal(t, 1, blocks(rows, "孙琪"), "aiming a draft at a message does not move the rows around it")
	require.Equal(t, "↩ ", gutterOf(rows[3]), "the gutter says which message the open draft answers")
	require.Equal(t, "  ", gutterOf(rows[2]), "and only that one")
}

func TestRenderRows_NamesTheReaderAsYou(t *testing.T) {
	mine := said("om_1", "me", "好的", 23, 9, 0)
	mine.SenderID, mine.SenderName = "ou_me", "林岚"
	out := rowText(renderRows([]store.Message{mine}, baseStyle()))
	require.Contains(t, out, "你", "the reader reads as 你, the way the chat list names them")
	require.NotContains(t, out, "林岚")
}

func TestRenderRows_QuoteNamesTheReaderAsYou(t *testing.T) {
	parent := said("om_1", "me", "原文", 23, 9, 0)
	parent.SenderID = "ou_me"
	msgs := []store.Message{parent, said("om_2", "孙琪", "无关", 23, 9, 1),
		func() store.Message { x := said("om_3", "孙琪", "答", 23, 9, 2); x.ReplyTo = "om_1"; return x }()}
	st := baseStyle()
	st.parents = map[string]store.Message{"om_1": parent}
	require.Contains(t, rowText(renderRows(msgs, st)), "▏你: 原文")
}

func TestRenderSearchRows_KeepsABlockInsideOneChat(t *testing.T) {
	a := said("om_1", "孙琪", "命中一", 23, 9, 0)
	b := said("om_2", "孙琪", "命中二", 23, 9, 1)
	b.ChatID = "oc_2"
	chats := []store.Chat{{ChatID: "oc_1", Name: "甲群"}, {ChatID: "oc_2", Name: "乙群"}}
	rows := renderSearchRows([]store.Message{a, b}, chats, baseStyle())
	require.Equal(t, 2, blocks(rows, "孙琪"), "a hit in another chat needs its own head to carry that chat's name")
	out := rowText(rows)
	require.Contains(t, out, "甲群")
	require.Contains(t, out, "乙群")
}

func TestRenderSearchRows_SplitsABlockWhenHitsAreHoursApart(t *testing.T) {
	// The search pane lists hits newest first, so the gap from a block's head
	// to the next hit runs backwards.
	newer := said("om_2", "孙琪", "命中二", 23, 15, 0)
	older := said("om_1", "孙琪", "命中一", 23, 9, 0)
	chats := []store.Chat{{ChatID: "oc_1", Name: "甲群"}}
	rows := renderSearchRows([]store.Message{newer, older}, chats, baseStyle())
	require.Equal(t, 2, blocks(rows, "孙琪"), "six hours apart is not one burst, whichever way the list runs")
}

func TestRenderRows_EveryMessageOwnsARow(t *testing.T) {
	// j and k step through messages, not blocks, so a merged message still
	// needs a row of its own to select and scroll to.
	msgs := []store.Message{
		said("om_1", "孙琪", "早", 23, 9, 0),
		said("om_2", "孙琪", "", 23, 9, 1),
		said("om_3", "孙琪", "看这个", 23, 9, 2),
	}
	msgs[2].MsgType, msgs[2].Content = "image", "[Image: img_v3_abc]"
	rows := renderRows(msgs, baseStyle())
	require.Equal(t, 1, blocks(rows, "孙琪"), "the three of them merge into one block")
	for i := range msgs {
		require.Equal(t, i, rows[firstRow(rows, i)].idx, "message %d has a row to land on", i)
		require.GreaterOrEqual(t, lastRow(rows, i), firstRow(rows, i))
	}
}

func TestRenderRows_DotsTheBlockRatherThanEveryUnreadMessage(t *testing.T) {
	msgs := []store.Message{said("om_1", "孙琪", "会推迟了", 23, 9, 0), said("om_2", "孙琪", "改到三点", 23, 9, 1)}
	st := baseStyle()
	st.dots = map[string]bool{"om_1": true, "om_2": true}
	rows := renderRows(msgs, st)
	require.Equal(t, 1, blocks(rows, "孙琪"), "one unread block")
	require.Equal(t, 1, strings.Count(rowText(rows), "●"), "a block is all unread, so one dot says it")
}

func TestRenderRows_BadgesAMentionOfTheReader(t *testing.T) {
	msgs := []store.Message{
		{MessageID: "om_1", SenderID: "ou_x", SenderName: "孙琪", Content: "@林岚 reachable",
			MentionsJSON: `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`, CreateMs: msgAt(23, 9, 0), RenderedAt: 1},
		{MessageID: "om_2", SenderID: "ou_x", SenderName: "孙琪", Content: "@秦风 unreachable",
			MentionsJSON: `[{"id":"ou_ma","key":"@_user_1","name":"秦风"}]`, CreateMs: msgAt(23, 9, 1), RenderedAt: 1},
		{MessageID: "om_3", SenderID: "ou_x", SenderName: "孙琪", Content: "@_all all",
			CreateMs: msgAt(23, 9, 2), RenderedAt: 1},
	}
	var body strings.Builder
	for _, r := range renderRows(msgs, baseStyle()) {
		body.WriteString(r.text + "\n")
	}
	out := body.String()
	require.Contains(t, out, stMentionMe.Render("@林岚"))
	require.Contains(t, out, "@秦风 unreachable", "another person's mention is body text")
	require.Contains(t, out, stAccent.Render(allName)+" all")
	require.NotContains(t, out, allKey)
}

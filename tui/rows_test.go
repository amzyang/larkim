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
		// The lead's picture is left out: what a test reads is the marker
		// column, and a disc has no text to strip.
		b.WriteString(ansi.Strip(r.lead.mark))
		b.WriteString(ansi.Strip(r.text))
		for _, s := range r.segs {
			// A picture stands in as the cells it will fill, which is what the
			// pane shows before the terminal has it.
			if s.pic.cols > 0 {
				b.WriteString(strings.Repeat("·", s.pic.cols))
				continue
			}
			b.WriteString(ansi.Strip(s.text))
		}
		b.WriteString("\n")
	}
	return b.String()
}

// segText is a row's drawn text with its styling intact, which is what a test
// asserting on colour or underline compares against.
func segText(r msgRow) string {
	var b strings.Builder
	b.WriteString(r.text)
	for _, s := range r.segs {
		b.WriteString(s.text)
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

func TestRenderRows_ADayRuleNeedsNoAirAroundIt(t *testing.T) {
	msgs := []store.Message{
		{MessageID: "om_1", SenderName: "张三", Content: "前天", CreateMs: msgAt(21, 9, 0), RenderedAt: 1},
		{MessageID: "om_2", SenderName: "张三", Content: "昨天", CreateMs: msgAt(22, 9, 0), RenderedAt: 1},
	}
	rows := renderRows(msgs, baseStyle())
	seen := 0
	for i, r := range rows {
		if i == 0 || !strings.Contains(ansi.Strip(r.text), "─ ") {
			continue
		}
		seen++
		require.NotEmpty(t, strings.TrimSpace(rowText(rows[i-1:i])),
			"the rule is a divider of its own, so nothing is held off above it:\n%s", rowText(rows))
		require.NotEmpty(t, strings.TrimSpace(rowText(rows[i+1:i+2])),
			"nor below it:\n%s", rowText(rows))
	}
	require.Equal(t, 1, seen, "a rule between the two days:\n%s", rowText(rows))
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

// cardMessage is a bot card whose whole body is one picture, the shape a
// scheduled notice takes.
func cardMessage() []store.Message {
	m := cardHeaded("每日构建报告", "", "夜间",
		`{"tag":"img","property":{"imageID":"2","alt":{"tag":"plain_text","property":{"content":"image"}}}}`,
		cardPictures(map[string]string{"2": "img_v3_notice"}))
	m.SenderName = "构建机器人"
	return []store.Message{m}
}

func TestBodyRows_CardPictureDrawsInsideTheFrame(t *testing.T) {
	st := baseStyle()
	st.res = map[string][]store.Resource{"om_1": {{FileKey: "img_v3_notice", LocalPath: "a.png", Status: "done"}}}
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
	require.Equal(t, leadWidth, pics[0].lead.cols(), "a picture inside a card keeps the lead's columns")
	out := rowText(rows)
	require.Contains(t, out, "每日构建报告")
	require.NotContains(t, out, "img_key:", "the picture is drawn, not spelled out")
}

func TestBodyRows_CardPictureStandsInWhenItCannotBeDrawn(t *testing.T) {
	out := rowText(renderRows(cardMessage(), baseStyle()))
	require.Contains(t, out, "[图片]")
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

// markOf is the marker column a rendered row opens with, styles stripped.
func markOf(r msgRow) string { return ansi.Strip(r.lead.mark) }

// drawn is the rows a message put on screen, with the day rules and the blank
// lines between sections dropped, so an index into it counts content.
func drawn(rows []msgRow) []msgRow {
	out := make([]msgRow, 0, len(rows))
	for _, r := range rows {
		if !r.plain {
			out = append(out, r)
		}
	}
	return out
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

func TestRenderRows_HoldsEachSectionOffTheOneAbove(t *testing.T) {
	sys := said("om_2", "孙琪", "林岚 invited 张三 to the group.", 23, 9, 1)
	sys.MsgType = "system"
	msgs := []store.Message{
		said("om_1", "孙琪", "早", 23, 9, 0),
		sys,
		said("om_3", "李四", "收到", 23, 9, 2),
		said("om_4", "李四", "在看", 23, 9, 3),
	}
	rows := renderRows(msgs, baseStyle())

	var gaps []int
	for i, r := range rows {
		if r.plain && r.text == "" {
			gaps = append(gaps, i)
		}
	}
	require.Equal(t, []int{3, 5}, gaps,
		"a blank line before the system notice and the next block, none under the day rule: %q", rowText(rows))
	require.Equal(t, 1, blocks(rows, "李四"), "the second message merges, so it opens nothing")
	require.NotEmpty(t, rows[0].text, "the day rule still heads the list")
	require.NotEmpty(t, rows[len(rows)-1].text, "and the last section is not followed by a blank")
}

func TestRenderRows_PutsTheBodyRightUnderTheSenderLine(t *testing.T) {
	mine := said("om_1", "me", "好的", 23, 9, 0)
	mine.SenderID = "ou_me"
	rows := drawn(renderRows([]store.Message{mine, said("om_2", "孙琪", "收到", 23, 9, 1)}, baseStyle()))

	require.Contains(t, ansi.Strip(rows[1].text), "好的", "the body follows the sender line with nothing between")
	require.Equal(t, leadWidth, rows[1].lead.cols(), "every body line keeps the lead's columns")
	require.Equal(t, " ", markOf(rows[1]), "an ordinary row marks nothing")
}

func TestRenderRows_SplitsABlockWhenTheReadStateDiffers(t *testing.T) {
	msgs := []store.Message{said("om_1", "孙琪", "早", 23, 9, 0), said("om_2", "孙琪", "在吗", 23, 9, 1)}
	st := baseStyle()
	st.dots = map[string]bool{"om_2": true}
	rows := renderRows(msgs, st)
	require.Equal(t, 2, blocks(rows, "孙琪"), "the unread dot heads a block, so a block is all read or all unread")
	require.Equal(t, "●", markOf(drawn(rows)[2]), "the dot opens the unread block")
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

func TestRenderRows_TheReadersOwnMessagesCarryNoMarkOfTheirOwn(t *testing.T) {
	mine := said("om_1", "me", "好的", 23, 9, 0)
	mine.SenderID = "ou_me"
	msgs := []store.Message{mine, said("om_2", "孙琪", "收到", 23, 9, 1)}
	rows := drawn(renderRows(msgs, baseStyle()))

	require.Equal(t, " ", markOf(rows[0]), "the disc says whose turn it is; nothing else has to")
	require.Equal(t, " ", markOf(rows[2]))
}

func TestRenderRows_ASendOnItsWaySaysSoOnALineOfItsOwn(t *testing.T) {
	msgs := []store.Message{{MessageID: "local-1", SenderID: "ou_me", SenderName: "林岚",
		ChatID: "oc_1", Content: "在路上", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	st := baseStyle()

	st.outbox = map[string]outboxState{"local-1": outSending}
	require.Contains(t, rowText(renderRows(msgs, st)), "(sending)",
		"how far a send has got is spelled out, so it heads a block of its own")

	st.outbox["local-1"] = outFailed
	require.Contains(t, rowText(renderRows(msgs, st)), "(failed)")
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

func TestRenderRows_MarksTheReplyTargetInTheLead(t *testing.T) {
	msgs := []store.Message{said("om_1", "孙琪", "早", 23, 9, 0), said("om_2", "孙琪", "在吗", 23, 9, 1)}
	st := baseStyle()
	st.quoted = "om_2"
	rows := renderRows(msgs, st)
	require.Equal(t, 1, blocks(rows, "孙琪"), "aiming a draft at a message does not move the rows around it")
	require.Equal(t, "↩", markOf(drawn(rows)[2]), "the lead says which message the open draft answers")
	require.Equal(t, " ", markOf(drawn(rows)[1]), "and only that one")
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
	rows := renderSearchRows(messageHits(a, b), chats, baseStyle())
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
	rows := renderSearchRows(messageHits(newer, older), chats, baseStyle())
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
	require.Contains(t, out, stAccent.Render("@秦风"), "another person's mention carries the accent")
	require.Contains(t, out, stAccent.Render(allName)+" all")
	require.NotContains(t, out, allKey)
}

func TestRenderRows_DimsAMentionAChatOfTwoCannotReach(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", SenderID: "ou_peer", SenderName: "张三", Content: "@李四 看下",
		MentionsJSON: `[{"id":"ou_a","key":"@_user_1","name":"李四"}]`, CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}

	group := baseStyle()
	var out strings.Builder
	for _, r := range renderRows(msgs, group) {
		out.WriteString(segText(r))
	}
	require.Contains(t, out.String(), stAccent.Render("@李四"), "in a group the name reaches somebody")

	pair := baseStyle()
	pair.p2p, pair.peer = true, "ou_peer"
	out.Reset()
	for _, r := range renderRows(msgs, pair) {
		out.WriteString(segText(r))
	}
	require.Contains(t, out.String(), stDim.Render("@李四"),
		"neither side of this chat is 李四, so the @ reaches nobody in it")
	require.NotContains(t, out.String(), stAccent.Render("@李四"))
}

func TestBodyRows_StickerDrawsThePictureInsteadOfItsPlaceholder(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", MsgType: "sticker", SenderName: "张三",
		Content: "[Sticker]", ContentRaw: `{"file_key":"v3_shrug"}`, CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	st := baseStyle()
	st.res = map[string][]store.Resource{"om_1": {{FileKey: "v3_shrug", Type: "sticker",
		LocalPath: "resources/stickers/v3_shrug.gif", Status: "done"}}}
	cols := 0
	st.place = func(path string, maxCols, maxRows int) picture {
		require.Equal(t, "resources/stickers/v3_shrug.gif", path)
		cols = maxCols
		return picture{path: path, cols: 6, rows: 3}
	}
	rows := renderRows(msgs, st)
	require.Equal(t, stickerCols, cols, "a sticker is drawn at its own size, not the pane's")
	require.NotContains(t, rowText(rows), "[Sticker]", "the picture stands for itself")
	n := 0
	for _, r := range rows {
		if r.pic.cols > 0 {
			n++
		}
	}
	require.Equal(t, 3, n, "one row per cell row of the picture")
}

func TestBodyRows_StickerWithoutItsPictureStandsIn(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", MsgType: "sticker", SenderName: "张三",
		Content: "[Sticker]", ContentRaw: `{"file_key":"v3_shrug"}`, CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	out := rowText(renderRows(msgs, baseStyle()))
	require.Contains(t, out, "[表情]")
	require.NotContains(t, out, "[Sticker]")

	msgs[0].ContentRaw = `{}`
	require.Contains(t, rowText(renderRows(msgs, baseStyle())), "[Sticker]",
		"a sticker body naming no picture keeps whatever text it has")
}

func TestRenderRows_AChatOfTwoNamesNobody(t *testing.T) {
	msgs := []store.Message{said("om_1", "孙琪", "今天的构建挂了", 23, 9, 0)}
	st := baseStyle()
	st.p2p = true
	rows := drawn(renderRows(msgs, st))

	require.Contains(t, ansi.Strip(rows[0].text), "今天的构建挂了",
		"with no name to spell, the block opens with its own body")
	require.NotContains(t, rowText(rows), "孙琪", "the disc beside the block says who spoke")
}

func TestRenderRows_AGroupNamesTheSender(t *testing.T) {
	msgs := []store.Message{said("om_1", "孙琪", "今天的构建挂了", 23, 9, 0)}
	rows := drawn(renderRows(msgs, baseStyle()))

	require.Len(t, rows, 2)
	require.Equal(t, "孙琪", ansi.Strip(rows[0].text), "a group opens the block with the name")
	require.Contains(t, ansi.Strip(rows[1].text), "今天的构建挂了")
}

func TestRenderRows_AChatOfTwoStillDrawsALineForABadge(t *testing.T) {
	msgs := []store.Message{said("om_1", "孙琪", "改好了", 23, 9, 0)}
	msgs[0].EditedAt = 7
	st := baseStyle()
	st.p2p = true
	rows := drawn(renderRows(msgs, st))

	require.Equal(t, "(Edited)", ansi.Strip(rows[0].text), "the badge has a line of its own, the name none")
	require.Contains(t, ansi.Strip(rows[1].text), "改好了")
}

func TestRenderSearchRows_NamesTheSenderInsideAChatOfTwo(t *testing.T) {
	msgs := []store.Message{said("om_1", "孙琪", "今天的构建挂了", 23, 9, 0)}
	st := baseStyle()
	st.p2p = true // the cursor happens to sit on a p2p chat
	out := rowText(renderSearchRows(messageHits(msgs...), []store.Chat{{ChatID: "oc_1", Name: "平台组"}}, st))

	require.Contains(t, out, "孙琪", "hits run across chats, so every block names its sender")
	require.Contains(t, out, "平台组")
}

func TestRenderRows_TheBlockOpenerCarriesTheSendersDisc(t *testing.T) {
	msgs := []store.Message{
		said("om_1", "孙琪", "早", 23, 9, 0),
		said("om_2", "孙琪", "方案我看了", 23, 9, 1),
	}
	st := baseStyle()
	st.avatars = map[string]string{"ou_孙琪": "users/ou_a.png"}
	st.disc = func(file, id, name string, cols, rows int) picture {
		require.Equal(t, "users/ou_a.png", file)
		require.Equal(t, "ou_孙琪", id)
		require.Equal(t, "孙琪", name)
		require.Equal(t, avatarWidth, cols, "a disc is the size the chat list draws one")
		require.Equal(t, avatarHeight, rows)
		return picture{path: file, cols: cols, rows: rows, disc: true}
	}
	rows := drawn(renderRows(msgs, st))

	require.True(t, rows[0].lead.pic.disc, "the disc is clipped to the circle the client draws")
	require.Equal(t, 0, rows[0].lead.picRow)
	require.Equal(t, 1, rows[1].lead.picRow,
		"the disc is taller than a line, so it spans on into the message merged under it")
	for i, r := range rows[2:] {
		require.Zero(t, r.lead.pic.cols, "row %d repeats no disc; one block, one sender", i+2)
		require.Equal(t, leadWidth, r.lead.cols(), "but it keeps the columns the disc took")
	}
}

func TestRenderRows_AShortBlockStillDrawsTheWholeDisc(t *testing.T) {
	msgs := []store.Message{said("om_1", "孙琪", "早", 23, 9, 0)}
	st := baseStyle()
	st.p2p = true // one line, no head line: the block is shorter than the disc
	st.avatars = map[string]string{"ou_孙琪": "users/ou_a.png"}
	st.disc = func(file, id, name string, cols, rows int) picture {
		return picture{path: file, cols: cols, rows: rows, disc: true}
	}
	rows := drawn(renderRows(msgs, st))

	require.Len(t, rows, avatarHeight, "the block is padded to the rows the circle needs")
	require.Equal(t, 1, rows[1].lead.picRow, "or its bottom half would be cut off")
	require.Empty(t, ansi.Strip(rows[1].text), "the row carries the disc and nothing else")
	require.Equal(t, 0, rows[1].idx, "it belongs to the message, so a selection tints it along")
}

func TestRenderRows_SenderWithNoPictureFallsBackToTheColourBlock(t *testing.T) {
	msgs := []store.Message{said("om_1", "孙琪", "早", 23, 9, 0)}
	rows := drawn(renderRows(msgs, baseStyle()))

	require.Zero(t, rows[0].lead.pic.cols, "a terminal that draws nothing still opens the block")
	require.Equal(t, " 孙 ", ansi.Strip(rows[0].lead.box), "the block carries the first character of the name")
	require.Equal(t, avatarWidth, lipgloss.Width(rows[0].lead.box))
	require.Equal(t, avatarWidth, lipgloss.Width(rows[1].lead.box), "and the rest of it is blank")
}

func TestBlockHeads_SplitsOnDaySenderAndMarker(t *testing.T) {
	msgs := []store.Message{
		{MessageID: "om_1", SenderID: "ou_a", SenderName: "张三", CreateMs: msgAt(22, 9, 0)},
		{MessageID: "om_2", SenderID: "ou_a", SenderName: "张三", CreateMs: msgAt(22, 9, 1)},
		{MessageID: "om_3", SenderID: "ou_b", SenderName: "李四", CreateMs: msgAt(22, 9, 2)},
		{MessageID: "om_4", SenderID: "ou_b", SenderName: "李四", CreateMs: msgAt(22, 9, 3)},
		{MessageID: "om_5", SenderID: "ou_b", SenderName: "李四", CreateMs: msgAt(23, 9, 0)},
	}
	st := baseStyle()
	st.dots = map[string]bool{"om_4": true}

	require.Equal(t, []int{0, 0, 2, 3, 4}, blockHeads(msgs, st),
		"a new sender, an unread marker and a new day each open a block")
}

func TestBlockHeads_ASystemNoticeEndsTheBlockAboveIt(t *testing.T) {
	msgs := []store.Message{
		{MessageID: "om_1", SenderID: "ou_a", SenderName: "张三", CreateMs: msgAt(22, 9, 0)},
		{MessageID: "om_2", MsgType: "system", CreateMs: msgAt(22, 9, 1)},
		{MessageID: "om_3", SenderID: "ou_a", SenderName: "张三", CreateMs: msgAt(22, 9, 2)},
	}

	require.Equal(t, []int{0, 1, 2}, blockHeads(msgs, baseStyle()),
		"the sender coming back writes a sender line of their own")
}

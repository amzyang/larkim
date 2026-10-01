package tui

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// cardOf wraps card elements the way an interactive message carries them: the
// card as embedded JSON, beside the table its pictures and mentions live in.
func cardOf(elements string, attachment map[string]any) store.Message {
	if attachment == nil {
		attachment = map[string]any{}
	}
	body := `{"schema":"2.0","body":{"tag":"body","property":{"elements":[` +
		`{"tag":"markdown","property":{"elements":[` + elements + `]}}]}}}`
	env, err := json.Marshal(map[string]any{"json_card": body, "json_attachment": attachment, "card_schema": 2})
	if err != nil {
		panic(err)
	}
	return store.Message{MessageID: "om_1", SenderName: "张三", SenderID: "ou_a", MsgType: "interactive",
		ContentRaw: string(env), CreateMs: msgAt(23, 9, 0), RenderedAt: 1}
}

// summarisedCard is the content of a card that names itself for the chat list:
// one band over every card the bot posts, and a summary naming this one.
func summarisedCard(summary string) string {
	body := `{"schema":"2.0","config":{"summary":{"content":"` + summary + `"}},` +
		`"header":{"tag":"card_header","property":{"title":{"tag":"plain_text","property":{"content":"告警"}}}},` +
		`"body":{"tag":"body","property":{"elements":[{"tag":"markdown","property":{"elements":[` +
		`{"tag":"plain_text","property":{"content":"1 分钟内出现 2 条异常"}}]}}]}}}`
	env, err := json.Marshal(map[string]any{"json_card": body, "json_attachment": map[string]any{}, "card_schema": 2})
	if err != nil {
		panic(err)
	}
	return string(env)
}

// cardPictures is the attachment table of a card whose body names pictures.
func cardPictures(keys map[string]string) map[string]any {
	images := map[string]any{}
	for id, key := range keys {
		images[id] = map[string]any{"origin_key": key}
	}
	return map[string]any{"images": images}
}

// cardHeaded is the same, with the band a bot's report carries above it.
func cardHeaded(title, subtitle, tag, elements string, attachment map[string]any) store.Message {
	head := `{"tag":"card_header","property":{"template":"blue","title":{"tag":"plain_text","property":{"content":"` + title +
		`"}},"subtitle":{"tag":"plain_text","property":{"content":"` + subtitle +
		`"}},"textTagList":[{"tag":"text_tag","property":{"color":"purple","text":{"tag":"plain_text","property":{"content":"` +
		tag + `"}}}}]}}`
	m := cardOf(elements, attachment)
	var env map[string]any
	if json.Unmarshal([]byte(m.ContentRaw), &env) != nil {
		panic("card envelope")
	}
	env["json_card"] = strings.Replace(env["json_card"].(string), `{"schema":"2.0",`, `{"schema":"2.0","header":`+head+`,`, 1)
	raw, err := json.Marshal(env)
	if err != nil {
		panic(err)
	}
	m.ContentRaw = string(raw)
	return m
}

// weeklyCard is a report of the kind a bot posts: a header band over a body.
var weeklyCard = cardHeaded("设备版本周报", "最新 1211", "兜底",
	`{"tag":"plain_text","property":{"content":"待认领账号：13 个"}}`, nil)

func cardText(t *testing.T, elements string) string {
	t.Helper()
	return rowText(renderRows([]store.Message{cardOf(elements, nil)}, baseStyle()))
}

const (
	elHeading   = `{"tag":"heading","property":{"level":1,"elements":[{"tag":"plain_text","property":{"content":"报表"}}]}}`
	elList      = `{"tag":"list","property":{"items":[{"type":"ul","level":0,"elements":[{"tag":"plain_text","property":{"content":"甲"}}]},{"type":"ul","level":1,"elements":[{"tag":"plain_text","property":{"content":"乙"}}]}]}}`
	elQuote     = `{"tag":"blockquote","property":{"elements":[{"tag":"plain_text","property":{"content":"引文"}}]}}`
	elCodeSpan  = "{\"tag\":\"code_span\",\"property\":{\"content\":\"go vet\"}}"
	elCodeBlock = `{"tag":"code_block","property":{"language":"go","contents":[{"contents":[{"content":"x := 1","contentType":"text"}]}]}}`
	elTable     = `{"tag":"table","property":{"columns":[{"name":"0","displayName":"项"},{"name":"1","displayName":"值"}],"rows":[{"0":{"data":{"tag":"markdown","property":{"elements":[{"tag":"plain_text","property":{"content":"甲"}}]}}},"1":{"data":"1"}}]}}`
	elRule      = `{"tag":"hr","property":{}}`
	elLink      = `{"tag":"link","property":{"content":"链接","url":{"url":"https://example.com"}}}`
)

// TestCardRows_BlocksDoNotRunTogether is the shape of the card larkim reads
// the schema for: every block element in one body, each of which has to land
// on a line of its own.
func TestCardRows_BlocksDoNotRunTogether(t *testing.T) {
	out := cardText(t, strings.Join([]string{
		elHeading, elList, elQuote, elCodeSpan, elCodeBlock, elTable, elRule, elLink,
	}, ","))

	for _, want := range []string{"报表", "• 甲", "◦ 乙", "引文", "go vet", "x := 1", "链接"} {
		require.Contains(t, out, want)
	}
	require.NotContains(t, out, "# ", "a heading reads as its text, not its hashes")
	require.NotContains(t, out, "```", "a fence is markup")
	require.NotContains(t, out, "|---", "a table is drawn, not spelled")
	require.Contains(t, out, "│ 引文", "a quote keeps its gutter")
	require.Contains(t, out, "─────", "the rule is drawn")

	// Each block ends where the next begins: no line carries two of them.
	for line := range strings.SplitSeq(out, "\n") {
		for _, pair := range [][2]string{
			{"报表", "甲"}, {"甲", "引文"}, {"引文", "go vet"}, {"go vet", "x := 1"}, {"值", "链接"},
		} {
			if strings.Contains(line, pair[0]) {
				require.NotContains(t, line, pair[1], "two blocks share a line: %q", line)
			}
		}
	}
}

func TestCardRows_TextStyleIsStylingNotText(t *testing.T) {
	out := cardText(t, `{"tag":"plain_text","property":{"content":"粗","textStyle":{"attributes":["bold"]}}},`+
		`{"tag":"plain_text","property":{"content":"删","textStyle":{"attributes":["strikethrough"]}}}`)
	require.Contains(t, out, "粗删")
	require.NotContains(t, out, "**")
	require.NotContains(t, out, "~~")
}

// cardButton spells one button of an action row: the label it shows, over the
// list of what pressing it does.
func cardButton(label, actions string) string {
	return `{"tag":"button","property":{"type":"default","text":{"tag":"plain_text","property":{"content":"` + label +
		`"}},"actions":[` + actions + `]}}`
}

// cardCallback is a press that reaches only the app that sent the card.
const cardCallback = `{"type":"action_request","action":{"actionID":"act_v1_1","value":""}}`

// cardActionRow wraps buttons the way a card carries them.
func cardActionRow(buttons ...string) string {
	return `{"tag":"action","property":{"actions":[` + strings.Join(buttons, ",") + `]}}`
}

func rowZones(rows []msgRow) []clickZone {
	var out []clickZone
	for _, r := range rows {
		out = append(out, r.zones...)
	}
	return out
}

func TestCardRows_ButtonsShareTheRowTheClientDraws(t *testing.T) {
	out := cardText(t, cardActionRow(cardButton("认领", cardCallback), cardButton("忽略", cardCallback)))
	require.Regexp(t, `认领 +忽略`, out, "an action row is one row of pills")
	require.NotContains(t, out, "act_v1_1", "a button shows its label, not what it fires")
}

func TestCardRows_EachButtonOpensWhatItsPressWouldReach(t *testing.T) {
	msg := cardOf(cardActionRow(
		cardButton("详情", `{"type":"open_url","action":{"url":"https://example.com/run/1"}}`),
		cardButton("同意", cardCallback)), nil)
	msg.ChatID, msg.MessagePosition = "oc_ops", 42
	zones := rowZones(renderRows([]store.Message{msg}, baseStyle()))
	require.Len(t, zones, 2)
	require.Equal(t, []string{"https://example.com/run/1"}, zones[0].urls)
	require.Equal(t, []string{applink.ChatLink("oc_ops", "om_1", 42)}, zones[1].urls,
		"no open API submits a card callback, so the client is where that press still lands")
	require.LessOrEqual(t, zones[0].x1, zones[1].x0, "each target sits under the pill it belongs to")
}

func TestCardRows_APillMovesToTheNextLineWhole(t *testing.T) {
	buttons := make([]string, 0, 8)
	for i := range 8 {
		buttons = append(buttons, cardButton("选项"+strconv.Itoa(i),
			`{"type":"open_url","action":{"url":"https://example.com/o"}}`))
	}
	st := baseStyle()
	rows := renderRows([]store.Message{cardOf(cardActionRow(buttons...), nil)}, st)
	lines := 0
	for _, r := range rows {
		if len(r.zones) == 0 {
			continue
		}
		lines++
		for _, z := range r.zones {
			require.LessOrEqual(t, z.x1, leadWidth+st.inner(), "a pill never runs past the pane")
		}
	}
	require.Greater(t, lines, 1, "eight pills do not fit one line")
	require.Len(t, rowZones(rows), 8, "every pill keeps a target of its own")
}

func TestCardRows_PictureHoldsItsPlaceUntilItLands(t *testing.T) {
	msg := cardOf(`{"tag":"img","property":{"imageID":"19","alt":{"tag":"plain_text","property":{"content":"image"}}}}`,
		cardPictures(map[string]string{"19": "img_card"}))
	require.Contains(t, rowText(renderRows([]store.Message{msg}, baseStyle())), "[Image]")
}

func TestCardRows_MentionNamesThePersonTheCardCarries(t *testing.T) {
	// A card @s by an id of the sending app's own, so the name comes from the
	// table beside the body, and the key there is what pairs the mention with
	// the open id this reader knows the person by.
	att := map[string]any{"at_users": map[string]any{
		"ou_app": map[string]any{"content": "李四", "mention_key": "@_user_1", "user_id": "7480000000000000000"},
	}}
	msg := cardOf(`{"tag":"at","property":{"userID":"ou_app"}},{"tag":"plain_text","property":{"content":" 请看"}}`, att)
	msg.MentionsJSON = `[{"id":"ou_them","key":"@_user_1","name":"李四"}]`
	require.Contains(t, rowText(renderRows([]store.Message{msg}, baseStyle())), "@李四 请看")

	// The reader's own mention is the one the pane sets off as a badge.
	msg.MentionsJSON = `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`
	rows := renderRows([]store.Message{msg}, baseStyle())
	require.Contains(t, rowText(rows), "@林岚")
	var body string
	for _, r := range rows {
		if strings.Contains(r.text, "请看") {
			body = r.text
		}
	}
	require.Contains(t, body, stMentionMe.Render("@林岚"), "a card mention of the reader reaches them")
}

// A card that names nobody the message knows still says who it @s.
func TestCardRows_MentionWithoutAKeyKeepsTheName(t *testing.T) {
	att := map[string]any{"at_users": map[string]any{"ou_app": map[string]any{"content": "王五"}}}
	msg := cardOf(`{"tag":"at","property":{"userID":"ou_app"}}`, att)
	require.Contains(t, rowText(renderRows([]store.Message{msg}, baseStyle())), "@王五")
}

func TestCardRows_DrawsBeforeARenderingArrives(t *testing.T) {
	msg := weeklyCard
	msg.Content, msg.RenderedAt = "", 0
	out := rowText(renderRows([]store.Message{msg}, baseStyle()))
	require.Contains(t, out, "设备版本周报")
	require.Contains(t, out, "待认领账号：13 个", "the card says everything it needs to say itself")
}

func TestCardGist_SaysWhatTheCardSays(t *testing.T) {
	c, ok := card.Parse(weeklyCard.ContentRaw)
	require.True(t, ok)
	require.Equal(t, "设备版本周报 「兜底」", cardGist(c))

	// A card with no band of its own is named by what it says, with the
	// markers its structure is spelled by left behind.
	c, ok = card.Parse(cardOf(elHeading+","+elList, nil).ContentRaw)
	require.True(t, ok)
	require.Equal(t, "报表 甲 乙", flatten(cardGist(c)))
}

// A bot that opens with a greeting says nothing on its first line; the words
// after it are what the one-line views have room for.
func TestCardGist_HeadlessCardRunsItsBodyOntoOneLine(t *testing.T) {
	c, ok := card.Parse(cardOf(`{"tag":"plain_text","property":{"content":"Hey"}},`+
		`{"tag":"br","property":{}},{"tag":"br","property":{}},`+
		`{"tag":"plain_text","property":{"content":"排查有结论了："}},`+
		`{"tag":"code_span","property":{"content":"job-1"}}`, nil).ContentRaw)
	require.True(t, ok)
	require.Equal(t, "Hey 排查有结论了：job-1", flatten(cardGist(c)))
}

func TestCardGist_PictureOnlyCardIsNamedByItsPicture(t *testing.T) {
	require.Equal(t, "[Image]", cardGist(card.Card{Blocks: []card.Block{{ImageKey: "img_a"}}}),
		"a card with nothing but a picture is still a card that said something")
}

// An alarm bot puts the same band on every card it posts and says which alarm
// this one is in the summary, which is the line the client shows as well.
func TestCardGist_TakesTheSummaryOverTheBand(t *testing.T) {
	c, ok := card.Parse(summarisedCard("应用 order-api 普通预警"))
	require.True(t, ok)
	require.Equal(t, "应用 order-api 普通预警", cardGist(c))
}

func TestZoneAt_AClickPicksTheButtonItLandsOn(t *testing.T) {
	row := msgRow{zones: []clickZone{
		{x0: 2, x1: 8, urls: []string{"https://example.com/run/1"}},
		{x0: 9, x1: 15, urls: []string{"lark://applink.feishu.cn/client/chat/open?openChatId=oc_ops"}},
	}}
	rows := []msgRow{row}
	z, ok := zoneAt(rows, 0, 11)
	require.True(t, ok)
	require.Equal(t, row.zones[1].urls, z.urls, "a row of pills hands over the one under the pointer")

	_, ok = zoneAt(rows, 0, 8)
	require.False(t, ok, "the gap between two pills is no target")
}

func TestCardRows_EachPillIsALinkToWhereItLeads(t *testing.T) {
	msg := cardOf(cardActionRow(
		cardButton("详情", `{"type":"open_url","action":{"url":"https://example.com/run/1"}}`),
		cardButton("同意", cardCallback)), nil)
	msg.ChatID, msg.MessagePosition = "oc_ops", 42
	rows := renderRows([]store.Message{msg}, baseStyle())

	var pills string
	for _, r := range rows {
		if strings.Contains(ansi.Strip(r.text), "详情") {
			pills = r.text
		}
	}
	require.Contains(t, pills, ";https://example.com/run/1\a")
	require.Contains(t, pills, ";"+applink.ChatLink("oc_ops", "om_1", 42)+"\a",
		"the pill a callback sits behind leads to the client, the way its press does")
	require.Equal(t, 2, strings.Count(pills, ansi.ResetHyperlink()))
}

// bodyCard wraps top-level body components the way a bot that posts several
// of them sends its card.
func bodyCard(components ...string) store.Message {
	body := `{"schema":"2.0","body":{"tag":"body","property":{"elements":[` + strings.Join(components, ",") + `]}}}`
	env, err := json.Marshal(map[string]any{"json_card": body, "json_attachment": map[string]any{}, "card_schema": 2})
	if err != nil {
		panic(err)
	}
	return store.Message{MessageID: "om_1", SenderName: "构建机器人", SenderID: "ou_a", MsgType: "interactive",
		ContentRaw: string(env), CreateMs: msgAt(23, 9, 0), RenderedAt: 1}
}

func mdComponent(elements ...string) string {
	return `{"tag":"markdown","property":{"elements":[` + strings.Join(elements, ",") + `]}}`
}

func plainEl(s string) string {
	return `{"tag":"plain_text","property":{"content":"` + s + `"}}`
}

func codeEl(s string) string {
	return `{"tag":"code_span","property":{"content":"` + s + `"}}`
}

// bodyLines is what each of a message's body rows draws past its lead.
func bodyLines(rows []msgRow) []string {
	var out []string
	for _, r := range rows[1:] { // the first row is the sender line
		var b strings.Builder
		b.WriteString(ansi.Strip(r.text))
		for _, s := range r.segs {
			b.WriteString(ansi.Strip(s.text))
		}
		out = append(out, b.String())
	}
	return out
}

// TestCardRows_AStreamingReplyLaysOutTheWayTheClientDoes is the reply an agent
// bot streams: an answer run through with code, a line naming the session, a
// rule, and a footer.
func TestCardRows_AStreamingReplyLaysOutTheWayTheClientDoes(t *testing.T) {
	st := baseStyle()
	m := bodyCard(
		mdComponent(plainEl("收到，状态已确认。"), `{"tag":"br","property":{}}`, `{"tag":"br","property":{}}`,
			plainEl("设备原为强制登录待修复，已标记为处理中。如后续仍有个别设备未恢复，可随时让我拉 "),
			codeEl("lc-device"), plainEl(" 看当前实际在线情况，或调 "), codeEl("lc-cookie"),
			plainEl(" 查账号的 Cookie 健康度。")),
		mdComponent(plainEl("sessionId: "), codeEl("s_demo"), plainEl("　"),
			`{"tag":"link","property":{"content":"🔗 在平台中查看完整对话","url":{"url":"https://example.com/s"}}}`),
		`{"tag":"hr","property":{}}`,
	)
	rows := renderRows([]store.Message{m}, st)
	lines := bodyLines(rows)

	var session int
	for i, l := range lines {
		require.False(t, strings.HasPrefix(l, " "), "row %d opens on the space it broke at: %q", i, l)
		require.LessOrEqual(t, ansi.StringWidth(strings.TrimRight(l, " ")), st.inner(), "row %d runs past the body: %q", i, l)
		if strings.Contains(l, "sessionId") {
			session = i
		}
	}
	require.True(t, strings.HasPrefix(lines[session], "sessionId"), "the session line is a paragraph of its own: %q", lines[session])
	require.Empty(t, strings.TrimSpace(lines[session-1]), "a blank line parts it from the answer")
}

func cardColumn(width string, elements string) string {
	return `{"tag":"column","property":{"width":"` + width + `","weight":1,"elements":[` + elements + `]}}`
}

// footerCard is a reply signed off the way an agent bot signs one: the answer,
// then a status on the left of a line and the buttons that rate it on the
// right.
var footerCard = bodyCard(
	mdComponent(plainEl("收到。")),
	`{"tag":"column_set","property":{"columns":[`+
		cardColumn("weighted", mdComponent(plainEl("✅ 完成 · 18.2s")))+`,`+
		cardColumn("auto", cardButton("👍", cardCallback))+`,`+
		cardColumn("auto", cardButton("👎", cardCallback))+`]}}`,
)

// lineOf is the body row drawing text, and where it sits among the rows.
func lineOf(t *testing.T, rows []msgRow, text string) (msgRow, int) {
	t.Helper()
	for i, r := range rows {
		line := ansi.Strip(r.text)
		for _, s := range r.segs {
			line += ansi.Strip(s.text)
		}
		if strings.Contains(line, text) {
			return r, i
		}
	}
	t.Fatalf("no row draws %q", text)
	return msgRow{}, 0
}

func TestCardRows_AFooterThatFitsSharesOneRow(t *testing.T) {
	st := baseStyle()
	rows := renderRows([]store.Message{footerCard}, st)
	row, i := lineOf(t, rows, "完成")
	line, _ := Model{}.rowLine(row, st.width)
	plain := ansi.Strip(line)
	require.Regexp(t, `✅ 完成 · 18\.2s +👍 +👎 *$`, plain, "status on the left, the buttons on the right")

	require.Len(t, row.zones, 2, "each pill keeps its target")
	for _, z := range row.zones {
		pill := ansi.Strip(cut(cutLeft(line, z.x0), z.x1-z.x0))
		require.True(t, strings.Contains(pill, "👍") || strings.Contains(pill, "👎"), "zone %v covers %q", z, pill)
	}
	_, j := lineOf(t, rows, "收到")
	require.Equal(t, j+2, i, "a blank line parts the footer from the answer")
}

func TestCardRows_AFooterTooWideForThePaneStacks(t *testing.T) {
	st := baseStyle()
	st.width = leadWidth + 16
	rows := renderRows([]store.Message{footerCard}, st)
	status, i := lineOf(t, rows, "完成")
	require.Empty(t, status.zones, "the status has a line of its own")
	pills, k := lineOf(t, rows, "👍")
	require.Equal(t, i+1, k, "and the buttons the line below it")
	require.Len(t, pills.zones, 2)
}

// fillButton is a button the card stretches across the column it stands in.
func fillButton(label, url string) string {
	return `{"tag":"button","property":{"type":"default","widthValue":{"type":"builtin_width","value":"fill"},` +
		`"text":{"tag":"plain_text","property":{"content":"` + label + `"}},` +
		`"actions":[{"type":"open_url","action":{"url":"` + url + `"}}]}}`
}

// alarmActions is the row of links an alarm bot closes its card with: a
// weighted column per button, each button filling its column.
var alarmActions = bodyCard(`{"tag":"column_set","property":{"columns":[` +
	cardColumn("weighted", fillButton("详情", "https://example.com/alarm")) + `,` +
	cardColumn("weighted", fillButton("日志", "https://example.com/log")) + `,` +
	cardColumn("weighted", fillButton("排查", "https://example.com/triage")) + `]}}`)

func TestCardRows_FillButtonsShareTheirColumnsRow(t *testing.T) {
	st := baseStyle()
	rows := renderRows([]store.Message{alarmActions}, st)
	row, _ := lineOf(t, rows, "详情")
	line, _ := Model{}.rowLine(row, st.width)
	require.Regexp(t, `详情.+日志.+排查`, ansi.Strip(line), "the buttons stand abreast")
	require.Len(t, row.zones, 3, "each pill keeps its target")

	room := st.inner() - 2*cardCellGap
	shares := []int{room / 3, room / 3, room - 2*(room/3)}
	labels := []string{"详情", "日志", "排查"}
	urls := []string{"https://example.com/alarm", "https://example.com/log", "https://example.com/triage"}
	at := row.lead.cols()
	for i, z := range row.zones {
		require.Equal(t, at, z.x0, "pill %d starts where its column does", i)
		require.Equal(t, shares[i], z.x1-z.x0, "pill %d spans its column", i)
		require.Equal(t, []string{urls[i]}, z.urls)
		pill := ansi.Strip(cut(cutLeft(line, z.x0), z.x1-z.x0))
		require.Equal(t, labels[i], strings.TrimSpace(pill))
		lead, trail := len(pill)-len(strings.TrimLeft(pill, " ")), len(pill)-len(strings.TrimRight(pill, " "))
		require.InDelta(t, lead, trail, 1, "the label sits in the middle of %q", pill)
		at = z.x1 + cardCellGap
	}
	require.Equal(t, row.lead.cols()+st.inner(), row.zones[2].x1, "the row ends at the edge of the body")
}

func TestCardRows_FillButtonsTooWideForThePaneStack(t *testing.T) {
	st := baseStyle()
	st.width = leadWidth + 12
	rows := renderRows([]store.Message{alarmActions}, st)
	_, first := lineOf(t, rows, "详情")
	for k, label := range []string{"详情", "日志", "排查"} {
		row, i := lineOf(t, rows, label)
		require.Equal(t, first+k, i, "%s has a line of its own", label)
		require.Len(t, row.zones, 1)
		require.Equal(t, st.inner(), row.zones[0].x1-row.zones[0].x0, "spanning the body")
	}
}

func TestRenderInline_AFontTagColoursItsText(t *testing.T) {
	ms := mentionsIn("", "")
	out := renderInline(`<font color="red">**告警**</font> 已恢复`, ms)
	require.Equal(t, "告警 已恢复", ansi.Strip(out), "the tag is drawn, not spelled")
	require.Contains(t, out, lipgloss.NewStyle().Foreground(colErr).Bold(true).Render("告警"))
	require.Equal(t, "已恢复", ansi.Strip(renderInline(`<font color="grey-600">已恢复</font>`, ms)),
		"a shade of a colour is that colour")
	require.Equal(t, "告警", inlineText(`<font color="red">告警</font>`), "the one-line gist reads the words")
}

func TestCardRows_ACentredBlockSitsInTheMiddle(t *testing.T) {
	st := baseStyle()
	title := `{"tag":"markdown","property":{"textAlign":"center","textStyle":{"size":"heading"},` +
		`"elements":[{"tag":"plain_text","property":{"content":"每日巡检"}}]}}`
	rows := renderRows([]store.Message{bodyCard(title)}, st)
	row, _ := lineOf(t, rows, "每日巡检")
	text := strings.TrimRight(ansi.Strip(row.text), " ")
	pad := len(text) - len(strings.TrimLeft(text, " "))
	require.Equal(t, (st.inner()-ansi.StringWidth("每日巡检"))/2, pad, "centred in the body")
	require.Contains(t, row.text, "\x1b[1m", "a heading-sized title is bold")
}

func TestCardHead_PaintsTheTemplateAcrossTheRow(t *testing.T) {
	st := baseStyle()
	rows := renderRows([]store.Message{weeklyCard}, st)
	row, _ := lineOf(t, rows, "设备版本周报")
	require.Equal(t, st.inner(), ansi.StringWidth(row.text), "the band runs the width of the body")
	require.Contains(t, row.text, ansi.Style{}.BackgroundColor(cardTemplates["blue"].bg).String()[2:], "in the template's colour")
}

func TestCardButtons_EachTypeHasItsOwnPill(t *testing.T) {
	pill := func(typ string) string {
		return cardButtons([]card.Button{{Label: "停止", Type: typ}}, 40, "")[0].text
	}
	require.NotEqual(t, pill("default"), pill("danger"))
	require.NotEqual(t, pill("default"), pill("primary"))
	require.NotContains(t, pill("text"), "\x1b[48", "a text button lays no fill")
	require.Equal(t, "停止", strings.TrimSpace(ansi.Strip(pill("danger"))))
}

func TestCardButtons_AFillButtonSpansTheLine(t *testing.T) {
	lines := cardButtons([]card.Button{{Label: "停止", Type: "danger", Fill: true}}, 30, applink.ChatLink("oc_ops", "", 1))
	require.Len(t, lines, 1)
	require.Equal(t, 30, ansi.StringWidth(lines[0].text))
	require.Len(t, lines[0].zones, 1)
	require.Equal(t, 30, lines[0].zones[0].x1-lines[0].zones[0].x0, "the whole line is the button")
	text := ansi.Strip(lines[0].text)
	require.Equal(t, len(text)-len(strings.TrimLeft(text, " ")), len(text)-len(strings.TrimRight(text, " ")),
		"the label sits in the middle")
}

func TestRowLine_TheSelectionCoversAPanel(t *testing.T) {
	m := Model{th: themeFor(lipgloss.Color("#ffffff"), false)}
	row := msgRow{text: "状态", panel: true}
	plain := m.paneLine(row, 20, false, false)
	require.Contains(t, plain, ansi.Style{}.BackgroundColor(m.th.panel.GetBackground()).String()[2:], "a panel lays its shade")
	selected := m.paneLine(row, 20, true, true)
	require.NotContains(t, selected, ansi.Style{}.BackgroundColor(m.th.panel.GetBackground()).String()[2:],
		"a selected row shows the selection, not the panel under it")
}

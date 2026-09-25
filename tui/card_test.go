package tui

import (
	"encoding/json"
	"strconv"
	"strings"
	"testing"

	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/store"
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
	for _, line := range strings.Split(out, "\n") {
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
	require.Equal(t, []string{feishuChatLink("oc_ops", 42)}, zones[1].urls,
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
	require.Contains(t, rowText(renderRows([]store.Message{msg}, baseStyle())), "[图片]")
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

	// A card with no band of its own is named by the first thing it says,
	// with the markers its structure is spelled by left behind.
	c, ok = card.Parse(cardOf(elHeading+","+elList, nil).ContentRaw)
	require.True(t, ok)
	require.Equal(t, "报表", cardGist(c))
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

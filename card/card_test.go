package card

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const (
	elHeading   = `{"tag":"heading","property":{"level":1,"elements":[{"tag":"plain_text","property":{"content":"报表"}}]}}`
	elList      = `{"tag":"list","property":{"items":[{"type":"ul","level":0,"elements":[{"tag":"plain_text","property":{"content":"甲"}}]},{"type":"ul","level":1,"elements":[{"tag":"plain_text","property":{"content":"乙"}}]}]}}`
	elOrdered   = `{"tag":"list","property":{"items":[{"type":"ol","order":3,"elements":[{"tag":"plain_text","property":{"content":"三"}}]},{"type":"ol","order":4,"elements":[{"tag":"plain_text","property":{"content":"四"}}]}]}}`
	elQuote     = `{"tag":"blockquote","property":{"elements":[{"tag":"plain_text","property":{"content":"引文"}},{"tag":"br","property":{}},{"tag":"plain_text","property":{"content":"还是引文"}}]}}`
	elCodeBlock = `{"tag":"code_block","property":{"language":"go","contents":[{"contents":[{"content":"x ","contentType":"text"},{"content":":= 1","contentType":"operator"}]}]}}`
	elTable     = `{"tag":"table","property":{"columns":[{"name":"0","displayName":"项"},{"name":"1","displayName":"值"}],"rows":[{"0":{"data":{"tag":"markdown","property":{"elements":[{"tag":"plain_text","property":{"content":"甲"}}]}}},"1":{"data":"1"}}]}}`
	elRule      = `{"tag":"hr","property":{}}`
	elLink      = `{"tag":"link","property":{"content":"链接","url":{"url":"https://example.com"}}}`
)

// cardJSON wraps elements the way an interactive message carries them: the
// card as embedded JSON, beside the table its pictures and mentions live in.
func cardJSON(elements string, attachment map[string]any) string {
	body := `{"schema":"2.0","body":{"tag":"body","property":{"elements":[` +
		`{"tag":"markdown","property":{"elements":[` + elements + `]}}]}}}`
	return envelopeJSON(body, attachment)
}

// headedJSON is the same, with the band a bot's report carries above it.
func headedJSON(title, subtitle, tag, elements string) string {
	head := `{"tag":"card_header","property":{"template":"blue","title":{"tag":"plain_text","property":{"content":"` + title +
		`"}},"subtitle":{"tag":"plain_text","property":{"content":"` + subtitle +
		`"}},"textTagList":[{"tag":"text_tag","property":{"color":"purple","text":{"tag":"plain_text","property":{"content":"` +
		tag + `"}}}}]}}`
	body := `{"schema":"2.0","header":` + head + `,"body":{"tag":"body","property":{"elements":[` +
		`{"tag":"markdown","property":{"elements":[` + elements + `]}}]}}}`
	return envelopeJSON(body, nil)
}

func envelopeJSON(body string, attachment map[string]any) string {
	if attachment == nil {
		attachment = map[string]any{}
	}
	raw, err := json.Marshal(map[string]any{"json_card": body, "json_attachment": attachment, "card_schema": 2})
	if err != nil {
		panic(err)
	}
	return string(raw)
}

func blocks(t *testing.T, elements string) []Block {
	t.Helper()
	c, ok := Parse(cardJSON(elements, nil))
	require.True(t, ok)
	return c.Blocks
}

func markdown(t *testing.T, elements string) string {
	t.Helper()
	c, ok := Parse(cardJSON(elements, nil))
	require.True(t, ok)
	return c.Markdown()
}

// TestParse_BlocksAreKeptApart is the shape of the card this package reads the
// schema for: block elements sit side by side in one body, and a blank line
// between them is what keeps them from parsing as one paragraph.
func TestParse_BlocksAreKeptApart(t *testing.T) {
	out := markdown(t, strings.Join([]string{elHeading, elList, elQuote, elCodeBlock, elTable, elRule, elLink}, ","))
	require.Equal(t, strings.Join([]string{
		"# 报表",
		"- 甲\n  - 乙",
		"> 引文\n> 还是引文",
		"```go\nx := 1\n```",
		"| 项 | 值 |\n| --- | --- |\n| 甲 | 1 |",
		"---",
		"[链接](https://example.com)",
	}, "\n\n"), out)
}

func TestParse_ListsKeepTheirMarkersAndNesting(t *testing.T) {
	require.Equal(t, "3. 三\n4. 四", blocks(t, elOrdered)[0].Markdown, "an ordered list keeps the numbers it was sent with")
}

func TestParse_TextStyleBecomesTheMarkupThatSpellsIt(t *testing.T) {
	out := markdown(t, `{"tag":"plain_text","property":{"content":"粗","textStyle":{"attributes":["bold"]}}},`+
		`{"tag":"plain_text","property":{"content":"斜删","textStyle":{"attributes":["italic","strikethrough"]}}}`)
	require.Equal(t, "**粗***~~斜删~~*", out)
}

func TestParse_HeaderBandNamesTheCard(t *testing.T) {
	c, ok := Parse(headedJSON("设备版本周报", "最新 1211", "兜底",
		`{"tag":"plain_text","property":{"content":"待认领账号：13 个"}}`))
	require.True(t, ok)
	require.Equal(t, "设备版本周报", c.Title)
	require.Equal(t, "最新 1211", c.Subtitle)
	require.Equal(t, "「兜底」", c.Tags)
	require.Equal(t, []Block{{Markdown: "待认领账号：13 个"}}, c.Blocks)
}

func TestParse_PictureComesFromTheAttachmentTable(t *testing.T) {
	att := map[string]any{"images": map[string]any{"19": map[string]any{"origin_key": "img_card"}}}
	c, ok := Parse(cardJSON(`{"tag":"img","property":{"imageID":"19"}}`, att))
	require.True(t, ok)
	require.Equal(t, []Block{{ImageKey: "img_card"}}, c.Blocks,
		"the body names an id; the key it stands for is in the attachment table")
}

func TestParse_MentionCarriesTheNameAndTheKeyItIsPairedWith(t *testing.T) {
	att := map[string]any{"at_users": map[string]any{
		"ou_app": map[string]any{"content": "李四", "mention_key": "@_user_1", "user_id": "7480000000000000000"},
	}}
	c, ok := Parse(cardJSON(`{"tag":"at","property":{"userID":"ou_app"}}`, att))
	require.True(t, ok)
	require.Equal(t, `<at user_id="@_user_1">李四</at>`, c.Blocks[0].Markdown,
		"a card @s by an id of the sending app's own, so the key is what reaches the reader's mentions")

	// Nobody paired it: the name the card carries is still who it @s.
	att = map[string]any{"at_users": map[string]any{"ou_app": map[string]any{"content": "王五"}}}
	c, ok = Parse(cardJSON(`{"tag":"at","property":{"userID":"ou_app"}}`, att))
	require.True(t, ok)
	require.Equal(t, `<at user_id="ou_app">王五</at>`, c.Blocks[0].Markdown)
}

// button is how the runtime DSL spells one: the label it shows, over the list
// of what pressing it does.
func button(label, actions string) string {
	return `{"tag":"button","property":{"type":"default","text":{"tag":"plain_text","property":{"content":"` + label +
		`"}},"actions":[` + actions + `]}}`
}

// callback is what a button that calls back to the app that sent the card
// carries: a handle, and an empty value — the payload only ever reaches that
// app.
const callback = `{"type":"action_request","action":{"actionID":"act_v1_1","value":"","isValueObjectType":true}}`

func TestParse_ButtonsOfOneRowStayTogether(t *testing.T) {
	c, ok := Parse(cardJSON(`{"tag":"action","property":{"actions":[`+
		button("认领", callback)+`,`+button("忽略", callback)+`]}}`, nil))
	require.True(t, ok)
	require.Equal(t, []Block{{Buttons: []Button{{Label: "认领"}, {Label: "忽略"}}}}, c.Blocks)
}

func TestParse_ButtonKeepsTheLinkItOpens(t *testing.T) {
	c, ok := Parse(cardJSON(button("详情", `{"type":"open_url","action":{"url":"https://example.com/run/1"}}`), nil))
	require.True(t, ok)
	require.Equal(t, []Block{{Buttons: []Button{{Label: "详情", URL: "https://example.com/run/1"}}}}, c.Blocks)
}

func TestParse_ButtonTakesTheTargetMeantForThisMachine(t *testing.T) {
	c, ok := Parse(cardJSON(button("详情",
		`{"type":"open_url","action":{"url":"https://example.com/m","pcURL":"https://example.com/desktop"}}`), nil))
	require.True(t, ok)
	require.Equal(t, "https://example.com/desktop", c.Blocks[0].Buttons[0].URL,
		"a card names a desktop target of its own, and larkim runs on one")
}

// A button whose press calls the app that sent the card leads nowhere a
// reader can follow: the payload is delivered to that app alone, and no open
// API submits one.
func TestParse_ACallbackButtonLeadsNowhere(t *testing.T) {
	c, ok := Parse(cardJSON(button("同意", callback), nil))
	require.True(t, ok)
	require.Equal(t, []Button{{Label: "同意"}}, c.Blocks[0].Buttons)
}

func TestParse_ColumnsStack(t *testing.T) {
	col := func(text string) string {
		return `{"tag":"column","property":{"elements":[{"tag":"plain_text","property":{"content":"` + text + `"}}]}}`
	}
	require.Equal(t, "名称\n\n版本", markdown(t, `{"tag":"column_set","property":{"columns":[`+col("名称")+`,`+col("版本")+`]}}`),
		"columns stand side by side in the client and stack in a document")
}

// A card_schema 1 body is a bare list of elements, with no tag or property bag
// around it.
func TestParse_ReadsABodyThatCarriesItsElementsDirectly(t *testing.T) {
	body := `{"body":{"elements":[{"tag":"div","property":{"text":{"tag":"plain_text","property":{"content":"计提通知"}}}}]},"config":{}}`
	raw, err := json.Marshal(map[string]any{"json_card": body, "card_schema": 1})
	require.NoError(t, err)

	c, ok := Parse(string(raw))
	require.True(t, ok)
	require.Equal(t, []Block{{Markdown: "计提通知"}}, c.Blocks)
}

// A sender who writes in several languages sends them all.
func TestParse_ReadsTextSentInSeveralLanguages(t *testing.T) {
	require.Equal(t, "请先填写表单",
		markdown(t, `{"tag":"plain_text","property":{"i18nContent":{"en_us":"Please fill in the form","zh_cn":"请先填写表单"}}}`))
}

func TestParse_TakesOnlyACard(t *testing.T) {
	_, ok := Parse(`{"text":"只是正文"}`)
	require.False(t, ok, "a body that is not a card is left to whoever asked")

	_, ok = Parse("")
	require.False(t, ok)

	_, ok = Parse(`{"json_card":"{}"}`)
	require.False(t, ok, "a card with nothing in it is named by its message type instead")
}

func TestCard_MarkdownCopiesTheDocumentTheCardIs(t *testing.T) {
	c, ok := Parse(headedJSON("版本周报", "最新 1211", "兜底", elHeading+
		`,{"tag":"action","property":{"actions":[{"tag":"button","property":{"text":{"tag":"plain_text","property":{"content":"认领"}}}}]}}`))
	require.True(t, ok)
	require.Equal(t, "版本周报 最新 1211 「兜底」\n\n# 报表\n\n[认领]", c.Markdown())
}

// A card names itself for the chat list and the notification with a summary
// of its own, which the body it draws says nothing about.
func TestParse_ReadsTheSummaryTheSenderWroteForTheCard(t *testing.T) {
	body := `{"schema":"2.0","config":{"summary":{"content":"应用 order-api 普通预警"}},` +
		`"header":{"tag":"card_header","property":{"title":{"tag":"plain_text","property":{"content":"告警"}}}},` +
		`"body":{"tag":"body","property":{"elements":[{"tag":"markdown","property":{"elements":[` +
		`{"tag":"plain_text","property":{"content":"1 分钟内出现 2 条异常"}}]}}]}}}`

	c, ok := Parse(envelopeJSON(body, nil))
	require.True(t, ok)
	require.Equal(t, "应用 order-api 普通预警", c.Summary)
	require.Equal(t, "告警", c.Title)
}

// A card whose sender drew nothing still arrives when it says what it is.
func TestParse_TakesACardThatIsOnlyItsSummary(t *testing.T) {
	c, ok := Parse(envelopeJSON(`{"schema":"2.0","config":{"summary":{"content":"构建完成"}}}`, nil))
	require.True(t, ok)
	require.Equal(t, "构建完成", c.Summary)
	require.Empty(t, c.Blocks)
}

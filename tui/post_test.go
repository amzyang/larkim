package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// postRaw is a rich-text message drawn from its own body, with no rendering
// behind it: that is the state every post is in when it lands, and the state a
// merged forward's children never leave.
func postRaw(raw string) []store.Message {
	return []store.Message{{MessageID: "om_1", SenderName: "张三", SenderID: "ou_a",
		MsgType: "post", ContentRaw: raw, CreateMs: msgAt(23, 9, 0)}}
}

// postLines is what a body draws, one string per row, past the day rule and
// the sender line. Blank rows stay in: they are what an empty paragraph is.
func postLines(t *testing.T, raw string, st msgStyle) []string {
	t.Helper()
	rows := renderRows(postRaw(raw), st)
	out := make([]string, 0, len(rows))
	for _, r := range rows[2:] {
		out = append(out, strings.TrimRight(ansi.Strip(segText(r)), " "))
	}
	return out
}

func TestPostRows_DrawsTheBodyWithoutWaitingForARendering(t *testing.T) {
	require.Equal(t, []string{"今天上线"},
		postLines(t, `{"content":[[{"tag":"text","text":"今天上线"}]]}`, baseStyle()))
}

func TestPostRows_AnEmptyParagraphIsABlankLine(t *testing.T) {
	// The client draws one line per paragraph, an empty one included, so a run
	// of them is content rather than the separator a markdown parser reads.
	raw := `{"content_v2":[[{"tag":"text","text":"甲"}],[],[{"tag":"text","text":"","style":[]}],[{"tag":"text","text":"乙"}]]}`
	require.Equal(t, []string{"甲", "", "", "乙"}, postLines(t, raw, baseStyle()))
}

func TestPostRows_WordsAreNotMarkup(t *testing.T) {
	// A post spells its emphasis and its links as elements, so whatever
	// asterisks and backticks somebody typed are theirs.
	raw := `{"content":[[{"tag":"text","text":"3 * 4 * 5 和 ` + "`pausedSeconds`" + ` 与 **报文**"}]]}`
	require.Equal(t, []string{"3 * 4 * 5 和 `pausedSeconds` 与 **报文**"},
		postLines(t, raw, baseStyle()))
}

func TestPostRows_EmphasisComesFromTheElement(t *testing.T) {
	raw := `{"content":[[{"tag":"text","text":"重要","style":["bold"]},{"tag":"text","text":"其余"}]]}`
	rows := renderRows(postRaw(raw), baseStyle())
	require.Equal(t, []string{"重要其余"}, postLines(t, raw, baseStyle()), "no markup is drawn")

	var body string
	for _, r := range rows[2:] {
		body += segText(r)
	}
	require.Contains(t, body, stBold.Render("重要"))
	require.NotContains(t, body, stBold.Render("其余"), "the emphasis ends where the element does")
}

func TestPostRows_ACodeBlockKeepsItsLanguage(t *testing.T) {
	raw := `{"content":[[{"tag":"code_block","language":"go","text":"x := 1\n"}]]}`
	rows := renderRows(postRaw(raw), baseStyle())
	var body string
	for _, r := range rows[2:] {
		body += segText(r)
	}
	require.Contains(t, ansi.Strip(body), "x := 1")
	require.Contains(t, body, codeRule, "a code block is framed")
	require.NotContains(t, ansi.Strip(body), "```", "the fence is a rendering's spelling, not the body's")
	require.NotEqual(t, ansi.Strip(body), body, "go is highlighted")
}

func TestPostRows_AMentionCarriesTheOpenIDTheElementNames(t *testing.T) {
	st := baseStyle()
	st.self = "ou_me"
	raw := `{"content":[[{"tag":"at","user_id":"ou_me","user_name":"林岚"},{"tag":"text","text":"看一下"}]]}`
	rows := renderRows(postRaw(raw), st)
	var body string
	for _, r := range rows[2:] {
		body += segText(r)
	}
	require.Contains(t, body, stMentionMe.Render("@林岚"),
		"the body's own at element names the reader; no rendering has landed to say so")
	require.Contains(t, ansi.Strip(body), chipRight+" 看一下", "a mention run straight on is lent a space")
}

func TestPostRows_AMarkdownElementIsDrawnAsADocument(t *testing.T) {
	// The one element the client itself writes markdown into, so the one that
	// belongs on the markdown path.
	raw := `{"content_v2":[[{"tag":"md","text":"- 甲\n- 乙"}]]}`
	require.Equal(t, []string{"• 甲", "• 乙"}, postLines(t, raw, baseStyle()))
}

func TestPostRows_APictureStandsWhereItsParagraphPlacedIt(t *testing.T) {
	raw := `{"content_v2":[[{"tag":"text","text":"看图"}],[{"tag":"img","image_key":"img_a"}],[{"tag":"text","text":"谢谢"}]]}`
	lines := postLines(t, raw, baseStyle())
	require.Equal(t, "看图", lines[0])
	require.Equal(t, "谢谢", lines[len(lines)-1])
	require.Contains(t, strings.Join(lines, "\n"), "[Image]", "the picture takes the rows between them")
}

func TestPostRows_ALinkLeadsWhereTheElementPoints(t *testing.T) {
	raw := `{"content":[[{"tag":"text","text":"见 "},{"tag":"a","text":"看板","href":"https://example.com/b"}]]}`
	rows := renderRows(postRaw(raw), baseStyle())
	zones := rowZones(rows)
	require.Len(t, zones, 1)
	require.Equal(t, []string{"https://example.com/b"}, zones[0].urls)
	require.Equal(t, "看板", zones[0].label)
	require.NotContains(t, strings.Join(postLines(t, raw, baseStyle()), "\n"), "https://",
		"the client shows the label; the address stays in the body")
}

func TestPostRows_ATitleLeadsInBold(t *testing.T) {
	raw := `{"title":"发布说明","content":[[{"tag":"text","text":"今天上线"}]]}`
	rows := renderRows(postRaw(raw), baseStyle())
	require.Contains(t, segText(rows[2]), stBold.Render("发布说明"))
	require.Equal(t, "今天上线", strings.TrimRight(ansi.Strip(segText(rows[3])), " "))
}

func TestPostRows_TheNewerParagraphsWin(t *testing.T) {
	// Both spellings of the same body ride along; content_v2 is the one the
	// client writes now, and mixing them would double the words.
	raw := `{"content":[[{"tag":"text","text":"旧"}]],"content_v2":[[{"tag":"text","text":"新"}]]}`
	require.Equal(t, []string{"新"}, postLines(t, raw, baseStyle()))
}

func TestPostRows_ABodyThatCannotBeReadFallsBackToItsRendering(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", SenderName: "张三", SenderID: "ou_a", MsgType: "post",
		ContentRaw: "not json", Content: "# 标题", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	require.Contains(t, rowText(renderRows(msgs, baseStyle())), "标题")
}

func TestPostRows_AnEmotionIsDrawnAsTheClientsPicture(t *testing.T) {
	// emoji_type is the table's key, and the shortcode is the spelling keyed
	// by it: the display name is capped at twelve characters, which the keys
	// Feishu sends run past. The Lark_Emoji_ spelling is folded to the same
	// emoji, and every emotion draws as the client draws it.
	raw := `{"content":[[{"tag":"text","text":"好"},{"tag":"emotion","emoji_type":"Lark_Emoji_Thumbsup_0"},{"tag":"emotion","emoji_type":"DONE"}]]}`
	rows := renderRows(postRaw(raw), drawingStyle(t))
	require.Equal(t, 2, picsIn(rows))
	out := rowText(rows)
	require.NotContains(t, out, ":DONE:")
	require.NotContains(t, out, "Thumbsup")
}

func TestPostRows_AnEmojiSpelledInTheWordsIsDrawnAsTheCharacters(t *testing.T) {
	// The client draws an emoji in a post from an emotion element alone; a
	// name somebody typed arrives, and is drawn, as the characters it is.
	raw := `{"zh_cn":{"content":[[{"tag":"md","text":"**收到** [赞] a[Done]b"}],[{"tag":"text","text":":DONE: [THANKS]"}]]}}`
	rows := renderRows(postRaw(raw), drawingStyle(t))
	require.Zero(t, picsIn(rows))
	out := rowText(rows)
	require.Contains(t, out, "收到 [赞] a[Done]b")
	require.Contains(t, out, ":DONE: [THANKS]")
}

func TestBodyRows_AShortcodeInAFlattenedPostIsTheEmotionItWasWrittenFrom(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", SenderName: "张三", SenderID: "ou_a", MsgType: "post",
		Content: "好 :THUMBSUP: [赞]", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	out := rowText(renderRows(msgs, baseStyle()))
	require.Contains(t, out, "好 :THUMBSUP: [赞]", "sync writes an emotion back as :KEY:, and never as a bracketed name")
}

func TestPostRows_APostLarkimSentReadsBackTheWayItsPreviewDrewIt(t *testing.T) {
	// The send path writes one md element per paragraph and an empty text
	// element for each blank line between them, which is the only spelling of
	// a gap that survives Feishu; the preview draws the draft itself.
	draft := "## 发布说明\n\n- 修复 A\n- 修复 B"
	raw := `{"zh_cn":{"content":[[{"tag":"md","text":"## 发布说明"}],[{"tag":"text","text":""}],[{"tag":"md","text":"- 修复 A\n- 修复 B"}]]}}`
	require.Equal(t, mdLines(t, draft), postLines(t, raw, baseStyle()))
}

func TestPostRows_AParagraphOpeningWithABulletDrawsAsAListItem(t *testing.T) {
	// Feishu's rich text has no list element, so a list only ever arrives as
	// the characters somebody typed in front of each item.
	raw := `{"content_v2":[[{"tag":"text","text":"- ","style":[]},{"tag":"text","text":"甲","style":[]}],` +
		`[{"tag":"text","text":"- ","style":[]},{"tag":"text","text":"乙","style":[]}],` +
		`[{"tag":"text","text":"- ","style":[]},{"tag":"text","text":"丙","style":[]}]]}`
	require.Equal(t, []string{"• 甲", "• 乙", "• 丙"}, postLines(t, raw, baseStyle()))
}

func TestPostRows_ANumberedRunKeepsItsNumbers(t *testing.T) {
	raw := `{"content_v2":[[{"tag":"text","text":"1. 打包"}],[{"tag":"text","text":"2. 灰度"}]]}`
	require.Equal(t, []string{"1. 打包", "2. 灰度"}, postLines(t, raw, baseStyle()))
}

func TestPostRows_AnIndentedItemNestsUnderTheOneAboveIt(t *testing.T) {
	raw := `{"content_v2":[[{"tag":"text","text":"- 甲"}],[{"tag":"text","text":"  - 甲一"}],[{"tag":"text","text":"- 乙"}]]}`
	require.Equal(t, []string{"• 甲", "  ◦ 甲一", "• 乙"}, postLines(t, raw, baseStyle()))
}

func TestPostRows_AListEndsWhereTheParagraphsStopBeingOne(t *testing.T) {
	raw := `{"content_v2":[[{"tag":"text","text":"清单："}],[{"tag":"text","text":"- 甲"}],[],[{"tag":"text","text":"就这些"}]]}`
	require.Equal(t, []string{"清单：", "• 甲", "", "就这些"}, postLines(t, raw, baseStyle()))
}

func TestPostRows_AMarkerNeedsTheSpaceAfterIt(t *testing.T) {
	// A hyphen running straight into a word is a word, the way the client
	// draws it.
	raw := `{"content_v2":[[{"tag":"text","text":"-甲"}],[{"tag":"text","text":"3 * 4 * 5"}]]}`
	require.Equal(t, []string{"-甲", "3 * 4 * 5"}, postLines(t, raw, baseStyle()))
}

func TestPostRows_AListItemKeepsTheElementsItWasWrittenFrom(t *testing.T) {
	st := baseStyle()
	st.self = "ou_me"
	raw := `{"content_v2":[[{"tag":"text","text":"- "},{"tag":"at","user_id":"ou_me","user_name":"林岚"},` +
		`{"tag":"text","text":" 看 "},{"tag":"a","text":"看板","href":"https://example.com/b"}]]}`
	rows := renderRows(postRaw(raw), st)
	var body string
	for _, r := range rows[2:] {
		body += segText(r)
	}
	require.Contains(t, ansi.Strip(body), "•", "the marker the paragraph opened with is drawn as one")
	require.Contains(t, body, stMentionMe.Render("@林岚"))
	zones := rowZones(rows)
	require.Len(t, zones, 1)
	require.Equal(t, []string{"https://example.com/b"}, zones[0].urls)
	require.Equal(t, "看板", zones[0].label)
}

func TestPostRows_AParagraphNoListItemCouldHoldStaysOnTheElementPath(t *testing.T) {
	// The marker is there, but a code block is not something a list item
	// carries, so the paragraph is drawn as the elements it is.
	raw := `{"content_v2":[[{"tag":"text","text":"- "},{"tag":"code_block","language":"go","text":"x := 1\n"}]]}`
	lines := postLines(t, raw, baseStyle())
	require.Equal(t, "-", lines[0], "the marker is the text it was written as")
	require.Contains(t, strings.Join(lines, "\n"), "x := 1")
}

func TestPostRows_AListItemReadsItsWordsAsMarkup(t *testing.T) {
	// The cost of drawing a list at all: inside an item the words go through
	// the markdown path, so markup somebody typed is markup. Outside one they
	// stay the characters they are, which TestPostRows_WordsAreNotMarkup
	// holds.
	raw := `{"content_v2":[[{"tag":"text","text":"- 3 * 4 * 5 与 **报文**"}]]}`
	require.Equal(t, []string{"• 3 * 4 * 5 与 报文"}, postLines(t, raw, baseStyle()))
}

func TestPostRows_AListItemWearsEveryStyleItsElementCarries(t *testing.T) {
	// The client draws the four at once; respelling them as nested markup on
	// the way to the list path must not leave any of that markup on screen.
	raw := `{"content_v2":[[{"tag":"text","text":"1. ","style":[]},` +
		`{"tag":"text","text":"abcd","style":["italic","underline","lineThrough","bold"]}]]}`
	require.Equal(t, []string{"1. abcd"}, postLines(t, raw, baseStyle()))

	rows := renderRows(postRaw(raw), baseStyle())
	var body string
	for _, r := range rows[2:] {
		body += segText(r)
	}
	require.Contains(t, body,
		lipgloss.NewStyle().Bold(true).Italic(true).Underline(true).Strikethrough(true).Render("abcd"))
}

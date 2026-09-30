package larkmd

import (
	"encoding/json"
	"testing"

	"github.com/stretchr/testify/require"
)

// wireElem is one post element as Feishu reads it off the wire.
type wireElem struct {
	Tag       string   `json:"tag"`
	Text      string   `json:"text"`
	Style     []string `json:"style"`
	Href      string   `json:"href"`
	UserID    string   `json:"user_id"`
	EmojiType string   `json:"emoji_type"`
}

// postParas decodes a body back into the paragraphs it spells.
func postParas(t *testing.T, markdown string) [][]wireElem {
	t.Helper()
	var body struct {
		ZhCn struct {
			Content [][]wireElem `json:"content"`
		} `json:"zh_cn"`
	}
	require.NoError(t, json.Unmarshal([]byte(PostContent(markdown)), &body))
	return body.ZhCn.Content
}

func md(text string) []wireElem { return []wireElem{{Tag: "md", Text: text}} }

func TestPostContent_EscapesTheMarkdown(t *testing.T) {
	paras := postParas(t, "他说\"好\"\n| a |\n|---|")
	require.Equal(t, [][]wireElem{md("他说\"好\"\n| a |\n|---|")}, paras, "the draft survives the wrapper verbatim")
}

func TestPostContent_BlankLineBecomesAnEmptyTextParagraph(t *testing.T) {
	// Feishu drops a blank line inside an md element and strips an empty
	// paragraph sent as an empty array, so the gap is spelled this one way.
	require.Equal(t,
		`{"zh_cn":{"content":[[{"tag":"md","text":"## 发布说明"}],[{"tag":"text","text":""}],[{"tag":"md","text":"正文"}]]}}`,
		PostContent("## 发布说明\n\n正文"))
}

func TestPostContent_ConsecutiveBlankLinesEachBecomeAGap(t *testing.T) {
	paras := postParas(t, "上\n\n\n下")
	require.Len(t, paras, 4)
	require.Equal(t, []wireElem{{Tag: "text"}}, paras[1])
	require.Equal(t, []wireElem{{Tag: "text"}}, paras[2])
}

func TestPostContent_KeepsAFenceWhole(t *testing.T) {
	paras := postParas(t, "看代码：\n\n```go\nfunc a() {\n\n}\n```\n\n就这些")
	require.Len(t, paras, 5)
	require.Equal(t, md("```go\nfunc a() {\n\n}\n```"), paras[2], "a blank line inside a fence is code")
}

func TestPostContent_DropsTheBlankLinesAroundTheBody(t *testing.T) {
	require.Equal(t, [][]wireElem{md("正文")}, postParas(t, "\n\n正文\n\n"))
}

func TestPostContent_AnEmojiNameBecomesTheEmotionTheClientWrites(t *testing.T) {
	// The client's own editor sends "**abc** [Done] xyz" as exactly these
	// elements: an md element takes a line of its own, so a line carrying an
	// emoji is spelled in the elements the emoji sits among.
	require.Equal(t,
		`{"zh_cn":{"content":[[{"tag":"text","text":"abc","style":["bold"]},{"tag":"text","text":" "},`+
			`{"tag":"emotion","emoji_type":"DONE"},{"tag":"text","text":" xyz"}]]}}`,
		PostContent("**abc** [Done] xyz"))
}

func TestPostContent_EitherLanguagesNameReachesTheSameEmotion(t *testing.T) {
	want := [][]wireElem{{{Tag: "text", Text: "收到 "}, {Tag: "emotion", EmojiType: "DONE"}}}
	require.Equal(t, want, postParas(t, "收到 [完成]"))
	require.Equal(t, want, postParas(t, "收到 [Done]"))
}

func TestPostContent_AnEmojiLineAfterAQuoteGoesOnItsOwn(t *testing.T) {
	// "[Done]" continues the quote lazily as far as markdown goes; carried as
	// an emotion it is a line of its own, which is what Feishu makes of an
	// emotion beside an md element anyway.
	require.Equal(t,
		[][]wireElem{md("> abc"), {{Tag: "emotion", EmojiType: "DONE"}}},
		postParas(t, "> abc\n[Done]"))
}

func TestPostContent_OnlyTheEmojiLineLeavesItsParagraph(t *testing.T) {
	require.Equal(t, [][]wireElem{
		md("## 周报"),
		{{Tag: "text"}},
		md("第一行"),
		{{Tag: "text", Text: "上线 "}, {Tag: "emotion", EmojiType: "DONE"}},
		md("第三行 **加粗**"),
	}, postParas(t, "## 周报\n\n第一行\n上线 [Done]\n第三行 **加粗**"))
}

func TestPostContent_AnEmojiWhereOnlyMarkdownCanSayTheRestStaysAsTyped(t *testing.T) {
	for _, draft := range []string{
		"## 发布 [Done]",
		"> 上线 [Done]",
		"- 修复 A [Done]",
		"| 状态 |\n|---|\n| [Done] |",
		"跑 `go test` [Done]",
		"```\n[Done]\n```",
		"![shot](img_v3_shot) [Done]",
	} {
		paras := postParas(t, draft)
		for _, para := range paras {
			require.Equal(t, "md", para[0].Tag, "draft %q", draft)
		}
	}
	require.Equal(t, "emotion", postParas(t, "发布 [Done]")[0][1].Tag, "the same words without the markup send the emoji")
}

func TestPostContent_AMentionBesideAnEmojiIsAnAtElement(t *testing.T) {
	// Feishu fills the name in from the id, so the one between the tags is
	// not sent.
	require.Equal(t,
		[][]wireElem{{{Tag: "text", Text: "cc "}, {Tag: "at", UserID: "ou_a"}, {Tag: "text", Text: " "}, {Tag: "emotion", EmojiType: "DONE"}}},
		postParas(t, `cc <at user_id="ou_a">张三</at> [Done]`))
	require.Equal(t,
		[][]wireElem{{{Tag: "at", UserID: "all"}, {Tag: "text", Text: " 已发布 "}, {Tag: "emotion", EmojiType: "DONE"}}},
		postParas(t, `<at user_id="all"></at> 已发布 [Done]`))
}

func TestPostContent_EmphasisAndLinksBesideAnEmojiKeepTheirElements(t *testing.T) {
	require.Equal(t, [][]wireElem{{
		{Tag: "text", Text: "旧", Style: []string{"lineThrough"}},
		{Tag: "text", Text: " "},
		{Tag: "text", Text: "新", Style: []string{"italic"}},
		{Tag: "text", Text: " 见 "},
		{Tag: "a", Text: "文档", Href: "https://example.com/doc"},
		{Tag: "text", Text: " "},
		{Tag: "emotion", EmojiType: "DONE"},
	}}, postParas(t, "~~旧~~ *新* 见 [文档](https://example.com/doc) [Done]"))
}

func TestPostContent_EmphasisAroundAnEmojiCarriesOnPastIt(t *testing.T) {
	// An emotion element carries no style of its own, so the emphasis around
	// one is spelled on the words either side of it.
	require.Equal(t, [][]wireElem{{
		{Tag: "text", Text: "abc ", Style: []string{"bold"}},
		{Tag: "emotion", EmojiType: "DONE"},
		{Tag: "text", Text: " xyz", Style: []string{"bold"}},
	}}, postParas(t, "**abc [Done] xyz**"))
}

func TestPostContent_AWrittenOutAddressBesideAnEmojiIsALink(t *testing.T) {
	require.Equal(t, [][]wireElem{{
		{Tag: "text", Text: "见 "},
		{Tag: "a", Text: "https://example.com/x", Href: "https://example.com/x"},
		{Tag: "text", Text: " "},
		{Tag: "emotion", EmojiType: "DONE"},
	}}, postParas(t, "见 https://example.com/x [Done]"))
}

func TestPostContent_ALinkFeishuDropsLeavesItsWordsBesideTheEmoji(t *testing.T) {
	require.Equal(t,
		[][]wireElem{{{Tag: "text", Text: "见 笔记 "}, {Tag: "emotion", EmojiType: "DONE"}}},
		postParas(t, "见 [笔记](./notes.md) [Done]"))
}

func TestPostContent_ABracketThatNamesNoEmojiStaysMarkdown(t *testing.T) {
	require.Equal(t, [][]wireElem{md("[WIP] 修复中")}, postParas(t, "[WIP] 修复中"))
	require.Equal(t, [][]wireElem{md("见 [Done](https://example.com)")}, postParas(t, "见 [Done](https://example.com)"),
		"a link's label is its words")
}

func TestPostContent_AnEscapeIsTheCharacterItEscapes(t *testing.T) {
	require.Equal(t,
		[][]wireElem{{{Tag: "text", Text: "*不加粗* "}, {Tag: "emotion", EmojiType: "DONE"}}},
		postParas(t, `\*不加粗\* [Done]`))
}

func TestPostContent_AnIndentedContinuationWithAnEmojiLeavesTheListItem(t *testing.T) {
	require.Equal(t,
		[][]wireElem{md("- 修复 A"), {{Tag: "text", Text: "已上线 "}, {Tag: "emotion", EmojiType: "DONE"}}},
		postParas(t, "- 修复 A\n  已上线 [Done]"))
}

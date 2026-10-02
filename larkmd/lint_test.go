package larkmd

import (
	"testing"

	"github.com/stretchr/testify/require"
)

// rules is the rule of every finding, which is what most of these assert on:
// the wording is free to change, the set of rules that fired is not.
func rules(fs []Finding) []string {
	out := make([]string, 0, len(fs))
	for _, f := range fs {
		out = append(out, f.Rule)
	}
	return out
}

func TestLint_SaysNothingAboutAnOrdinaryBody(t *testing.T) {
	require.Empty(t, Lint(""))
	require.Empty(t, Lint("   \n\n"))
	require.Empty(t, Lint("## 周报\n\n- 修复了 A\n- 修复了 B\n\n见 [详情](https://example.com)"))
}

func TestLint_BareMentionNotifiesNobody(t *testing.T) {
	got := Lint("@张三 看下这个")
	require.Equal(t, []string{"mention_unresolved"}, rules(got))
	require.Equal(t, 1, got[0].Line)
	require.Equal(t, 1, got[0].Column)
	require.Contains(t, got[0].Message, "@张三")
	require.Contains(t, got[0].Hint, `<at user_id="ou_…">张三</at>`)
}

func TestLint_ResolvedMentionIsQuiet(t *testing.T) {
	// The tag is raw HTML to a parser, so the @ never reaches a text node.
	require.Empty(t, Lint(`<at user_id="ou_a">张三</at> 看下这个`))
	require.Empty(t, Lint(`<at user_id="all"></at> 今天停服`))
}

func TestLint_AnEmailIsNotAMention(t *testing.T) {
	require.Empty(t, Lint("写信到 linlan@example.com 就行"))
	require.Empty(t, Lint("单价 100@2 件"), "a digit before the @ is arithmetic")
	require.Empty(t, Lint("在 @ 后面写名字"), "a lone @ names nobody")
	require.Empty(t, Lint("看 @，然后呢"), "punctuation after the @ names nobody")
}

func TestLint_MentionInsideCodeIsQuoted(t *testing.T) {
	require.Empty(t, Lint("写成 `@张三` 就行"))
	require.Empty(t, Lint("```\n@张三 看下\n```"))
}

func TestLint_ShortcodeArrivesAsCharacters(t *testing.T) {
	// Feishu reads no colon spelling of an emoji, the canonical key included.
	for _, draft := range []string{"收到 :DONE:", "收到 :done:", "辛苦了 :SMILE:"} {
		got := Lint(draft)
		require.Equal(t, []string{"emoji_not_rendered"}, rules(got), "draft %q", draft)
	}
}

func TestLint_ShortcodeHintOffersTheSpellingTheSendCarries(t *testing.T) {
	// The hint names the bracketed spelling — the one the client itself puts
	// on the wire for its own emoji, and the one every client draws.
	got := Lint("摊手 :Shrug:")
	require.Len(t, got, 1)
	require.Contains(t, got[0].Hint, "[Shrug]")

	got = Lint("收到 :DONE:")
	require.Len(t, got, 1)
	require.Contains(t, got[0].Hint, "[Done]")
}

func TestLint_BracketNameOnAPlainLineIsSentAsTheEmoji(t *testing.T) {
	// emojiInsert writes [Name] for an emoji with no character of its own,
	// and the send carries it as the emotion the client would have written.
	require.Empty(t, Lint("收到 [Done] 了"))
	require.Empty(t, Lint("**收到** [完成]"))
	require.Empty(t, Lint("> abc\n[Done]"), "the line after the quote goes on its own")
	require.Equal(t, []string{"emoji_not_rendered"}, rules(Lint("> abc [Done]")), "the quote's own line keeps its brackets")
}

func TestLint_BracketNameInsideMarkupIsReported(t *testing.T) {
	// A line only markdown can say goes as an md element, which reads no
	// emoji name.
	got := Lint("## 发布 [Done]")
	require.Equal(t, []string{"emoji_not_rendered"}, rules(got))
	require.Contains(t, got[0].Message, "[Done]")
	require.Equal(t, 1+len("## 发布 "), got[0].Column, "the finding points at the opening bracket")

	for _, draft := range []string{"> 上线 [Done]", "跑 `go test` [完成]"} {
		require.Equal(t, []string{"emoji_not_rendered"}, rules(Lint(draft)), "draft %q", draft)
	}
}

func TestLint_EmojiNameInAListItemGoesAsACardAndIsNotReported(t *testing.T) {
	require.Empty(t, Lint("- 修复 A [Done]"), "the draft goes as a card, whose markdown reads the name")
	require.Empty(t, Lint("> 上线 [Done]\n\n- 修复 A [OK]"), "a card reads the name on the quote's line too")
}

func TestLint_ALinkLabelIsNotABracketedEmoji(t *testing.T) {
	require.Empty(t, Lint("见 [Done](https://example.com)"))
	require.Empty(t, Lint("看图 ![Done](img_v3_shot)"))
}

func TestLint_ATaskBoxIsNotABracketedEmoji(t *testing.T) {
	require.Empty(t, Lint("- [ ] 第一条\n- [x] 第二条"))
}

func TestLint_ProseBetweenColonsIsNotReported(t *testing.T) {
	// Feishu has no emoji under either name, and whether the sender meant one
	// or wrote prose is not decidable.
	require.Empty(t, Lint("辛苦了 :tada:"))
	require.Empty(t, Lint("看 :this: 一下"))
}

func TestLint_AClockIsNotAnEmoji(t *testing.T) {
	require.Empty(t, Lint("跑了 10:30:45 才结束"))
	require.Empty(t, Lint("开始 09:00:00 结束"))
}

func TestLint_EmojiInsideCodeIsQuoted(t *testing.T) {
	require.Empty(t, Lint("写成 `:DONE:` 就行"))
	require.Empty(t, Lint("```\n:DONE:\n[Done]\n```"))
}

func TestLint_ListWithABlankLineIsCutUp(t *testing.T) {
	got := Lint("- 第一条\n\n- 第二条\n")
	require.Equal(t, []string{"list_split"}, rules(got))
	require.Contains(t, got[0].Message, "2 separate post elements")

	got = Lint("1. 第一条\n\n   接着说\n2. 第二条\n")
	require.Equal(t, []string{"list_split"}, rules(got))
}

func TestLint_ListOnConsecutiveLinesIsQuiet(t *testing.T) {
	require.Empty(t, Lint("- 第一条\n- 第二条\n- 第三条\n"))
	require.Empty(t, Lint("前言\n\n- 第一条\n- 第二条\n\n后记\n"), "the gaps are around the list, not inside it")
}

func TestLint_FenceInsideAListIsNotACut(t *testing.T) {
	// Paragraphs keeps a fence whole, so the blank line in this one is code
	// rather than somewhere the wire parts the elements.
	require.Empty(t, Lint("- 跑这个：\n\n  ```sh\n  go test\n\n  go vet\n  ```\n"))
}

func TestLint_ReportsEveryFindingInABody(t *testing.T) {
	got := Lint("@张三 @李四 看下 :DONE:")
	require.Equal(t, []string{"mention_unresolved", "mention_unresolved", "emoji_not_rendered"}, rules(got))
	require.Less(t, got[0].Column, got[1].Column)
}

func TestLint_NamesTheLineAFindingSitsOn(t *testing.T) {
	got := Lint("## 周报\n\n见图\n\n@张三 看下")
	require.Len(t, got, 1)
	require.Equal(t, 5, got[0].Line)
	require.Equal(t, 1, got[0].Column)
}

func TestLint_MentionStopsWhereTheSentenceGoesOn(t *testing.T) {
	for draft, want := range map[string]string{
		"@张三，看下这个":      "@张三",
		"@张三。":          "@张三",
		"@李四(负责人) 看下":   "@李四",
		"@zhang.san 看下": "@zhang.san",
		"@zhang-san 看下": "@zhang-san",
	} {
		got := Lint(draft)
		require.Len(t, got, 1, "draft %q", draft)
		require.Contains(t, got[0].Message, want+" notifies nobody", "draft %q", draft)
	}
}

func TestLint_DroppedLinkIsReported(t *testing.T) {
	for _, dest := range []string{"/path", "./notes.md", "../up.md", "#section", "$urlVal", "mailto:linlan@example.com"} {
		got := Lint("见 [详情](" + dest + ")")
		require.Equal(t, []string{"link_dropped"}, rules(got), "dest %q", dest)
		require.Contains(t, got[0].Message, dest)
	}
}

func TestLint_AddressFeishuCanReadIsQuiet(t *testing.T) {
	// Feishu keeps a scheme it knows and anything shaped like a host, a bare
	// notes.md included — see docs/markdown-lint/DIALECT.md.
	for _, dest := range []string{
		"https://example.com", "http://example.com", "www.example.com", "example.com",
		"notes.md", "lark://applink.feishu.cn/client/chat/open?openChatId=oc_a",
	} {
		require.Empty(t, Lint("见 [详情]("+dest+")"), "dest %q", dest)
	}
	require.Empty(t, Lint("见 https://example.com"), "a bare address links itself")
}

func TestLint_DroppedLinkInsideCodeIsQuoted(t *testing.T) {
	require.Empty(t, Lint("写成 `[详情](/path)` 就行"))
	require.Empty(t, Lint("```\n[详情](/path)\n```"))
}

func TestLint_HTMLTagArrivesAsCharacters(t *testing.T) {
	got := Lint("这是 <b>粗</b> 的")
	require.Equal(t, []string{"html_literal"}, rules(got), "one finding for the pair, not two")
	require.Contains(t, got[0].Message, "<b>")
	require.Contains(t, got[0].Hint, "**bold**")

	require.Equal(t, []string{"html_literal"}, rules(Lint("换行<br>这里")))
	require.Equal(t, []string{"html_literal"}, rules(Lint(`<font color='red'>红</font>`)))
	require.Equal(t, []string{"html_literal"}, rules(Lint("<hr>")))
	require.Equal(t, []string{"html_literal"}, rules(Lint("<div>\nblock\n</div>")))
}

func TestLint_MentionTagIsTheOneTagAPostReads(t *testing.T) {
	require.Empty(t, Lint(`<at user_id="ou_a">林岚</at> 看下`))
	require.Empty(t, Lint(`<at user_id="all"></at> 今天停服`))
}

func TestLint_HTMLInsideCodeIsQuoted(t *testing.T) {
	require.Empty(t, Lint("写成 `<b>粗</b>` 就行"))
	require.Empty(t, Lint("```html\n<b>粗</b>\n```"))
}

func TestLint_TableAlignmentIsDropped(t *testing.T) {
	got := Lint("| l | c | r |\n| :-- | :-: | --: |\n| 1 | 2 | 3 |\n")
	require.Equal(t, []string{"table_alignment_ignored"}, rules(got), "one finding for the table, not one a cell")
	require.Equal(t, 1, got[0].Line)
}

func TestLint_TableWithoutAlignmentIsQuiet(t *testing.T) {
	// Feishu draws the grid, the cells' inline content and as many rows as
	// are written; only the alignment is lost.
	require.Empty(t, Lint("| n | v |\n| - | - |\n| 1 | one |\n| 2 | **two** |\n"))
	require.Empty(t, Lint("| n | v |\n| - | - |\n| 1 | a |\n| 2 | b |\n| 3 | c |\n| 4 | d |\n| 5 | e |\n| 6 | f |\n| 7 | g |\n"),
		"seven body rows arrive; the card documentation's five-row cap is not this renderer's")
}

func TestLint_ReadsAMentionOutOfBytesThatAreNotUTF8(t *testing.T) {
	// A body written on a GBK machine arrives as the bytes it was saved in,
	// and one that runs out mid-name must not take the process with it.
	var out []Finding
	require.NotPanics(t, func() { out = Lint("@\xd5\xc5") })
	require.Equal(t, []string{"mention_unresolved"}, rules(out))
}

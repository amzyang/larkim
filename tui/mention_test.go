package tui

import (
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

const selfMention = `[{"id":"ou_me","key":"@_user_1","name":"林岚"}]`

// badge is the drawn form of a mention of the reader: the filled run closed by
// the caps that round it off.
func badge(name string) string {
	return stMentionMeEdge.Render(chipLeft) + stMentionMe.Render(name) + stMentionMeEdge.Render(chipRight)
}

// pairMentions names the reader, the person across from them in a chat of two,
// and somebody that chat does not hold.
const pairMentions = `[{"id":"ou_me","name":"林岚"},{"id":"ou_peer","name":"张三"},{"id":"ou_a","name":"李四"}]`

func TestMentions_BadgesTheReaderAndAccentsEveryoneElse(t *testing.T) {
	me := mentionsIn(selfMention, "ou_me").render("@林岚 reachable")
	require.Equal(t, badge("@林岚")+" reachable", me)

	other := mentionsIn(`[{"id":"ou_ma","key":"@_user_1","name":"秦风"}]`, "ou_me").render("@秦风 unreachable")
	require.Equal(t, stAccent.Render("@秦风")+" unreachable", other,
		"a name called out in the body is worth a colour whoever it belongs to")

	require.Equal(t, stAccent.Render("@林岚")+" hi", mentionsIn(selfMention, "").render("@林岚 hi"),
		"before the identity sync lands nothing is the reader's own, so nothing is badged")
}

func TestMentions_BadgeWearsTheCapsAChipDoes(t *testing.T) {
	out := mentionsIn(selfMention, "ou_me").render("@林岚 hi")
	require.Equal(t, stMentionMeEdge.Render(chipLeft)+stMentionMe.Render("@林岚")+stMentionMeEdge.Render(chipRight)+" hi", out,
		"a cap is painted in the very fill it closes")
	require.Equal(t, lipgloss.Width("@林岚")+chipPad, lipgloss.Width(ansi.Strip(out))-len(" hi"),
		"a cap costs one column either side, the same as a reaction chip's")

	require.NotContains(t, mentionsIn(selfMention, "ou_x").render("@林岚 hi"), chipLeft,
		"only the reader's own mention is a badge; the rest are colour alone")
}

func TestMentions_AllReadsAsItsName(t *testing.T) {
	require.Equal(t, stAccent.Render(allName)+" all", mentionsIn("", "ou_me").render("@_all all"),
		"@_all resolves to no name of its own, so the client's spelling is ours to write")
}

func TestMentions_LongerNameWinsOverTheOneItContains(t *testing.T) {
	m := mentionsIn(`[{"id":"ou_me","name":"张三"},{"id":"ou_x","name":"张三丰"}]`, "ou_me")
	require.Equal(t, "@张三丰 在吗", ansi.Strip(m.render("@张三丰 在吗")))
	require.Equal(t, badge("@张三")+" 在吗", m.render("@张三 在吗"))
}

func TestMentions_OnDimsTheTextAround(t *testing.T) {
	out := mentionsIn(selfMention, "ou_me").on(stDim).render("群里 @林岚 看下")
	require.Equal(t, stDim.Render("群里 ")+badge("@林岚")+stDim.Render(" 看下"), out)
}

func TestMentions_OnKeepsOthersInTheLinesOwnDim(t *testing.T) {
	out := mentionsIn(`[{"id":"ou_a","key":"@_user_1","name":"李四"}]`, "ou_me").on(stDim).render("群里 @李四 看下")
	require.Equal(t, stDim.Render("群里 ")+stDim.Render("@李四")+stDim.Render(" 看下"), out,
		"a one-line summary says nothing by colouring an @ that is not the reader's")
}

func TestMentions_DimsAMentionThatReachesNobodyInAChatOfTwo(t *testing.T) {
	m := mentionsIn(pairMentions, "ou_me").facing("ou_peer")
	require.Equal(t, badge("@林岚")+" 在吗", m.render("@林岚 在吗"))
	require.Equal(t, stAccent.Render("@张三")+" 在吗", m.render("@张三 在吗"))
	require.Equal(t, stDim.Render("@李四")+" 在吗", m.render("@李四 在吗"),
		"a chat of two reaches nobody else, so a third name is a card link, not a call")
}

func TestMentions_FacingNobodyLeavesAGroupAlone(t *testing.T) {
	require.Equal(t,
		mentionsIn(pairMentions, "ou_me").render("@李四 在吗"),
		mentionsIn(pairMentions, "ou_me").facing("").render("@李四 在吗"),
		"a group has nobody sitting across from the reader")
}

func TestMentions_PostTagFollowsTheSameReach(t *testing.T) {
	m := mentionsIn("", "ou_me").facing("ou_peer")
	require.Equal(t, stAccent.Render("@张三")+" 在吗", m.render(`<at user_id="ou_peer">张三</at>在吗`))
	require.Equal(t, stDim.Render("@李四")+" 在吗", m.render(`<at user_id="ou_a">李四</at>在吗`))
	require.Equal(t, badge("@林岚")+" 在吗", m.render(`<at user_id="ou_me">林岚</at>在吗`))
}

func TestMentions_MalformedJSONStillDraws(t *testing.T) {
	require.Equal(t, "@林岚 hi", mentionsIn("not json", "ou_me").render("@林岚 hi"))
	require.Equal(t, "笑 😂", ansi.Strip(mentionsIn("", "ou_me").render("笑 [笑哭]")), "emoji still expand")
}

func TestMentions_BadgeSurvivesTheRowHighlight(t *testing.T) {
	m := Model{th: themeFor(lipgloss.Color("0"), true)}
	line := m.highlight(mentionsIn(selfMention, "ou_me").render("@林岚 hi"), true)
	require.Contains(t, line, stMentionMe.Render("@林岚"),
		"the selection background must not repaint a run that carries its own")
}

func TestMentions_PostTagResolvesToAName(t *testing.T) {
	require.Equal(t, stAccent.Render(allName)+" 各位伙伴好",
		mentionsIn("", "ou_me").render(`<at user_id="all"></at>各位伙伴好`),
		"a post keeps the tag lark-cli wrote, so it is resolved here")

	require.Equal(t, badge("@林岚")+" 在吗",
		mentionsIn("", "ou_me").render(`<at user_id="ou_me">林岚</at>在吗`))

	require.Equal(t, stAccent.Render("@秦风")+" 在吗",
		mentionsIn("", "ou_me").render(`<at user_id="ou_ma">秦风</at>在吗`))

	require.Equal(t, stAccent.Render("@ou_x")+" hi", mentionsIn("", "ou_me").render(`<at user_id="ou_x"></at>hi`),
		"a tag carrying no name falls back to the id rather than drawing empty")
}

func TestMentions_GapOnlyWhereTheBodyWouldGlue(t *testing.T) {
	m := mentionsIn("", "ou_me")
	require.Equal(t, stAccent.Render(allName)+"，注意", m.render(`<at user_id="all"></at>，注意`),
		"punctuation separates on its own")
	require.Equal(t, stAccent.Render(allName)+" 注意", m.render(`<at user_id="all"></at> 注意`),
		"the author's own space is not doubled")
	require.Equal(t, stAccent.Render(allName), m.render(`<at user_id="all"></at>`))
}

func TestMentions_UnclosedTagStaysText(t *testing.T) {
	require.Equal(t, `<at user_id="all"> hi`, mentionsIn("", "ou_me").render(`<at user_id="all"> hi`))
}

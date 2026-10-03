package tui

import (
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// lintModel is the composer in insert mode with one colleague on the roster,
// which is what tells a mention the composer can place from one it cannot.
func lintModel(t *testing.T) Model {
	t.Helper()
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.files = fakeFiles(nil)
	m.roster = []store.Contact{{OpenID: "ou_a", Name: "张三"}}
	return m
}

func TestReplan_BadgeNamesWhatTheDraftLoses(t *testing.T) {
	t.Parallel()
	m := lintModel(t)
	m.input.SetValue("## 周报\n\n@李四 看下")
	m.replan()

	require.Len(t, m.draftLint, 1)
	require.Equal(t, "mention_unresolved", m.draftLint[0].Rule)
	require.Contains(t, ansi.Strip(m.renderBadge(m.width-2)), "@李四")
}

func TestReplan_AMentionTheComposerPlacedDrawsNoWarning(t *testing.T) {
	t.Parallel()
	m := lintModel(t)
	m.input.SetValue("## 周报\n\n@张三 看下")
	m.replan()
	require.Empty(t, m.draftLint, "resolveMentions turns this into a tag before the lint reads it")
}

func TestReplan_TextDraftIsNotLinted(t *testing.T) {
	t.Parallel()
	m := lintModel(t)
	m.input.SetValue("@李四 看下")
	m.replan()
	require.Equal(t, kindText, m.draft.kind)
	require.Empty(t, m.draftLint, "a text message is sent verbatim, so nothing in it is markdown")
}

func TestRenderBadge_CountsTheFindingsPastTheFirst(t *testing.T) {
	t.Parallel()
	m := lintModel(t)
	m.input.SetValue("## 周报\n\n@李四 @王五 看下")
	m.replan()

	require.Len(t, m.draftLint, 2)
	require.Contains(t, ansi.Strip(m.renderBadge(m.width-2)), "2 · ")
}

func TestRenderBadge_ARefusedPathBeatsAFinding(t *testing.T) {
	t.Parallel()
	m := lintModel(t)
	m.input.SetValue("## 周报\n\n@李四 看下\n\n![图](~/nope.png)")
	m.replan()

	badge := ansi.Strip(m.renderBadge(m.width - 2))
	require.Contains(t, badge, "no such file")
	require.NotContains(t, badge, "@李四")
}

func TestRunCommand_SendCarriesTheFindingInItsNotice(t *testing.T) {
	t.Parallel()
	m := lintModel(t)
	m.chats = []store.Chat{{ChatID: "oc_1", Name: "平台组"}}

	mm, cmd := m.runCommand("send 平台组 ## 周报\n\n@李四 看下")
	m = mm.(Model)
	require.NotNil(t, cmd, "the finding is advisory, so the send still goes")
	require.Contains(t, m.notice, "sending…")
	require.Contains(t, m.notice, "@李四")
}

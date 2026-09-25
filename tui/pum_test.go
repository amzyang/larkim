package tui

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// newPumModel is a model in a group chat with three colleagues in it, writing.
func newPumModel(t *testing.T) Model {
	t.Helper()
	m, _ := newOutboxModel(t)
	// Accepting an emoji writes the remembered list, and a model with nowhere
	// to write it would leave the file in the repository.
	m.deps.DataDir = t.TempDir()
	m.chatID = "oc_group"
	m.chats = []store.Chat{{ChatID: "oc_group", Name: "平台组", ChatMode: "group"}}
	m.roster = []store.Contact{
		{OpenID: "ou_me", Name: "林岚"},
		{OpenID: "ou_a", Name: "张三"},
		{OpenID: "ou_b", Name: "李四"},
		{OpenID: "cli_c", Name: "构建机器人", IsBot: true},
	}
	mm, _ := m.startInsert(nil, false)
	return mm.(Model)
}

func TestPumRunAt_OpensOnATriggerAtAWordBoundary(t *testing.T) {
	for _, tc := range []struct {
		line  string
		kind  pumKind
		runes int
		query string
	}{
		{"@", pumMention, 1, ""},
		{"@zh", pumMention, 3, "zh"},
		{"好的 @zh", pumMention, 3, "zh"},
		{":do", pumEmoji, 3, "do"},
		{"发一个 :666", pumEmoji, 4, "666"},
		{"(:rocket", pumEmoji, 7, "rocket"},
		{":点赞", pumEmoji, 3, "点赞"},
		{"[ok", pumEmoji, 3, "ok"},
		{"发一个 [鼓掌", pumEmoji, 3, "鼓掌"},
		{"[完成][wan", pumEmoji, 4, "wan"},
	} {
		run, ok := pumRunAt(tc.line)
		require.True(t, ok, tc.line)
		assert.Equal(t, tc.kind, run.kind, tc.line)
		assert.Equal(t, tc.runes, run.runes, tc.line)
		assert.Equal(t, tc.query, run.query, tc.line)
	}
}

func TestPumRunAt_StaysShutInsideAWord(t *testing.T) {
	for _, line := range []string{
		"",
		"好的",
		"http://x",
		"14:30",
		"note:",
		"linlan@example.com",
		":)",
		":-(",
		"@zh ", // the run ended with the space
		":",    // a lone trigger is punctuation until a name stands behind it
		":d",
		"[",
		"[a",
		"[]", // the file-send form closes the run before a name can stand
		"[](a.png)",
		"foo[ab",                      // a bracket inside a word opens nothing, as a colon does not
		"[完成] ",                       // the reader spelled it out; the run ended with the space
		":" + strings.Repeat("a", 33), // prose that happens to follow a colon
	} {
		_, ok := pumRunAt(line)
		assert.False(t, ok, line)
	}
}

func TestLineBeforeCursor_CountsRunesNotBytes(t *testing.T) {
	m := newPumModel(t)
	m.input.SetValue("你好世界")
	m.input.SetCursorColumn(2)
	require.Equal(t, "你好", lineBeforeCursor(m.input))

	// A run never reaches across a newline into the line above it.
	m.input.SetValue("第一行 @a\n第二行")
	m.input.MoveToEnd()
	require.Equal(t, "第二行", lineBeforeCursor(m.input))
}

func TestPum_OpensOnTheRosterAndNarrowsByPinyin(t *testing.T) {
	m := typeInto(newPumModel(t), "@")
	require.True(t, m.pum.open())
	// @All leads, and everyone in the chat follows in roster order.
	require.Equal(t, []string{"All", "林岚", "张三", "李四", "构建机器人"}, pumNames(m))

	m = typeInto(m, "zs")
	require.Equal(t, []string{"张三"}, pumNames(m), "pinyin initials reach a Chinese name")
}

// A chat of two keeps no member list, but @ opens on it all the same: naming
// the peer is what makes a message a reminder rather than one more line.
func TestPum_OpensInAChatOfTwoOnThePeerAndSelf(t *testing.T) {
	m := newPumModel(t)
	m.chatID = "oc_pair"
	m.chats = []store.Chat{{ChatID: "oc_pair", Name: "张三", ChatMode: "p2p", P2PTargetID: "ou_a"}}
	m.roster = []store.Contact{{OpenID: "ou_a", Name: "张三"}, {OpenID: "ou_me", Name: "林岚"}}

	m = typeInto(m, "@")

	require.True(t, m.pum.open())
	// No @All, matching the client: a chat of two has nobody to shout at.
	require.Equal(t, []string{"张三", "林岚"}, pumNames(m))
}

func TestPum_OffersTheReaderThemselves(t *testing.T) {
	m := typeInto(newPumModel(t), "@lin")

	require.Equal(t, []string{"林岚"}, pumNames(m))
}

// A group's roster now carries its bots, so the badge that says which of them
// is a machine reaches the popup.
func TestPum_OffersABotWithItsBadge(t *testing.T) {
	m := typeInto(newPumModel(t), "@gjjqr")

	require.Equal(t, []string{"构建机器人"}, pumNames(m))
	require.Contains(t, m.pum.hits[0].label, botBadge)
}

func TestPum_ClosesWhenNobodyAnswers(t *testing.T) {
	m := typeInto(newPumModel(t), "@zzzz")
	require.False(t, m.pum.open())
	require.Equal(t, "@zzzz", m.input.Value(), "what was typed stays in the draft")
}

func TestPum_AcceptingAMentionWritesThePlainNameAndRemembersWho(t *testing.T) {
	m := typeInto(newPumModel(t), "@zs")
	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = mm.(Model)

	require.Equal(t, "@张三 ", m.input.Value())
	require.Equal(t, map[string]string{"张三": "ou_a"}, m.picked)
	require.False(t, m.pum.open())
}

func TestPum_AcceptingAtAllRemembersNobody(t *testing.T) {
	m := typeInto(newPumModel(t), "@")
	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(Model)

	require.Equal(t, "@All ", m.input.Value())
	require.Empty(t, m.picked, "@All names nobody, so there is nothing to remember")
}

func TestPum_AcceptingMidDraftLeavesTheTailAlone(t *testing.T) {
	m := newPumModel(t)
	m.input.SetValue("好的 @zs 你看下")
	// The cursor sits at the end of the run, not at the end of the draft.
	m.input.SetCursorColumn(len([]rune("好的 @zs")))
	m.takePum()
	require.True(t, m.pum.open())

	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, "好的 @张三  你看下", mm.(Model).input.Value())
}

func TestPum_AcceptingAnEmojiWritesTheCharacterWhereThereIsOne(t *testing.T) {
	m := typeInto(newPumModel(t), ":dianzan")
	require.True(t, m.pum.open())
	require.Equal(t, "👍", m.pum.hits[0].insert)

	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, "👍 ", mm.(Model).input.Value())
}

func TestPum_AcceptingAnEmojiWithNoCharacterWritesTheBracketedName(t *testing.T) {
	m := typeInto(newPumModel(t), ":done")
	require.True(t, m.pum.open())

	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = mm.(Model)
	require.Equal(t, "[完成] ", m.input.Value(), "the spelling the client itself sends")
	// The message list reads the bracketed form back as the emoji it names.
	_, ok := emojiByBracket(m.input.Value())
	require.True(t, ok)
}

func TestPum_StaysShutUntilTwoLettersStand(t *testing.T) {
	m := typeInto(newPumModel(t), ":")
	require.False(t, m.pum.open(), "a lone colon is punctuation")
	m = typeInto(m, "d")
	require.False(t, m.pum.open(), "one letter is still ambiguous")
	m = typeInto(m, "o")
	require.True(t, m.pum.open())
}

func TestPum_OpensOnTheBracketedFormTheClientSends(t *testing.T) {
	m := typeInto(newPumModel(t), "[wancheng")
	require.True(t, m.pum.open())

	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = mm.(Model)
	require.Equal(t, "[完成] ", m.input.Value(), "the bracket is erased with the rest of the run")

	// The bracket is a second way in, not a second insert rule: an emoji a
	// character carries still goes in as that character.
	m = typeInto(newPumModel(t), "[dianzan")
	require.Equal(t, "👍", m.pum.hits[0].insert)
}

func TestPum_OffersTheUnicodeEmojiFeishuHasNoAnswerFor(t *testing.T) {
	m := typeInto(newPumModel(t), ":rocket")
	require.True(t, m.pum.open())

	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyTab})
	require.Equal(t, "🚀 ", mm.(Model).input.Value())
}

func TestPum_EnterAcceptsWhileOpenAndSendsOnceClosed(t *testing.T) {
	m := typeInto(newPumModel(t), "好的 @zs")
	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(Model)
	require.Empty(t, m.msgs, "Enter chose a name; it did not send")
	require.Equal(t, "好的 @张三 ", m.input.Value())

	mm, cmd := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Len(t, mm.(Model).msgs, 1, "with the popup closed Enter sends again")
}

func TestPum_EscDismissesWithoutLeavingInsertMode(t *testing.T) {
	m := typeInto(newPumModel(t), "@zs")
	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = mm.(Model)

	require.False(t, m.pum.open())
	require.Equal(t, modeInsert, m.mode)
	require.Equal(t, "@zs", m.input.Value(), "the run stays as the text it is")

	// Typing on does not bring back the popup the reader just dismissed.
	m = typeInto(m, "a")
	require.False(t, m.pum.open())

	// A second Esc leaves insert mode, the way it always has.
	mm, _ = m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	require.Equal(t, modeNormal, mm.(Model).mode)
}

func TestPum_MovingKeepsItsPlaceWhileTheRunStands(t *testing.T) {
	m := typeInto(newPumModel(t), "@")
	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: 'n', Mod: tea.ModCtrl})
	m = mm.(Model)
	require.Equal(t, 1, m.pum.idx)

	// Re-reading an unchanged run must not throw the cursor back to the top.
	m.takePum()
	require.Equal(t, 1, m.pum.idx)

	mm, _ = m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "@林岚 ", mm.(Model).input.Value())
}

func TestComposerHeight_GrowsByThePopupAndKeepsThePanesFloor(t *testing.T) {
	m := typeInto(newPumModel(t), "@")
	require.Equal(t, len(m.pum.hits), m.composerRows().pum)
	require.Equal(t, len(m.pum.hits), len(m.pumLines(m.width-2)))

	// The offers outrun the cap; the band does not.
	m = typeInto(newPumModel(t), ":ha")
	require.Greater(t, len(m.pum.hits), pumMaxRows)
	require.Equal(t, pumMaxRows, m.composerRows().pum)

	// A terminal with nothing to spare keeps the message panes their floor and
	// draws no popup at all.
	m.height = minHeight
	m.layout()
	require.Zero(t, m.composerRows().pum)
	require.GreaterOrEqual(t, m.listHeight(), 1)
}

func TestPum_TakesNoKeysOnATerminalWithNoRoomToDrawIt(t *testing.T) {
	m := typeInto(newPumModel(t), ":do")
	require.True(t, m.pumShowing())

	m.height = minHeight
	m.layout()
	require.Zero(t, m.composerRows().pum)
	require.False(t, m.pumShowing(), "nothing is drawn, so nothing may be chosen from")

	// Enter sends, the way it does whenever no popup is on screen.
	mm, cmd := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Len(t, mm.(Model).msgs, 1)
	require.NotContains(t, m.renderBadge(m.width-2), pumHint)
}

func TestRenderInput_BoxIsAsTallAsItClaimsWithThePopupOpen(t *testing.T) {
	m := typeInto(newPumModel(t), "@")
	require.Equal(t, m.composerHeight()+2, strings.Count(m.renderInput(), "\n")+1)
}

func TestModelPicturePrepare_ClaimsWhatTheOpenPopupOffers(t *testing.T) {
	m := typeInto(newPumModel(t), ":ha")
	require.NotEmpty(t, m.pumVisible())
	require.LessOrEqual(t, len(m.pumVisible()), pumMaxRows)
	for i, h := range m.pumVisible() {
		require.Equal(t, m.pum.hits[m.pum.top+i], h, "the renderer and the picture pass see the same offers")
	}
}

// pumNames is what the popup is offering, in order.
func pumNames(m Model) []string {
	out := make([]string, 0, len(m.pum.hits))
	for _, h := range m.pum.hits {
		out = append(out, h.name)
	}
	return out
}

// emojiByBracket reads a draft's `[Name]` back the way the message list does.
func emojiByBracket(draft string) (string, bool) {
	g := bracketName.FindStringSubmatch(draft)
	if g == nil {
		return "", false
	}
	return g[1], true
}

package tui

import (
	"image"
	"image/png"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
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
	require.Contains(t, m.pum.menu.rows[0].name, botBadge)
}

func TestPum_ClosesWhenNobodyAnswers(t *testing.T) {
	m := typeInto(newPumModel(t), "@zzzz")
	require.False(t, m.pum.open())
	require.Equal(t, "@zzzz", m.input.Value(), "what was typed stays in the draft")
}

func TestPum_AcceptingAMentionWritesThePlainNameAndRemembersWho(t *testing.T) {
	m := typeInto(newPumModel(t), "@zs")
	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(Model)

	require.Equal(t, "@张三 ", m.input.Value())
	require.Equal(t, map[string]string{"张三": "ou_a"}, m.picked)
	require.False(t, m.pum.open())
}

func TestPum_TabAndShiftTabWalkRatherThanAccept(t *testing.T) {
	m := typeInto(newPumModel(t), "@")
	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyTab})
	m = mm.(Model)
	require.True(t, m.pum.open(), "tab walks the offers, it does not accept")
	require.Equal(t, 1, m.pum.menu.idx)
	mm, _ = m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyTab, Mod: tea.ModShift})
	m = mm.(Model)
	require.Zero(t, m.pum.menu.idx, "shift+tab walks back")
}

func TestPum_CtrlYAccepts(t *testing.T) {
	m := typeInto(newPumModel(t), "@zs")
	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: 'y', Mod: tea.ModCtrl})
	m = mm.(Model)
	require.Equal(t, "@张三 ", m.input.Value())
	require.False(t, m.pum.open())
}

func TestForward_APasteInTheComposerRereadsThePopup(t *testing.T) {
	m := typeInto(newPumModel(t), "@zs")
	require.True(t, m.pum.open())
	m.deps.Clipboard = func(string) (clip, error) { return clip{kind: clipText, text: " 你好"}, nil }
	mm, cmd := m.Update(tea.PasteMsg{Content: " 你好"})
	require.NotNil(t, cmd)
	mm, _ = mm.(Model).Update(cmd())
	m = mm.(Model)
	require.False(t, m.pum.open(), "the run the popup offered for is behind the cursor now")
	mm, _ = m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotContains(t, mm.(Model).input.Value(), "张三", "Enter sends rather than accepting over the paste")

	m = newPumModel(t)
	m.deps.Clipboard = func(string) (clip, error) { return clip{kind: clipText, text: "@zs"}, nil }
	mm, cmd = m.Update(tea.PasteMsg{Content: "@zs"})
	require.NotNil(t, cmd)
	mm, _ = mm.(Model).Update(cmd())
	require.True(t, mm.(Model).pum.open(), "a pasted trigger offers what a typed one does")
}

func TestPastedMsg_AClipboardPasteRereadsThePopup(t *testing.T) {
	m := typeInto(newPumModel(t), "@zs")
	mm, _ := m.Update(pastedMsg{clip: clip{kind: clipText, text: " 你好"}})
	m = mm.(Model)
	require.Equal(t, "@zs 你好", m.input.Value())
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

	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "好的 @张三  你看下", mm.(Model).input.Value())
}

func TestPum_AcceptingAnEmojiWithNoCharacterWritesTheBracketedName(t *testing.T) {
	m := typeInto(newPumModel(t), ":done")
	require.True(t, m.pum.open())

	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(Model)
	require.Equal(t, "[Done] ", m.input.Value(), "the spelling the client itself sends")
	// The message list reads the bracketed form back as the emoji it names.
	require.True(t, emojiByBracket.MatchString(m.input.Value()),
		"a bracketed spelling is there to read")
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

	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	m = mm.(Model)
	require.Equal(t, "[Done] ", m.input.Value(), "the bracket is erased with the rest of the run")

	// The bracket is a second way in, not a second insert rule: both spellings
	// go in as the bracketed name the client itself sends.
	m = typeInto(newPumModel(t), "[dianzan")
	require.Equal(t, "[Like]", m.pum.menu.items[0].insert)
}

// The emoji terms and the pinyin of a name are all lowercase, so the smart case
// a picker reads a query with would turn the capital of `[Do` into a query
// nothing answers. The popup completes what is being typed mid-sentence, where
// the capital is the shift still held from the bracket, not a request.
func TestPum_MatchesWhateverCaseTheQueryIsTypedIn(t *testing.T) {
	for _, run := range []string{"[Do", ":Do"} {
		m := typeInto(newPumModel(t), run)
		require.True(t, slices.ContainsFunc(m.pum.menu.items, func(h pumHit) bool { return h.insert == "[Done]" }), run)
	}
	m := typeInto(newPumModel(t), "[DONE")
	require.Equal(t, "[Done]", m.pum.menu.items[0].insert)
	m = typeInto(newPumModel(t), "@Zs")
	require.Equal(t, []string{"张三"}, pumNames(m))
}

func TestPum_OffersTheUnicodeEmojiFeishuHasNoAnswerFor(t *testing.T) {
	m := typeInto(newPumModel(t), ":rocket")
	require.True(t, m.pum.open())
	require.NotContains(t, pumInfo(m), larkMark, "a character is not one of Lark's own")

	mm, _ := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
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
	require.Equal(t, 1, m.pum.menu.idx)

	// Re-reading an unchanged run must not throw the cursor back to the top.
	m.takePum()
	require.Equal(t, 1, m.pum.menu.idx)

	mm, _ = m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.Equal(t, "@林岚 ", mm.(Model).input.Value())
}

func TestPumRows_HoldThePopupToThePaneItCovers(t *testing.T) {
	m := typeInto(newPumModel(t), "@")
	require.Equal(t, len(m.pum.menu.items), m.pumRows())
	require.Len(t, m.floatSegs(), m.pumRows())

	// The offers outrun the cap; the popup does not.
	m = typeInto(newPumModel(t), ":ha")
	require.Greater(t, len(m.pum.menu.items), pumMaxRows)
	require.Equal(t, pumMaxRows, m.pumRows())

	// A terminal with nothing to spare keeps the message panes their floor and
	// draws no popup at all.
	m.height = minHeight
	m.layout()
	require.Zero(t, m.pumRows())
	require.GreaterOrEqual(t, m.listHeight(), 1)
}

func TestPum_TakesNoKeysOnATerminalWithNoRoomToDrawIt(t *testing.T) {
	m := typeInto(newPumModel(t), ":do")
	require.True(t, m.pumShowing())

	m.height = minHeight
	m.layout()
	require.Zero(t, m.pumRows())
	require.False(t, m.pumShowing(), "nothing is drawn, so nothing may be chosen from")

	// Enter sends, the way it does whenever no popup is on screen.
	mm, cmd := m.onInsertKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	require.NotNil(t, cmd)
	require.Len(t, mm.(Model).msgs, 1)
	require.NotContains(t, m.renderBadge(m.width-2), pumHint)
}

func TestRenderInput_BoxStandsStillWhenThePopupOpens(t *testing.T) {
	m := newPumModel(t)
	before, body := m.composerRows(), m.bodyHeight()
	m = typeInto(m, "@")
	require.True(t, m.pum.open())
	require.Equal(t, before, m.composerRows(), "the popup buys no rows off the box")
	require.Equal(t, body, m.bodyHeight(), "so the panes above it do not move")
	require.Equal(t, m.composerHeight()+2, strings.Count(m.renderInput(sideMain), "\n")+1)
}

func TestModelPicturePrepare_ClaimsWhatTheOpenPopupOffers(t *testing.T) {
	m := newPumModel(t)
	writeTestEmoji(t, m.deps.DataDir, "DONE")
	m.pics = picturesIn(m.deps.DataDir)
	m = typeInto(m, ":done")
	require.Equal(t, "DONE", m.pum.menu.items[0].emoji.Emoji.Key, "Done is drawn as a word, which no character carries")

	require.NotEmpty(t, m.picturePrepare())
	pic := m.floatSegs()[0][0].pic
	require.Positive(t, pic.cols, "the row draws the client's picture")
	require.NotEmpty(t, m.pics.cells(pic, 0), "and it is drawable on the frame the popup opens")
}

// offerText is one of the popup's rows as the reader sees it, drawn the width
// the popup would give it.
func offerText(m Model, i int, selected bool) string {
	segs := m.offerRow(m.pum.menu.rows[i], m.pum.menu.cols, i, selected)
	return ansi.Strip(m.joinSegs(segs, segsWidth(segs)))
}

// pumInfo is what the box beside the popup says about the focused offer.
func pumInfo(m Model) []string {
	v, _ := m.floatMenu()
	out := make([]string, 0, len(v.info))
	for _, l := range v.info {
		out = append(out, ansi.Strip(l))
	}
	return out
}

// pumNames is what the popup is offering, in order.
func pumNames(m Model) []string {
	out := make([]string, 0, len(m.pum.menu.items))
	for _, h := range m.pum.menu.items {
		out = append(out, h.name)
	}
	return out
}

// emojiByBracket reads a draft's `[Name]` back the way the message list does.
var emojiByBracket = regexp.MustCompile(`^\[([^\[\]\n]{1,12})\]`)

func TestOfferSegs_SayALetteringEmojisNameOnce(t *testing.T) {
	m := newPumModel(t)
	m = typeInto(m, ":yes")
	i := slices.IndexFunc(m.pum.menu.items, func(h pumHit) bool { return h.emoji.Emoji.Key == "Yes" })
	require.GreaterOrEqual(t, i, 0)
	m.pum.menu.idx = i
	line := offerText(m, i, true) + strings.Join(pumInfo(m), " ")
	require.Equal(t, 1, strings.Count(strings.ToLower(line), "yes"), "line=%q", line)
}

func TestPum_AnEmojisKeyAndTermAreItsInfoNotItsName(t *testing.T) {
	m := typeInto(newPumModel(t), ":dianzan")
	require.Equal(t, "THUMBSUP", m.pum.menu.items[0].emoji.Emoji.Key)
	require.Equal(t, "Like", ansi.Strip(m.pum.menu.rows[0].name))
	require.Equal(t, []string{"THUMBSUP", "dianzan", larkMark}, pumInfo(m), "the key, then the pinyin that answered")
}

func TestPum_ALarkBuiltInEmojiSaysLarkInTheBox(t *testing.T) {
	m := typeInto(newPumModel(t), ":dianzan")
	require.Equal(t, "THUMBSUP", m.pum.menu.items[0].emoji.Emoji.Key)
	require.Contains(t, pumInfo(m), larkMark, "one of Lark's own is marked; a character is not")
}

func TestPum_APersonsInfoIsDepartmentThenEmail(t *testing.T) {
	m := newPumModel(t)
	m.roster[1].Department = "平台组"
	m.roster[1].Email = "zhangsan@example.com"
	m = typeInto(m, "@zs")
	require.Equal(t, "张三", ansi.Strip(m.pum.menu.rows[0].name), "the row is the name alone")
	require.Equal(t, []string{"平台组", "zhangsan@example.com"}, pumInfo(m))

	// Somebody with neither has nothing more to say, and no box opens.
	m = typeInto(newPumModel(t), "@ls")
	require.Empty(t, pumInfo(m))
	f, ok := m.floater()
	require.True(t, ok, "a bracketed spelling is there to read")
	_, ok = m.infoFloater(f)
	require.False(t, ok)
}

// writeTestAvatar puts a square picture where a contact's avatar would be
// downloaded to, and returns the path the store records for it.
func writeTestAvatar(t *testing.T, dataDir, id string) string {
	t.Helper()
	rel := filepath.Join("resources", "avatars", id+".png")
	require.NoError(t, os.MkdirAll(filepath.Join(dataDir, filepath.Dir(rel)), 0o700))
	f, err := os.Create(filepath.Join(dataDir, rel))
	require.NoError(t, err)
	defer f.Close()
	require.NoError(t, png.Encode(f, image.NewRGBA(image.Rect(0, 0, 64, 64))))
	return rel
}

func TestPum_AMentionWearsItsFace(t *testing.T) {
	// Without graphics the face is the colour block the chat list stands in
	// with, so the column is there either way.
	m := typeInto(newPumModel(t), "@zs")
	require.True(t, m.pum.menu.cols.icon)
	require.Equal(t, "张 张三", strings.TrimRight(offerText(m, 0, false), " "))

	m = newPumModel(t)
	m.roster[1].AvatarPath = writeTestAvatar(t, m.deps.DataDir, "ou_a")
	m.pics = picturesIn(m.deps.DataDir)
	m = typeInto(m, "@zs")
	pic := m.floatSegs()[0][0].pic
	require.Positive(t, pic.cols, "the avatar is drawn as a picture")
	require.True(t, pic.disc, "cut to the client's circle")
	require.NotEmpty(t, m.picturePrepare())
	require.NotEmpty(t, m.pics.cells(pic, 0), "and claimed on the frame the popup opens")
}

func TestPum_AtAllWearsTheGroupsFace(t *testing.T) {
	m := newPumModel(t)
	m.chats[0].AvatarPath = writeTestAvatar(t, m.deps.DataDir, "oc_group")
	m = typeInto(m, "@")
	require.Equal(t, allKey, m.pum.menu.items[0].id)
	icon := m.pum.menu.rows[0].icon
	require.True(t, icon.avatar)
	require.Equal(t, "oc_group", icon.id)
	require.Equal(t, m.chats[0].AvatarPath, icon.image)
}

func TestPum_AnEmojiListHasNoFaces(t *testing.T) {
	m := typeInto(newPumModel(t), ":dianzan")
	icon := m.pum.menu.rows[0].icon
	require.False(t, icon.avatar)
	require.Equal(t, "emoji/THUMBSUP.png", icon.image, "the icon is the client's own picture")
}

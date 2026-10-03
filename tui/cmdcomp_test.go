package tui

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// cmdModel is a model on the : line, over a couple of chats and a colleague
// larkim has no chat with yet.
func cmdModel(t *testing.T) Model {
	t.Helper()
	m := pickerModel(t)
	m.chats = []store.Chat{
		{ChatID: "oc_team", Name: "平台组", ChatMode: "group"},
		{ChatID: "oc_proj", Name: "项目协作群", ChatMode: "group"},
	}
	m.contacts = []store.Contact{{OpenID: "ou_a", Name: "张三"}}
	m.layout()
	return press(t, m, ":")
}

// typed is the : line as the reader sees it.
func typed(m Model) string { return m.cmdline.Value() }

// offers is the names the list's rows show, with their styling taken off.
func offers(m Model) []string {
	out := make([]string, 0, len(m.cmdcomp.menu.rows))
	for _, o := range m.cmdcomp.menu.rows {
		out = append(out, ansi.Strip(o.name))
	}
	return out
}

// focusedInfo is what the box beside the list says, with its styling taken off.
func focusedInfo(m Model) []string {
	v, _ := m.floatMenu()
	out := make([]string, 0, len(v.info))
	for _, l := range v.info {
		out = append(out, ansi.Strip(l))
	}
	return out
}

func TestCmdComp_OpensOnTheFirstRuneAndNotOnABareColon(t *testing.T) {
	t.Parallel()
	m := cmdModel(t)
	require.Equal(t, modeCommand, m.mode)
	require.False(t, m.cmdcomp.open(), "a bare : is a bare :, not the whole table")
	require.Equal(t, restingComposer, m.composerHeight())

	m = press(t, m, "c")
	require.True(t, m.cmdcomp.open())
	require.Equal(t, -1, m.cmdcomp.menu.idx, "the line is still what the reader typed")
}

func TestCmdComp_OffersEveryCommandAPrefixReaches(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c")

	// goto is reached through its alias chat, and is offered under the name
	// the line will carry.
	require.Equal(t, []string{"copy", "goto", "candidates", "config"}, offers(m))
}

func TestCmdComp_UsageAndHelpAreTheFocusedRowsInfoNotItsName(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c")
	require.Empty(t, focusedInfo(m), "nothing is focused until the reader walks onto a row")

	m = press(t, m, "tab")
	require.Equal(t, []string{":copy <200|7d|all>", "put that much of the chat on the clipboard as agent context"}, focusedInfo(m))

	// A command that takes nothing has only its help to say.
	m = press(t, m, "ctrl+n", "ctrl+n")
	require.Equal(t, "candidates", typed(m))
	require.Equal(t, []string{"pick a pending lark-watch reply draft into the composer"}, focusedInfo(m))
}

func TestCmdComp_ATargetsIDIsItsInfo(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "s", "e", "n", "d", " ")
	require.Equal(t, []string{"平台组", "项目协作群", "张三"}, offers(m), "the rows name; the id they write is not on them")

	m = press(t, m, "tab")
	require.Equal(t, []string{"oc_team"}, focusedInfo(m))

	// :goto gets the id too, though it writes the name.
	m = press(t, cmdModel(t), "g", "o", "t", "o", " ", "tab")
	require.Equal(t, []string{"oc_team"}, focusedInfo(m))
}

func TestCmdComp_ASettingsInfoIsItsHelpValueAndReach(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "s", "e", "t", " ", "tab")
	require.Equal(t, "set applink_pace_ms=", typed(m))
	info := focusedInfo(m)
	require.Len(t, info, 2)
	require.Contains(t, info[1], "takes effect now", ":set reaches only live keys")
}

func TestCmdComp_AnEnumWordHasNothingMoreToSay(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c", "o", "p", "y", " ", "tab")
	require.Equal(t, "copy 200", typed(m))
	require.Empty(t, focusedInfo(m))
	f, ok := m.floater()
	require.True(t, ok)
	_, ok = m.infoFloater(f)
	require.False(t, ok, "no box opens on an empty answer")
}

func TestCmdComp_WalkingWritesTheOfferIntoTheLine(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c")
	require.Equal(t, "c", typed(m))

	m = press(t, m, "tab")
	require.Equal(t, "copy", typed(m))
	require.Equal(t, 0, m.cmdcomp.menu.idx)

	m = press(t, m, "ctrl+n")
	require.Equal(t, "goto", typed(m))
	require.Equal(t, 4, m.cmdline.Position(), "the caret follows what was written")
}

func TestCmdComp_WalkingBackPastTheTopRestoresWhatWasTyped(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c", "tab", "ctrl+p")

	require.Equal(t, "c", typed(m), "the stem is the only way back off a list that overwrote it")
	require.Equal(t, -1, m.cmdcomp.menu.idx)
	require.True(t, m.cmdcomp.open(), "the offers stand: walking back is not dismissing")
}

func TestCmdComp_KeepsItsOffersWhileWalking(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c")
	want := offers(m)

	m = press(t, m, "tab")

	require.Equal(t, want, offers(m), "rewriting the line must not refilter it to the one row just written")
}

func TestCmdComp_ClosesOnceTheLineNamesOneCommandWithNoArgument(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "s", "y", "n", "c")
	require.Equal(t, []string{"sync"}, offers(m))

	m = press(t, m, " ")
	require.False(t, m.cmdcomp.open(), ":sync takes nothing, so there is nothing to offer")
}

func TestCmdComp_CompletesAChatNameForGotoAndAnOpenIDForSend(t *testing.T) {
	t.Parallel()
	// :goto reads its whole rest and folds it, so it takes the readable name.
	m := press(t, cmdModel(t), "g", "o", "t", "o", " ")
	require.Equal(t, []string{"平台组", "项目协作群"}, offers(m))
	m = press(t, m, "tab")
	require.Equal(t, "goto 平台组", typed(m))

	// :send splits its target from its text on the first space, so a name is
	// not a target it can take.
	m = press(t, cmdModel(t), "s", "e", "n", "d", " ", "tab")
	require.Equal(t, "send oc_team", typed(m))

	// A colleague with no chat yet is still somewhere a message can go.
	m = press(t, cmdModel(t), "s", "e", "n", "d", " ", "张", "tab")
	require.Equal(t, "send ou_a", typed(m))
}

func TestCmdComp_OffersOnlyChatsToGoto(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "g", "o", " ", "张")

	require.Empty(t, offers(m), "a person is not a chat the reader can be taken to")
}

func TestCmdComp_CompletesAnEmojiKeyForReact(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "r", "e", "a", "c", "t", " ", "z", "a", "n")
	require.NotEmpty(t, m.cmdcomp.menu.items, "the pinyin reaches the emoji the picker reaches")
	require.Equal(t, "THUMBSUP", m.cmdcomp.menu.items[0].insert)

	// What the list writes has to be what :react reads back, or completing a
	// name would react with something else.
	m = press(t, m, "tab")
	require.Equal(t, "react THUMBSUP", typed(m))
	for _, h := range m.cmdcomp.menu.items {
		_, ok := m.emoji.ByKey(h.insert)
		require.True(t, ok, "%s is not a key :react reads back", h.insert)
	}
}

func TestCmdComp_CompletesTheFixedWordsOfAnEnumArgument(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c", "o", "p", "y", " ")
	require.Equal(t, []string{"200", "7d", "all"}, offers(m))

	m = press(t, cmdModel(t), "a", "i", " ", "d")
	require.Equal(t, []string{"draft"}, offers(m))
}

func TestCmdComp_StopsPastTheFirstArgument(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "s", "e", "n", "d", " ", "tab", " ", "h", "i")

	require.Equal(t, "send oc_team hi", typed(m))
	require.False(t, m.cmdcomp.open(), "past the first argument the line is prose")
}

func TestCmdComp_EscClosesTheListThenLeavesCommandMode(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c", "esc")
	require.False(t, m.cmdcomp.open())
	require.Equal(t, modeCommand, m.mode, "dismissing a menu and leaving the line are two presses")
	require.Equal(t, "c", typed(m))

	m = press(t, m, "o")
	require.False(t, m.cmdcomp.open(), "typing on down the same line leaves it dismissed")

	m = press(t, m, "esc")
	require.Equal(t, modeNormal, m.mode)
	require.Empty(t, typed(m))
}

func TestCmdComp_TheColonLineStandsStillAsTheListGrows(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c")
	one := m.cmdCompRows()
	require.Positive(t, one)
	at := m.View().Cursor
	box := m.composerRows()

	m = press(t, m, "backspace", "r", "e", "a", "c", "t", " ")
	require.Greater(t, m.cmdCompRows(), one, "an emoji list is longer than two commands")
	require.Equal(t, at.Y, m.View().Cursor.Y, "the line the reader types on does not move")
	require.Equal(t, box, m.composerRows(), "and the list buys no rows off the box")
	require.Equal(t, m.composerHeight()+2, lipgloss.Height(m.renderInput(sideMain)), "the box is exactly as tall as it claims")
}

func TestRunCommand_RunsWhatTheListWroteIntoTheLine(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c", "tab", "enter")

	require.Equal(t, modeNormal, m.mode)
	require.False(t, strings.Contains(m.notice, "unknown"), "notice: %s", m.notice)
	require.False(t, strings.Contains(m.notice, "ambiguous"), "notice: %s", m.notice)
}

func TestCmdComp_CompletesASettingWithItsEqualsAlready(t *testing.T) {
	t.Parallel()
	// An option only ever reads as a pair, so the = comes along and the
	// reader's next keystroke is the value.
	m := press(t, cmdModel(t), "s", "e", "t", " ")
	require.Equal(t, []string{"applink_pace_ms", "mark_read.mode", "mark_read.browser", "ai.agent", "ai.model", "ai.context",
		"ai.jev_key_env", "ai.jev_endpoint", "ai.history", "todoist.token", "todoist.project_id"}, offers(m),
		":set reaches only the keys a change takes effect on, which with a daemon owning the sweep excludes the sweep's own")

	m = press(t, m, "tab")
	require.Equal(t, "set applink_pace_ms=", typed(m))
}

func TestCmdComp_CompletesAModeWithTheWordsItTakes(t *testing.T) {
	t.Parallel()
	// mark_read.mode takes one of two words; offering them is what spares the
	// reader a round trip through the refusal.
	m := press(t, cmdModel(t), []string{"s", "e", "t", " ", "m", "a", "r", "k", "_", "r", "e", "a", "d", ".", "m", "o", "d", "e", "="}...)
	require.Equal(t, []string{"applink", "web"}, offers(m))

	m = press(t, m, "w", "tab")
	require.Equal(t, "set mark_read.mode=web", typed(m))
}

func TestCmdComp_CompletesAConfigKeyBare(t *testing.T) {
	t.Parallel()
	// :config opens the panel on the row; the value is typed into the row
	// rather than into the line, so no = comes along.
	m := press(t, cmdModel(t), "c", "o", "n", "f", "i", "g", " ")
	require.Equal(t, config.Keys(), offers(m), "every key of the file, not just the live ones")

	m = press(t, m, "tab")
	require.Equal(t, "config data_dir", typed(m))
}

func TestFloater_StandsUnderTheFieldOnTheCommandLine(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c")
	f, ok := m.floater()
	require.True(t, ok)
	// The command being completed is the line's first field, one column past
	// the : the line opens with, and the box's border sits left of that.
	require.Equal(t, m.bandLeft(m.cmdSide())+1+lipgloss.Width(m.cmdline.Prompt)-1, f.x)

	// An argument stands further along, and the box follows the field rather
	// than the caret the list keeps moving.
	m = press(t, cmdModel(t), "r", "e", "a", "c", "t", " ")
	g, ok := m.floater()
	require.True(t, ok)
	require.Greater(t, g.x, f.x)
	require.Equal(t, len("react "), g.x-f.x)
}

func TestFloater_FollowsTheFieldNotTheLineTheListWrites(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c")
	f, _ := m.floater()

	m = press(t, m, "tab")
	require.GreaterOrEqual(t, m.cmdcomp.menu.idx, 0, "the list has written into the line")
	g, ok := m.floater()
	require.True(t, ok)
	require.Equal(t, f.x, g.x, "the box stands where the field does, not where the caret went")
}

func TestCursorAt_StaysOnTheCommandLinesOwnRow(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "c")
	require.True(t, m.cmdcomp.open())
	// The first inner row of the box: the panes with their border, then the
	// box's own top border.
	require.Equal(t, m.bodyHeight()+3, m.View().Cursor.Y)
}

func TestModelPicturePrepare_ClaimsWhatTheReactListOffers(t *testing.T) {
	t.Parallel()
	m := cmdModel(t)
	writeTestEmoji(t, m.deps.DataDir, "DONE")
	m.pics = picturesIn(m.deps.DataDir)
	m = press(t, m, "r", "e", "a", "c", "t", " ", "d", "o", "n", "e")
	require.Equal(t, "DONE", m.cmdcomp.menu.items[0].insert)

	require.NotEmpty(t, m.picturePrepare())
	pic := m.floatSegs()[0][0].pic
	require.Positive(t, pic.cols, "a picture-only emoji draws the client's picture")
	require.NotEmpty(t, m.pics.cells(pic, 0), "and the : line's list claims it, as the composer's popup does")
}

func TestCmdComp_ATargetWearsItsFace(t *testing.T) {
	t.Parallel()
	m := press(t, cmdModel(t), "s", "e", "n", "d", " ")
	require.True(t, m.cmdcomp.menu.cols.icon)
	chat := m.cmdcomp.menu.rows[0].icon
	require.True(t, chat.avatar)
	require.Equal(t, "oc_team", chat.id, "a group is coloured by its own id")
	person := m.cmdcomp.menu.rows[2].icon
	require.Equal(t, "ou_a", person.id, "a colleague with no chat yet, by theirs")

	// A list of words has nothing to wear.
	m = press(t, cmdModel(t), "c")
	require.False(t, m.cmdcomp.menu.cols.icon)
}

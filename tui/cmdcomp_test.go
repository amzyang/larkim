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

// offers is what the list is holding, with its styling taken off.
func offers(m Model) []string {
	out := make([]string, 0, len(m.cmdcomp.hits))
	for _, h := range m.cmdcomp.hits {
		out = append(out, ansi.Strip(h.label))
	}
	return out
}

func TestCmdComp_OpensOnTheFirstRuneAndNotOnABareColon(t *testing.T) {
	m := cmdModel(t)
	require.Equal(t, modeCommand, m.mode)
	require.False(t, m.cmdcomp.open(), "a bare : is a bare :, not the whole table")
	require.Equal(t, restingComposer, m.composerHeight())

	m = press(t, m, "c")
	require.True(t, m.cmdcomp.open())
	require.Equal(t, -1, m.cmdcomp.idx, "the line is still what the reader typed")
}

func TestCmdComp_OffersEveryCommandAPrefixReaches(t *testing.T) {
	m := press(t, cmdModel(t), "c")

	// goto is reached through its alias chat, and is offered under the name
	// the line will carry.
	require.Equal(t, []string{"copy <200|7d|all>", "goto <chat>", "config [<key>]"}, offers(m))
}

func TestCmdComp_WalkingWritesTheOfferIntoTheLine(t *testing.T) {
	m := press(t, cmdModel(t), "c")
	require.Equal(t, "c", typed(m))

	m = press(t, m, "tab")
	require.Equal(t, "copy", typed(m))
	require.Equal(t, 0, m.cmdcomp.idx)

	m = press(t, m, "ctrl+n")
	require.Equal(t, "goto", typed(m))
	require.Equal(t, 4, m.cmdline.Position(), "the caret follows what was written")
}

func TestCmdComp_WalkingBackPastTheTopRestoresWhatWasTyped(t *testing.T) {
	m := press(t, cmdModel(t), "c", "tab", "ctrl+p")

	require.Equal(t, "c", typed(m), "the stem is the only way back off a list that overwrote it")
	require.Equal(t, -1, m.cmdcomp.idx)
	require.True(t, m.cmdcomp.open(), "the offers stand: walking back is not dismissing")
}

func TestCmdComp_KeepsItsOffersWhileWalking(t *testing.T) {
	m := press(t, cmdModel(t), "c")
	want := offers(m)

	m = press(t, m, "tab")

	require.Equal(t, want, offers(m), "rewriting the line must not refilter it to the one row just written")
}

func TestCmdComp_ClosesOnceTheLineNamesOneCommandWithNoArgument(t *testing.T) {
	m := press(t, cmdModel(t), "s", "y", "n", "c")
	require.Equal(t, []string{"sync"}, offers(m))

	m = press(t, m, " ")
	require.False(t, m.cmdcomp.open(), ":sync takes nothing, so there is nothing to offer")
}

func TestCmdComp_CompletesAChatNameForGotoAndAnOpenIDForSend(t *testing.T) {
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
	m := press(t, cmdModel(t), "g", "o", " ", "张")

	require.Empty(t, offers(m), "a person is not a chat the reader can be taken to")
}

func TestCmdComp_CompletesAnEmojiKeyForReact(t *testing.T) {
	m := press(t, cmdModel(t), "r", "e", "a", "c", "t", " ", "z", "a", "n")
	require.NotEmpty(t, m.cmdcomp.hits, "the pinyin reaches the emoji the picker reaches")
	require.Equal(t, "THUMBSUP", m.cmdcomp.hits[0].insert)

	// What the list writes has to be what :react reads back, or completing a
	// name would react with something else.
	m = press(t, m, "tab")
	require.Equal(t, "react THUMBSUP", typed(m))
	for _, h := range m.cmdcomp.hits {
		_, ok := m.emoji.ByKey(h.insert)
		require.True(t, ok, "%s is not a key :react reads back", h.insert)
	}
}

func TestCmdComp_CompletesTheFixedWordsOfAnEnumArgument(t *testing.T) {
	m := press(t, cmdModel(t), "c", "o", "p", "y", " ")
	require.Equal(t, []string{"200", "7d", "all"}, offers(m))

	m = press(t, cmdModel(t), "a", "i", " ", "d")
	require.Equal(t, []string{"draft"}, offers(m))
}

func TestCmdComp_StopsPastTheFirstArgument(t *testing.T) {
	m := press(t, cmdModel(t), "s", "e", "n", "d", " ", "tab", " ", "h", "i")

	require.Equal(t, "send oc_team hi", typed(m))
	require.False(t, m.cmdcomp.open(), "past the first argument the line is prose")
}

func TestCmdComp_EscClosesTheListThenLeavesCommandMode(t *testing.T) {
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
	m := press(t, cmdModel(t), "c")
	one := m.composerRows().pum
	require.Positive(t, one)
	at := m.View().Cursor

	m = press(t, m, "backspace", "r", "e", "a", "c", "t", " ")
	require.Greater(t, m.composerRows().pum, one, "an emoji list is longer than two commands")
	require.Equal(t, at.Y, m.View().Cursor.Y, "the line the reader types on does not move")
	require.Equal(t, m.composerHeight()+2, lipgloss.Height(m.renderInput()), "the box is exactly as tall as it claims")
}

func TestRunCommand_RunsWhatTheListWroteIntoTheLine(t *testing.T) {
	m := press(t, cmdModel(t), "c", "tab", "enter")

	require.Equal(t, modeNormal, m.mode)
	require.False(t, strings.Contains(m.notice, "unknown"), "notice: %s", m.notice)
	require.False(t, strings.Contains(m.notice, "ambiguous"), "notice: %s", m.notice)
}

func TestCmdComp_CompletesASettingWithItsEqualsAlready(t *testing.T) {
	// An option only ever reads as a pair, so the = comes along and the
	// reader's next keystroke is the value.
	m := press(t, cmdModel(t), "s", "e", "t", " ")
	require.Equal(t, []string{"applink_pace_ms", "ai.model", "ai.api_key_env", "ai.context"}, offers(m),
		":set reaches only the keys a change takes effect on")

	m = press(t, m, "tab")
	require.Equal(t, "set applink_pace_ms=", typed(m))
}

func TestCmdComp_CompletesAConfigKeyBare(t *testing.T) {
	// :config opens the panel on the row; the value is typed into the row
	// rather than into the line, so no = comes along.
	m := press(t, cmdModel(t), "c", "o", "n", "f", "i", "g", " ")
	require.Equal(t, config.Keys(), offers(m), "every key of the file, not just the live ones")

	m = press(t, m, "tab")
	require.Equal(t, "config data_dir", typed(m))
}

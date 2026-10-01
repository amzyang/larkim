package tui

import (
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// pickerModel is a model over a real store holding one chat with one message
// the reader has already put 👍 on.
func pickerModel(t *testing.T) Model {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	require.NoError(t, st.EnsureChat(ctx, "oc_team", 1))
	_, err = st.UpsertMessages(ctx, []store.Message{{MessageID: "om_a", ChatID: "oc_team", MsgType: "text",
		SenderID: "ou_a", SenderName: "张三", ContentRaw: `{"text":"下周一发版"}`, CreateMs: 100, UpdateMs: 100}}, 1)
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(ctx, "om_a", "下周一发版", "", 2))
	require.NoError(t, st.UpdateReactions(ctx, "om_a",
		`{"counts":[{"reaction_type":"THUMBSUP","count":"1"}],"details":[{"emoji_type":"THUMBSUP","operator":{"operator_id":"ou_me"}}]}`))

	m := New(Deps{Store: st, Self: "ou_me", DataDir: t.TempDir(), Syncer: &sync.Syncer{Store: st},
		Config: config.Default()})
	m.width, m.height = 120, 36
	m.layout()
	m.chats, _ = st.ListChats(ctx, store.ChatQuery{Limit: 10})
	m.msgsBase, _ = st.ListMessages(ctx, store.MessageQuery{ChatID: "oc_team", Limit: 10})
	m.chatID = "oc_team"
	m.applyOutbox()
	m.focus = paneMessages
	m.msgIdx = 0
	m.layout()
	return m
}

func press(t *testing.T, m Model, keys ...string) Model {
	t.Helper()
	for _, k := range keys {
		next, _ := m.onKey(keyMsg(k))
		m = next.(Model)
	}
	return m
}

// paste delivers s the way a bracketed paste from the terminal arrives.
func paste(t *testing.T, m Model, s string) Model {
	t.Helper()
	next, _ := m.Update(tea.PasteMsg{Content: s})
	return next.(Model)
}

// keyMsg spells a key the way the terminal delivers it under the kitty
// protocol: modifiers ride on Mod rather than folding into another key, and
// only an unmodified character carries text.
func keyMsg(name string) tea.KeyPressMsg {
	k := tea.KeyPressMsg{}
	for {
		if rest, ok := strings.CutPrefix(name, "ctrl+"); ok {
			k.Mod |= tea.ModCtrl
			name = rest
		} else if rest, ok := strings.CutPrefix(name, "alt+"); ok {
			k.Mod |= tea.ModAlt
			name = rest
		} else if rest, ok := strings.CutPrefix(name, "shift+"); ok {
			k.Mod |= tea.ModShift
			name = rest
		} else {
			break
		}
	}
	k.Code = keyCode(name)
	if k.Mod == 0 && len([]rune(name)) == 1 {
		k.Text = name
	}
	return k
}

func keyCode(name string) rune {
	switch name {
	case "enter":
		return tea.KeyEnter
	case "esc":
		return tea.KeyEscape
	case "up":
		return tea.KeyUp
	case "down":
		return tea.KeyDown
	case "backspace":
		return tea.KeyBackspace
	case "tab":
		return tea.KeyTab
	case "left":
		return tea.KeyLeft
	case "right":
		return tea.KeyRight
	}
	return []rune(name)[0]
}

// reactKey is the emoji the chooser's offer at i names.
func reactKey(m Model, i int) string { return m.picker.menu.items[i].hit.Emoji.Key }

func TestOpenPicker_ArmsAgainstTheSelectedMessage(t *testing.T) {
	m := press(t, pickerModel(t), "e")
	require.Equal(t, modeEmoji, m.mode)
	require.Equal(t, "om_a", m.picker.target.MessageID)
	require.NotEmpty(t, m.picker.menu.items, "an empty query offers the client's own panel order")
	require.True(t, m.picker.mine["THUMBSUP"], "the reader's own reaction is known before they choose")
}

func TestPicker_FiltersAsTheReaderTypes(t *testing.T) {
	m := press(t, pickerModel(t), "e", "z", "a", "n")
	require.Equal(t, "zan", m.picker.input.Value())
	require.Equal(t, "THUMBSUP", reactKey(m, 0))
	require.Less(t, len(m.picker.menu.items), m.emoji.Len(), "the list narrows")

	m = press(t, m, "backspace", "backspace", "backspace")
	require.Equal(t, "", m.picker.input.Value())
	require.Len(t, m.picker.menu.items, m.emoji.Len(), "and opens back up")
}

func TestPicker_TakesTheCharacterItselfAsTheQuery(t *testing.T) {
	// The reader has the emoji in the clipboard, not its name; pasting it is
	// one keystroke against a spelling they would have to guess.
	m := press(t, pickerModel(t), "e", "🌹")
	require.Equal(t, "ROSE", reactKey(m, 0))

	// A character of more than one rune arrives as a paste rather than as
	// keys, which the filter takes the same way it takes anything else.
	mm, _ := press(t, pickerModel(t), "e").Update(tea.PasteMsg{Content: "❤️"})
	require.Equal(t, "HEART", reactKey(mm.(Model), 0))
}

func TestReactRow_DrawsTheCharacterItselfOnce(t *testing.T) {
	m := press(t, pickerModel(t), "e", "🌹")
	require.Len(t, m.picker.menu.items, 1)

	// The character is already in the icon column, so the row does not name
	// it a second time as the term that answered.
	line := ansi.Strip(m.joinSegsWidth(m.offerRow(m.picker.menu.rows[0], m.picker.menu.cols, 0, true)))
	require.Equal(t, 1, strings.Count(line, "🌹"), line)
}

func TestPicker_OffersNothingForACharacterFeishuDrawsItsOwnWay(t *testing.T) {
	// Feishu's 看 is a face looking sideways, not 👀, so it keeps its picture and
	// the character reaches no reaction at all — the Unicode one is a
	// composer emoji Feishu would refuse.
	m := press(t, pickerModel(t), "e", "👀")
	require.Empty(t, m.picker.menu.items)
}

func TestPicker_MovesOnArrowsBecauseTheQueryOwnsTheLetters(t *testing.T) {
	m := press(t, pickerModel(t), "e")
	first := reactKey(m, 0)
	m = press(t, m, "down")
	require.Equal(t, 1, m.picker.menu.idx, "down moves one offer")
	require.NotEqual(t, first, reactKey(m, m.picker.menu.idx))

	// j is a letter, so it filters rather than moving.
	m = press(t, m, "j")
	require.Equal(t, "j", m.picker.input.Value())
	require.Zero(t, m.picker.menu.idx, "a new query puts the cursor back on the best hit")
}

func TestPicker_EscLeavesWithoutReacting(t *testing.T) {
	m := press(t, pickerModel(t), "e", "z")
	m = press(t, m, "esc")
	require.Equal(t, modeNormal, m.mode)
	require.Zero(t, m.picker.target.MessageID, "nothing of the chooser is left behind")
}

func TestPicker_RemembersWhatWasChosen(t *testing.T) {
	m := press(t, pickerModel(t), "e", "m", "e", "i", "g", "u", "i")
	require.Equal(t, "ROSE", reactKey(m, 0))
	m = press(t, m, "enter")
	require.Equal(t, modeNormal, m.mode)
	require.Equal(t, []string{"ROSE"}, m.emoji.Used())
}

func TestPicker_ADigitPicksTheRowItIsDrawnBeside(t *testing.T) {
	m := press(t, pickerModel(t), "e", "z", "a", "n")
	require.Equal(t, "THUMBSUP", reactKey(m, 0))
	m = press(t, m, "1")
	require.Equal(t, modeNormal, m.mode)
	require.Len(t, m.reacts, 1)
	require.Equal(t, "THUMBSUP", m.reacts[0].emojiType)
}

func TestPicker_OpensOnTheSmallestTerminalTheClientDraws(t *testing.T) {
	// The chooser's own box is the composer's, so any terminal that can write
	// a message can offer an emoji.
	m := pickerModel(t)
	m.height = minHeight
	m.layout()
	m = press(t, m, "e")
	require.Equal(t, modeEmoji, m.mode)
	require.Equal(t, m.height, lipgloss.Height(m.View().Content))
}

func TestPicker_SurvivesAResizeBelowWhatTheClientDraws(t *testing.T) {
	// Nothing closes the chooser when the terminal shrinks under the size the
	// client draws at, and the picture pass runs at any height.
	m := press(t, pickerModel(t), "e")
	require.Equal(t, modeEmoji, m.mode)
	next, _ := m.Update(tea.WindowSizeMsg{Width: 120, Height: 8})
	shrunk := next.(Model)
	require.Len(t, shrunk.picker.menu.view(shrunk.reactRows()).rows, shrunk.reactRows())
	require.NotPanics(t, func() { shrunk.picturePrepare() })
	require.Contains(t, ansi.Strip(shrunk.View().Content), "terminal too small")
}

func TestPicker_TheListScrollsWithTheCursor(t *testing.T) {
	m := press(t, pickerModel(t), "e")
	rows := m.reactRows()
	require.Positive(t, rows)
	second := reactKey(m, 1)

	// One row past the bottom scrolls the list by exactly one row.
	for range rows {
		m = press(t, m, "down")
	}
	require.Equal(t, 1, m.picker.menu.top)
	require.Equal(t, second, reactKey(m, m.picker.menu.top), "what was the second offer opens the list")
}

func TestPicker_LeavesTheMessageOnScreenBehindIt(t *testing.T) {
	// The reader has to see what they are reacting to, so the offers stand
	// over the panes and cost the composer's box nothing.
	m := press(t, pickerModel(t), "e")
	require.GreaterOrEqual(t, m.bodyHeight(), minListRows)
	require.Contains(t, ansi.Strip(m.View().Content), "下周一发版")
	require.Contains(t, ansi.Strip(m.View().Content), "react", "the frame names what is being reacted to")
}

func TestRenderPicker_StandsInTheComposersBoxRatherThanBesideIt(t *testing.T) {
	shut := pickerModel(t)
	open := press(t, shut, "e")
	require.Equal(t, lipgloss.Width(shut.renderInput(shut.side)), lipgloss.Width(open.renderPicker()),
		"the chooser replaces the composer, so it takes the same columns")
	require.Equal(t, lipgloss.Height(shut.renderInput(shut.side)), lipgloss.Height(open.renderPicker()),
		"and exactly the same rows, so nothing above it moves")
	require.Equal(t, shut.bodyHeight(), open.bodyHeight(), "the panes keep their height")
	for i, line := range strings.Split(open.View().Content, "\n") {
		require.Equal(t, open.width, lipgloss.Width(line), "line %d is not the screen's width", i)
	}
}

func TestModelPicturePrepare_ClaimsWhatTheOpenPickerOffers(t *testing.T) {
	m := press(t, pickerModel(t), "e")
	writeTestEmoji(t, m.deps.DataDir, "OK")
	m.pics = picturesIn(m.deps.DataDir)
	require.Equal(t, "OK", reactKey(m, 0), "an empty query opens on the client's own first emoji")

	require.NotEmpty(t, m.picturePrepare())
	// The claim comes from the pass the list draws through, so the first
	// offer's picture is drawable on the frame the chooser opens.
	var pic picture
	for _, seg := range m.floatSegs()[0] {
		if seg.pic.cols > 0 {
			pic = seg.pic
		}
	}
	require.Positive(t, pic.cols)
	require.NotEmpty(t, m.pics.cells(pic, 0), "the emoji is drawable on the frame the chooser opens")
}

func TestPicker_MarksAnEmojiTheReaderAlreadyChose(t *testing.T) {
	m := press(t, pickerModel(t), "e", "z", "a", "n")
	require.Contains(t, ansi.Strip(m.picker.menu.rows[0].name), "✓",
		"choosing it again takes the reaction back, and the row says so")
}

func TestRunReact_TogglesOffWhatTheReaderAlreadyChose(t *testing.T) {
	m := pickerModel(t)
	next, cmd := m.runCommand("react zan")
	require.NotNil(t, cmd)
	require.Equal(t, []string{"THUMBSUP"}, next.(Model).emoji.Used(),
		"naming it by pinyin reaches the same emoji the chooser would")
}

func TestRunReact_SaysWhatIsWrongRatherThanReactingWithTheFirstThingItFinds(t *testing.T) {
	m := pickerModel(t)
	for _, tc := range []struct{ arg, want string }{
		{"", "usage:"},
		{"zzzzqqqq", "no emoji matches"},
	} {
		next, cmd := m.runCommand("react " + tc.arg)
		require.Nil(t, cmd, "%q sends nothing", tc.arg)
		require.Contains(t, next.(Model).notice, tc.want)
	}
}

func TestOpenPicker_TakesAMessageWhoseBodyIsNotRenderedYet(t *testing.T) {
	// A reaction reaches a message by id alone, so waiting on the rendering
	// would refuse a message Feishu already holds.
	m := pickerModel(t)
	m.msgs[0].RenderedAt = 0
	m = press(t, m, "e")
	require.Equal(t, modeEmoji, m.mode)
}

func TestOpenPicker_RefusesASendStillOnItsWay(t *testing.T) {
	m := pickerModel(t)
	m.enqueue(outboxItem{localID: "local_1", chatID: "oc_team", msgType: "text", body: "稍等", createMs: 200})
	m.applyOutbox()
	m.msgIdx = len(m.msgs) - 1
	require.Equal(t, "local_1", m.msgs[m.msgIdx].MessageID)

	m = press(t, m, "e")
	require.Equal(t, modeNormal, m.mode)
	require.Contains(t, m.notice, "not reached Feishu")
}

func TestStatus_NamesThePickerWhileItIsOpen(t *testing.T) {
	m := press(t, pickerModel(t), "e")
	require.Contains(t, fmtStatus(m), "REACT", "the mode line says which keys are live")
}

func TestPicker_FilterErasesTheWayReadlineDoes(t *testing.T) {
	m := press(t, pickerModel(t), "e", "z", "a", "n")
	m = press(t, m, "ctrl+h")
	require.Equal(t, "za", m.picker.input.Value(), "the kitty protocol tells ctrl+h from backspace, and both erase a rune")

	m = press(t, m, "ctrl+w")
	require.Equal(t, "", m.picker.input.Value(), "a filter is one word, so erasing the word empties it")
	require.Len(t, m.picker.menu.items, m.emoji.Len(), "and the list opens back up")

	m = press(t, m, "z", "a", "n", "ctrl+a", "ctrl+d")
	require.Equal(t, "an", m.picker.input.Value(), "the cursor moves, it does not only sit at the end")

	m = press(t, m, "ctrl+e", "ctrl+u")
	require.Equal(t, "", m.picker.input.Value(), "ctrl+u erases back from the cursor")
}

func TestView_PanesStandStillWhateverModeTheReaderIsIn(t *testing.T) {
	// The bottom box is the same height in every mode, so pressing i, e or :
	// moves nothing above it.
	m := pickerModel(t)
	body := m.bodyHeight()
	for _, k := range []string{"i", "esc", "e", "esc", ":", "esc", "/", "esc"} {
		m = press(t, m, k)
		require.Equal(t, body, m.bodyHeight(), "%q moved the panes", k)
		require.Equal(t, m.height, lipgloss.Height(m.View().Content), "%q left the screen a different height", k)
	}
	require.Equal(t, modeNormal, m.mode)
}

// chipAt presses the chip carrying key on the strip the messages pane draws,
// in the coordinates the terminal reports a click in.
func chipAt(t *testing.T, m Model, key string) (Model, tea.Cmd) {
	t.Helper()
	for line, r := range m.msgRows {
		for _, z := range r.zones {
			if z.react != key {
				continue
			}
			next, cmd := m.onClick(tea.Mouse{Button: tea.MouseLeft,
				X: chatsWidth + 1 + z.x0, Y: line - m.msgTop + 1 + msgHeaderHeight})
			return next.(Model), cmd
		}
	}
	t.Fatalf("no %s chip on the page", key)
	return m, nil
}

func TestPressChip_DrawsThePressBeforeItIsSent(t *testing.T) {
	m := pickerModel(t)
	before := rowText(m.msgRows)
	require.Contains(t, before, "👍⋮You", "the reader already reacted, which is what a press takes back")
	m, cmd := chipAt(t, m, "THUMBSUP")
	require.NotNil(t, cmd)
	require.Len(t, m.reacts, 1)
	require.False(t, m.reacts[0].on, "pressing what the reader already put takes it back")
	require.NotContains(t, rowText(m.msgRows), "👍", "the strip answers the press, not the round trip")
}

func TestPressChip_SecondPressGoesTheOtherWay(t *testing.T) {
	m := pickerModel(t)
	m, _ = chipAt(t, m, "THUMBSUP")
	require.False(t, m.reacts[0].on)
	// The chip is gone with the reaction, so the second press comes through
	// the chooser the way a reader would reach an emoji nothing carries.
	next, cmd := m.toggleReaction(m.msgs[0], "THUMBSUP")
	m = next.(Model)
	require.NotNil(t, cmd)
	require.Len(t, m.reacts, 1, "the last press is what the reader means")
	require.True(t, m.reacts[0].on, "the direction answers the strip the first press left")
	require.Contains(t, rowText(m.msgRows), "👍⋮You")
}

func TestPressChip_LeavesTheCursorWhereItWas(t *testing.T) {
	m := pickerModel(t)
	m.msgIdx = 0
	m, _ = chipAt(t, m, "THUMBSUP")
	require.Equal(t, 0, m.msgIdx, "a press asked for the chip, not for the message under it")
}

func TestReactedMsg_TakesAFailedPressBackOffTheStrip(t *testing.T) {
	m := pickerModel(t)
	m, _ = chipAt(t, m, "THUMBSUP")
	require.NotContains(t, rowText(m.msgRows), "👍")
	next, _ := m.update(reactedMsg{p: m.reacts[0], err: errors.New("no network")})
	m = next.(Model)
	require.Empty(t, m.reacts)
	require.Contains(t, rowText(m.msgRows), "👍⋮You", "the strip goes back to what Feishu holds")
	require.Contains(t, m.notice, "no network")
}

func TestReactedMsg_AsksForTheSummaryRatherThanWaitingOnARevision(t *testing.T) {
	m := pickerModel(t)
	m, _ = chipAt(t, m, "THUMBSUP")
	next, cmd := m.update(reactedMsg{p: m.reacts[0]})
	m = next.(Model)
	require.NotNil(t, cmd, "an answer that changed nothing bumps no revision to reload on")
	require.Len(t, m.reacts, 1,
		"taking it off now would flicker back to the summary the reload is about to replace")
	require.True(t, m.reacts[0].answered)
}

func TestReactedMsg_StopsDrawingARemovalFeishuTookWithoutChangingAnything(t *testing.T) {
	// Taking back a reaction Feishu no longer holds answers without moving the
	// summary. The press must still go when the reload lands, or the strip
	// keeps showing a reaction that is not there until the window runs out.
	m := pickerModel(t)
	m, _ = chipAt(t, m, "THUMBSUP")
	require.NotContains(t, rowText(m.msgRows), "👍")
	next, _ := m.update(reactedMsg{p: m.reacts[0]})
	m = next.(Model)
	m.applyOutbox()
	m.layout()
	require.Empty(t, m.reacts)
	require.Contains(t, rowText(m.msgRows), "👍⋮You", "the strip goes back to what Feishu holds")
}

func TestReactedMsg_RollsBackOnlyThePressThatFailed(t *testing.T) {
	m := pickerModel(t)
	m, _ = chipAt(t, m, "THUMBSUP")
	failed := m.reacts[0]
	next, _ := m.toggleReaction(m.msgs[0], "THUMBSUP")
	m = next.(Model)
	next, _ = m.update(reactedMsg{p: failed, err: errors.New("no network")})
	m = next.(Model)
	require.Len(t, m.reacts, 1, "the press that failed is not the press now standing")
	require.True(t, m.reacts[0].on)
}

func TestOpenPicker_MarksWhatAPressAlreadyPut(t *testing.T) {
	m := pickerModel(t)
	// Taking the reaction back means the chooser must stop marking it, even
	// though the store still says the reader has it.
	m, _ = chipAt(t, m, "THUMBSUP")
	m = press(t, m, "e")
	require.False(t, m.picker.mine["THUMBSUP"], "the tick follows the strip, not the summary behind it")
}

func TestReactRow_SaysALetteringEmojisNameOnce(t *testing.T) {
	// OK, Yes, No and OKR are spelled as their own name, and the picture in
	// the icon column spells it again; the row must not.
	m := press(t, pickerModel(t), "e", "y", "e", "s")
	i := slices.IndexFunc(m.picker.menu.items, func(h reactHit) bool { return h.hit.Emoji.Key == "Yes" })
	require.GreaterOrEqual(t, i, 0)
	require.Equal(t, 1, strings.Count(strings.ToLower(ansi.Strip(m.picker.menu.rows[i].name)), "yes"),
		"row=%q", m.picker.menu.rows[i].name)
}

func TestReactInfo_StillNamesTheKeyAndThePinyinThatReachedIt(t *testing.T) {
	m := press(t, pickerModel(t), "e", "d", "z")
	require.Equal(t, "THUMBSUP", reactKey(m, 0))
	info := ansi.Strip(strings.Join(m.picker.menu.view(m.reactRows()).info, "\n"))
	require.Contains(t, info, "THUMBSUP", "the key is what :react takes")
	require.Contains(t, info, "dz", "and the initials say why this hit came back")
}

func TestRenderPicker_NamesTheBandOnlyWhenItIsTheReadersOwn(t *testing.T) {
	m := press(t, pickerModel(t), "e")
	require.NotContains(t, ansi.Strip(m.renderPicker()), "frequently used",
		"nothing reached for yet: the lead is the client's panel order, not this reader's habits")

	m.emoji.Use("THUMBSUP")
	m.pickerGrid()
	require.Contains(t, ansi.Strip(m.renderPicker()), "frequently used")

	m = press(t, m, "z", "a", "n")
	require.NotContains(t, ansi.Strip(m.renderPicker()), "frequently used",
		"a narrowed query answers with what it matched, and the count says how much")
}

// withdrawnKey is an emoji the client took out of the reaction panel. Feishu
// answers a reaction with it "reaction type is invalid", so the chooser sends
// it as a picture instead.
const withdrawnKey = "GOODJOB"

// pictureModel is pickerModel with a Feishu to send to and the withdrawn
// emoji's picture already cut out, which is what emoji.Ensure leaves behind on
// a real start.
func pictureModel(t *testing.T) (Model, *larkcli.Fake) {
	t.Helper()
	m := pickerModel(t)
	f := larkcli.NewFake()
	m.deps.Client = f
	require.NoError(t, os.MkdirAll(emoji.Dir(m.deps.DataDir), 0o700))
	require.NoError(t, os.WriteFile(emoji.Path(m.deps.DataDir, withdrawnKey), []byte("png"), 0o600))
	return m, f
}

func TestToggleReaction_SendsAWithdrawnEmojiAsAPictureInstead(t *testing.T) {
	m, f := pictureModel(t)
	next, cmd := m.toggleReaction(m.msgs[0], withdrawnKey)
	m = next.(Model)
	require.NotNil(t, cmd)
	require.Empty(t, m.reacts, "nothing was put on the message; a picture answers it instead")
	require.Len(t, m.outbox, 1)
	require.Equal(t, "image", m.outbox[0].msgType)
	require.Equal(t, "om_a", m.outbox[0].replyTo, "the picture answers the message the press was aimed at")
	require.Equal(t, emoji.Path(m.deps.DataDir, withdrawnKey), m.outbox[0].images[0].local)
	require.Contains(t, m.notice, "GoodJob")

	cmd() // the send itself
	require.Equal(t, []string{emoji.Path(m.deps.DataDir, withdrawnKey)}, f.Uploads)
}

func TestToggleReaction_DrawsTheOutgoingPictureUnderTheMessage(t *testing.T) {
	m, _ := pictureModel(t)
	next, _ := m.toggleReaction(m.msgs[0], withdrawnKey)
	m = next.(Model)
	require.Len(t, m.msgs, 2, "the bubble stands in the chat while the picture is on its way")
	require.Equal(t, m.outbox[0].localID, m.msgs[1].MessageID)
}

func TestToggleReaction_StillTakesBackAWithdrawnEmojiAlreadyOnTheMessage(t *testing.T) {
	// Somebody reacted with it before the client withdrew it. Taking one's own
	// back is a reaction, not a picture.
	m, _ := pictureModel(t)
	require.NoError(t, m.deps.Store.UpdateReactions(t.Context(), "om_a",
		`{"counts":[{"reaction_type":"GOODJOB","count":"1"}],`+
			`"details":[{"emoji_type":"GOODJOB","operator":{"operator_id":"ou_me"}}]}`))
	m.msgsBase, _ = m.deps.Store.ListMessages(t.Context(), store.MessageQuery{ChatID: "oc_team", Limit: 10})
	m.applyOutbox()
	next, cmd := m.toggleReaction(m.msgs[0], withdrawnKey)
	m = next.(Model)
	require.NotNil(t, cmd)
	require.Empty(t, m.outbox, "nothing is sent; the reaction is taken back")
	require.Len(t, m.reacts, 1)
	require.False(t, m.reacts[0].on)
}

func TestReactRow_SaysWhichEmojiGoInAsAPicture(t *testing.T) {
	m := press(t, pickerModel(t), "e")
	m = paste(t, m, "给力")
	require.NotEmpty(t, m.picker.menu.items)
	require.Equal(t, withdrawnKey, reactKey(m, 0), "the chooser still finds it")
	require.Contains(t, ansi.Strip(m.picker.menu.rows[0].name), "pic",
		"pressing enter sends a message, not a reaction, and the row says so")
}

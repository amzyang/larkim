package tui

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/jev"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// fakeSuggest answers every question the same way and keeps what it was asked.
type fakeSuggest struct {
	rank  jev.Rank
	err   error
	asked []jev.Ask
}

func (f *fakeSuggest) Rank(_ context.Context, a jev.Ask) (jev.Rank, error) {
	f.asked = append(f.asked, a)
	return f.rank, f.err
}

// openSuggest opens the picker with a suggester behind it and hands back the
// command that asks the question, unrun.
func openSuggest(t *testing.T, f *fakeSuggest) (Model, tea.Cmd) {
	t.Helper()
	m := pickerModel(t)
	m.suggester = f
	next, cmd := m.openPicker()
	return next.(Model), cmd
}

// answer runs cmd and feeds every suggestedMsg it yields back into the model,
// walking a batch to find them.
func answer(t *testing.T, m Model, cmd tea.Cmd) Model {
	t.Helper()
	if cmd == nil {
		return m
	}
	var msgs []tea.Msg
	switch v := cmd().(type) {
	case tea.BatchMsg:
		for _, c := range v {
			if c != nil {
				msgs = append(msgs, c())
			}
		}
	default:
		msgs = append(msgs, v)
	}
	for _, msg := range msgs {
		if s, ok := msg.(suggestedMsg); ok {
			next, _ := m.Update(s)
			m = next.(Model)
		}
	}
	return m
}

// ready is an answer with four emoji over the floor and one under it, so the
// cases can tell a cell the answer chose from one filled out of the reader's
// own order.
var ready = jev.Rank{Fits: 0.9, Options: []jev.Option{
	{Key: "DONE", P: 0.5}, {Key: "THUMBSUP", P: 0.2}, {Key: "APPLAUSE", P: 0.1},
	{Key: "OK", P: 0.06}, {Key: "SMILE", P: suggestFloor - 0.01},
}}

// marked is what ready puts in the rows, in order.
var marked = []string{"DONE", "THUMBSUP", "APPLAUSE", "OK"}

func TestPicker_WithoutASuggesterHasNoRow(t *testing.T) {
	t.Parallel()
	m := press(t, pickerModel(t), "e")
	require.Equal(t, suggestOff, m.picker.suggest)
	require.Equal(t, reactHits(m.emoji.Search(""), 0, m.picker.mine), m.picker.menu.items, "the list is the one it has always been")
}

func TestPicker_OpensOnTheGridItHasAlwaysOpenedOn(t *testing.T) {
	t.Parallel()
	m, _ := openSuggest(t, &fakeSuggest{rank: ready})
	require.Equal(t, suggestWaiting, m.picker.suggest)
	require.Equal(t, reactHits(m.emoji.Search(""), 0, m.picker.mine), m.picker.menu.items, "nothing is reserved while the answer is on its way")
	require.Equal(t, "jev…", m.picker.suggestNote(), "the query line is what says an answer is coming")
}

func TestPicker_TheHeadRowsCostTheBoxNothing(t *testing.T) {
	t.Parallel()
	plain := press(t, pickerModel(t), "e")
	m, _ := openSuggest(t, &fakeSuggest{rank: ready})
	require.Equal(t, plain.composerHeight(), m.composerHeight(),
		"the head of the list stands over the panes, not in the box")
}

func TestPicker_TheAnswerLeadsTheList(t *testing.T) {
	t.Parallel()
	m, cmd := openSuggest(t, &fakeSuggest{rank: ready})
	m = answer(t, m, cmd)
	require.Equal(t, suggestReady, m.picker.suggest)
	for i, key := range marked {
		require.Equal(t, key, reactKey(m, i))
		require.True(t, m.picker.menu.items[i].byAsk, key+" is one the conversation chose")
	}
	require.Empty(t, m.picker.suggestNote(), "a head that speaks for itself needs no word on the line")
}

func TestPicker_APickUnderTheFloorIsNotOffered(t *testing.T) {
	t.Parallel()
	m, cmd := openSuggest(t, &fakeSuggest{rank: ready})
	m = answer(t, m, cmd)
	n := 0
	for _, it := range m.picker.menu.items {
		if it.byAsk {
			n++
		}
	}
	require.Equal(t, len(marked), n, "SMILE came in under the floor")
	require.False(t, m.picker.menu.items[len(marked)].byAsk)
}

func TestPicker_TheRestOfTheRowsComeFromTheReadersOwnOrder(t *testing.T) {
	t.Parallel()
	m, cmd := openSuggest(t, &fakeSuggest{rank: ready})
	m = answer(t, m, cmd)
	// Every cell is filled — an empty one costs the grid a place for nothing —
	// and the ones past the answer's own are the order the picker opened with.
	for i := range suggestPicks {
		require.NotEmpty(t, reactKey(m, i))
	}
	own := slices.DeleteFunc(m.emoji.Search(""), func(h emoji.Hit) bool {
		return slices.Contains(marked, h.Emoji.Key)
	})
	for i, h := range own[:suggestPicks-len(marked)] {
		require.Equal(t, h.Emoji.Key, reactKey(m, len(marked)+i))
	}
}

func TestPicker_TheRowsEmojiComeOutOfTheGridUnderThem(t *testing.T) {
	t.Parallel()
	m, cmd := openSuggest(t, &fakeSuggest{rank: ready})
	m = answer(t, m, cmd)
	require.Len(t, m.picker.menu.items, m.picker.found, "as many out of the list as into the head rows")
	for _, key := range marked {
		n := 0
		for _, h := range m.picker.menu.items {
			if h.hit.Emoji.Key == key {
				n++
			}
		}
		require.Equal(t, 1, n, key+" stands in one place, not two")
	}
}

func TestPicker_TheCursorKeepsItsEmojiWhenTheAnswerLands(t *testing.T) {
	t.Parallel()
	m, cmd := openSuggest(t, &fakeSuggest{rank: ready})
	// Down two rows, which is an offer the emoji the answer pulls to the head
	// would otherwise shift out from under.
	m.picker.moved = true
	m.picker.menu.move(2, m.reactRows())
	was := m.pickerCursorKey()
	m = answer(t, m, cmd)
	require.Equal(t, was, m.pickerCursorKey())
}

func TestPicker_ChoosingFromTheRowReactsWithThatEmoji(t *testing.T) {
	t.Parallel()
	m, cmd := openSuggest(t, &fakeSuggest{rank: ready})
	m = answer(t, m, cmd)
	next, _ := m.chooseAt(m.picker.menu.idx)
	m = next.(Model)
	require.Equal(t, modeNormal, m.mode)
	require.Len(t, m.reacts, 1)
	require.Equal(t, "DONE", m.reacts[0].emojiType)
}

func TestPicker_AMessageNobodyWouldReactToGivesTheRowsLineBack(t *testing.T) {
	t.Parallel()
	m, cmd := openSuggest(t, &fakeSuggest{rank: jev.Rank{
		Options: []jev.Option{{Key: "DONE", P: 1}}, Fits: suggestFits - 0.1}})
	m = answer(t, m, cmd)
	require.Equal(t, suggestNone, m.picker.suggest)
	require.False(t, m.picker.rowOpen(), "nothing more is coming, so the list has the line")
	require.Equal(t, reactHits(m.emoji.Search(""), 0, m.picker.mine), m.picker.menu.items)
	require.Equal(t, "nothing to react to", m.picker.suggestNote())
}

func TestPicker_AFailedQuestionSaysSoInTheRowAndNowhereElse(t *testing.T) {
	t.Parallel()
	m, cmd := openSuggest(t, &fakeSuggest{err: errors.New("boom")})
	m = answer(t, m, cmd)
	require.Equal(t, suggestFailed, m.picker.suggest)
	require.False(t, m.picker.rowOpen(), "a head that will never fill keeps no line")
	require.Equal(t, reactHits(m.emoji.Search(""), 0, m.picker.mine), m.picker.menu.items)
	require.Empty(t, m.notice, "a best-effort row does not take the notification line")
	require.Empty(t, m.suggestCache, "the next press is worth another try")
}

func TestPicker_AnAnswerForAClosedPickerIsDropped(t *testing.T) {
	t.Parallel()
	m, cmd := openSuggest(t, &fakeSuggest{rank: ready})
	m = press(t, m, "esc")
	m = press(t, m, "e")
	m = answer(t, m, cmd)
	require.Equal(t, suggestWaiting, m.picker.suggest, "the second picker is still waiting on its own answer")
	require.Equal(t, reactHits(m.emoji.Search(""), 0, m.picker.mine), m.picker.menu.items)
}

func TestPicker_AnUntouchedCursorLandsOnTheBestPick(t *testing.T) {
	t.Parallel()
	m, cmd := openSuggest(t, &fakeSuggest{rank: ready})
	m = answer(t, m, cmd)
	require.Equal(t, 0, m.picker.menu.idx)
	require.Equal(t, "DONE", m.pickerCursorKey())
}

func TestPicker_ReopeningOnTheSameMessageAsksNothingAgain(t *testing.T) {
	t.Parallel()
	f := &fakeSuggest{rank: ready}
	m, cmd := openSuggest(t, f)
	m = answer(t, m, cmd)
	m = press(t, m, "esc")
	next, _ := m.openPicker()
	m = next.(Model)
	require.Equal(t, suggestReady, m.picker.suggest)
	require.Equal(t, "DONE", reactKey(m, 0))
	require.Len(t, f.asked, 1, "nothing is asked a second time")
}

func TestPicker_AQueryTakesTheRowAwayAndClearingItBringsItBack(t *testing.T) {
	t.Parallel()
	m, cmd := openSuggest(t, &fakeSuggest{rank: ready})
	m = answer(t, m, cmd)
	m = press(t, m, "z")
	require.Equal(t, m.emoji.Search("z")[0].Emoji.Key, reactKey(m, 0), "a narrowed list carries no head")
	require.False(t, m.picker.menu.items[0].byAsk)
	m = press(t, m, "backspace")
	require.Equal(t, "DONE", reactKey(m, 0))
	require.True(t, m.picker.menu.items[0].byAsk)
}

func TestStanding_NamesWhatIsAlreadyOnTheMessage(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	x, _ := m.selected()
	require.Equal(t, "Like / 赞 ×1 (mine already)", standing(m.drawnChips(x)))
	require.Equal(t, "none yet", standing(nil))
}

func TestAskSuggest_NamesBothSpellingsOfAnEmoji(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	x, _ := m.selected()
	opts := m.askSuggest(x).Options
	require.Equal(t, "Like / 赞", opts["THUMBSUP"], "the English name alone says nothing for half the set")
	require.Equal(t, "OK", opts["OK"], "one name spelled the same way twice is said once")
}

func TestPicker_TheRowMarksItselfApartFromTheGridUnderIt(t *testing.T) {
	t.Parallel()
	m, cmd := openSuggest(t, &fakeSuggest{rank: ready})
	m = answer(t, m, cmd)
	require.Contains(t, ansi.Strip(m.picker.menu.rows[0].name), "✦")
	pad := len(marked)
	require.NotContains(t, ansi.Strip(m.picker.menu.rows[pad].name), "✦",
		"a row filled from the reader's own order is not something the chat chose")
}

func TestAskSuggest_OffersWhatTheReaderReachesForAndOnlyReactions(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	x, ok := m.selected()
	require.True(t, ok)
	ask := m.askSuggest(x)
	require.Len(t, ask.Options, suggestOptions)
	for key := range ask.Options {
		e, ok := m.emoji.ByKey(key)
		require.True(t, ok, key)
		require.True(t, e.Reactable(), "a picture to reply with is not a reaction to offer")
	}
}

func TestAskSuggest_NamesWhoIsReactingAndWhoWrote(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	m.selfName = "林岚"
	x, _ := m.selected()
	state := m.askSuggest(x).State.(map[string]string)
	require.Equal(t, "林岚", state["me"])
	require.Equal(t, "张三", state["sender"])
}

func TestAskSuggest_NamesTheTargetApartFromTheConversation(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	x, _ := m.selected()
	state := m.askSuggest(x).State.(map[string]string)
	require.Contains(t, state["target"], "张三: 下周一发版")
	require.Equal(t, "Like / 赞 ×1 (mine already)", state["reactions"])
	require.NotContains(t, state["transcript"], "下周一发版",
		"the message being reacted to is not also one of the messages before it")
	require.True(t, strings.HasPrefix(state["transcript"], "chat: "))
}

func TestAskSuggest_APictureBringsItsWritingIntoTheState(t *testing.T) {
	t.Parallel()
	m := pickerModel(t)
	ctx := t.Context()
	st := m.deps.Store
	_, err := st.UpsertMessages(ctx, []store.Message{{MessageID: "om_shot", ChatID: "oc_team", MsgType: "image",
		SenderID: "ou_a", SenderName: "张三", ContentRaw: `{"image_key":"img_a"}`, CreateMs: 50, UpdateMs: 50}}, 1)
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(ctx, "om_shot", "[Image: img_a]", "", 2))
	m.msgsBase, _ = st.ListMessages(ctx, store.MessageQuery{ChatID: "oc_team", Limit: 10})
	m.applyOutbox()
	m.msgIdx = slices.IndexFunc(m.msgs, func(x store.Message) bool { return x.MessageID == "om_a" })
	m.meta.imgText = map[string][]string{"om_shot": {"回归用例全绿"}, "om_a": {"排期表"}}

	x, ok := m.selected()
	require.True(t, ok)
	state := m.askSuggest(x).State.(map[string]string)
	require.Contains(t, state["target"], "[image] 排期表")
	require.Contains(t, state["transcript"], "[image] 回归用例全绿",
		"the messages before the target bring their pictures too")
}

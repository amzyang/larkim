package tui

import (
	"encoding/json"
	"testing"
	"time"

	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/stretchr/testify/require"
)

func TestPendingChips_AddsTheReaderToAChipSomebodyElseOpened(t *testing.T) {
	chips := []emoji.Chip{{Key: "THUMBSUP", Count: 2, Operators: []string{"ou_a", "ou_b"}}}
	out := pendingChips(chips, map[string]bool{"THUMBSUP": true}, "ou_me")
	require.Equal(t, []emoji.Chip{{Key: "THUMBSUP", Count: 3, Mine: true,
		Operators: []string{"ou_a", "ou_b", "ou_me"}}}, out)
	require.Equal(t, 2, chips[0].Count, "the stored summary is left alone")
}

func TestPendingChips_TakesTheReaderBackOffAChipOthersKeep(t *testing.T) {
	chips := []emoji.Chip{{Key: "THUMBSUP", Count: 2, Mine: true, Operators: []string{"ou_a", "ou_me"}}}
	out := pendingChips(chips, map[string]bool{"THUMBSUP": false}, "ou_me")
	require.Equal(t, []emoji.Chip{{Key: "THUMBSUP", Count: 1, Operators: []string{"ou_a"}}}, out)
}

func TestPendingChips_ClosesAChipThePressEmptied(t *testing.T) {
	chips := []emoji.Chip{
		{Key: "THUMBSUP", Count: 1, Mine: true, Operators: []string{"ou_me"}},
		{Key: "OK", Count: 1, Operators: []string{"ou_a"}},
	}
	out := pendingChips(chips, map[string]bool{"THUMBSUP": false}, "ou_me")
	require.Equal(t, []emoji.Chip{{Key: "OK", Count: 1, Operators: []string{"ou_a"}}}, out,
		"the reader was the only one holding it up")
}

func TestPendingChips_OpensANewChipAtTheEndOfTheStrip(t *testing.T) {
	chips := []emoji.Chip{{Key: "THUMBSUP", Count: 1, Operators: []string{"ou_a"}}}
	out := pendingChips(chips, map[string]bool{"OK": true}, "ou_me")
	require.Len(t, out, 2)
	require.Equal(t, emoji.Chip{Key: "OK", Count: 1, Mine: true, Operators: []string{"ou_me"}}, out[1],
		"Feishu orders the strip by first use, so a brand new emoji lands last")
}

func TestPendingChips_LeavesAStripTheStoreAlreadyAgreesWith(t *testing.T) {
	chips := []emoji.Chip{{Key: "THUMBSUP", Count: 1, Mine: true, Operators: []string{"ou_me"}}}
	require.Equal(t, chips, pendingChips(chips, map[string]bool{"THUMBSUP": true}, "ou_me"))
	require.Equal(t, chips, pendingChips(chips, nil, "ou_me"))
}

func TestPendingChips_DrawsNothingForTakingBackAReactionThatIsNotThere(t *testing.T) {
	chips := []emoji.Chip{{Key: "THUMBSUP", Count: 1, Operators: []string{"ou_a"}}}
	require.Equal(t, chips, pendingChips(chips, map[string]bool{"OK": false}, "ou_me"))
}

func TestMineOn_MatchesTheWayFeishuSpellsTheKey(t *testing.T) {
	chips := []emoji.Chip{{Key: "Thumbsup", Count: 1, Mine: true}, {Key: "OK", Count: 2}}
	require.True(t, mineOn(chips, "THUMBSUP"), "a key is matched folded, the way the picker matches")
	require.False(t, mineOn(chips, "OK"), "somebody else's reaction is not the reader's")
	require.False(t, mineOn(chips, "ROSE"))
}

func TestSettleReacts_DropsThePressFeishuAnswered(t *testing.T) {
	now := time.Now()
	ps := []reactPending{
		{seq: 1, messageID: "om_a", key: "THUMBSUP", on: true, at: now, answered: true},
		{seq: 2, messageID: "om_a", key: "OK", on: true, at: now},
	}
	out := settleReacts(ps, now)
	require.Len(t, out, 1)
	require.EqualValues(t, 2, out[0].seq, "a press still on the wire keeps drawing")
}

func TestSettleReacts_DropsAnAnsweredPressTheSummaryDisagreesWith(t *testing.T) {
	// Taking back a reaction Feishu no longer holds is answered without the
	// summary moving. Holding the press against that would keep drawing a
	// reaction that is not there.
	now := time.Now()
	ps := []reactPending{{seq: 1, messageID: "om_a", key: "THUMBSUP", on: false, at: now, answered: true}}
	require.Empty(t, settleReacts(ps, now))
}

func TestSettleReacts_GivesUpOnAPressThatWasNeverAnswered(t *testing.T) {
	now := time.Now()
	ps := []reactPending{{seq: 1, messageID: "om_a", key: "THUMBSUP", on: false, at: now.Add(-2 * reactSettle)}}
	require.Empty(t, settleReacts(ps, now),
		"past the settle window the stored summary is the truth, disagreement and all")
}

func TestSettleReacts_HoldsAPressStillOnTheWire(t *testing.T) {
	now := time.Now()
	ps := []reactPending{{seq: 1, messageID: "om_elsewhere", key: "THUMBSUP", on: true, at: now}}
	require.Equal(t, ps, settleReacts(ps, now),
		"a reload that landed mid-flight says nothing about a press Feishu has not taken")
}

func TestPressReaction_ReplacesTheEarlierPressOnTheSameEmoji(t *testing.T) {
	m := Model{}
	m.deps.Self = "ou_me"
	x := store.Message{MessageID: "om_a"}
	first := m.pressReaction(x, "THUMBSUP")
	require.True(t, first.on)
	second := m.pressReaction(x, "thumbsup")
	require.False(t, second.on, "the direction answers the strip the first press already changed")
	require.Len(t, m.reacts, 1, "the last press is what the reader means")
	require.Equal(t, second.seq, m.reacts[0].seq)
	require.Greater(t, second.seq, first.seq)
}

func TestReactStates_KeysThePressesByMessageThenEmoji(t *testing.T) {
	m := Model{reacts: []reactPending{
		{messageID: "om_a", key: "THUMBSUP", on: true},
		{messageID: "om_a", key: "OK", on: false},
		{messageID: "om_b", key: "ROSE", on: true},
	}}
	require.Equal(t, map[string]map[string]bool{
		"om_a": {"THUMBSUP": true, "OK": false},
		"om_b": {"ROSE": true},
	}, m.reactStates())
	require.Nil(t, Model{}.reactStates())
}

// TestToggleReaction_SendsTheSpellingFeishuKnows pins the one thing folding
// must not touch. Most keys are upper case already, so a press that folds them
// reaches Feishu unharmed and only the two thirds that are not — Yes, No, Get,
// BubbleTea, every Status and General one — come back 231001.
func TestToggleReaction_SendsTheSpellingFeishuKnows(t *testing.T) {
	yes, ok := emoji.ByKey("Yes")
	require.True(t, ok)
	require.True(t, yes.Reactable(), "the picker offers it, so a press has to reach Feishu")

	f := larkcli.NewFake()
	f.Reactions["om_a"] = json.RawMessage(`{"counts":[]}`)
	m := pickerModel(t)
	m.deps.Syncer = &sync.Syncer{Store: m.deps.Store, Client: f}
	x, ok := m.selected()
	require.True(t, ok)

	next, cmd := m.toggleReaction(x, yes.Key)
	require.NotNil(t, cmd)
	sent, isReacted := cmd().(reactedMsg)
	require.True(t, isReacted)
	require.NoError(t, sent.err)

	require.Len(t, f.Reacted["om_a"], 1)
	require.Equal(t, "Yes", f.Reacted["om_a"][0].EmojiType,
		"emoji_type is case-sensitive: Feishu answers 231001 to the folded spelling")
	require.Equal(t, map[string]bool{"YES": true}, next.(Model).reactStates()["om_a"],
		"the strip still matches chips on the folded key")
}

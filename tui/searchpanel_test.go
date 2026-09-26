package tui

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// messageHits wraps messages as the hits a panel holds, which is what a test
// building a search by hand wants.
func messageHits(msgs ...store.Message) []searchHit {
	out := make([]searchHit, 0, len(msgs))
	for _, msg := range msgs {
		out = append(out, searchHit{kind: hitMessage, msg: msg})
	}
	return out
}

// panelModel is a model over a store holding one message, one chat and one
// person that all answer to the same query, so a search returns all three
// groups.
func panelModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := context.Background()

	require.NoError(t, st.EnsureChat(ctx, "oc_budget", 1))
	_, err = st.UpsertMessages(ctx, []store.Message{{MessageID: "om_1", ChatID: "oc_budget", MsgType: "text",
		SenderID: "ou_x", SenderName: "张三", ContentRaw: `{"text":"下个季度预算"}`, CreateMs: 100, UpdateMs: 100}}, 1)
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(ctx, "om_1", "下个季度预算", "", "", 1))
	require.NoError(t, st.UpsertContacts(ctx, []store.Contact{
		{OpenID: "ou_a", Name: "预算负责人", Email: "a@example.com", P2PChatID: "oc_a"},
	}, 1))

	m := New(Deps{Store: st, Self: "ou_me"})
	m.width, m.height = 120, 36
	m.chats = []store.Chat{{ChatID: "oc_budget", Name: "预算审核群", ChatMode: "group"}}
	m.layout()
	return m, st
}

// search runs the panel's own command and folds the answer back in, the way
// the update loop does.
func search(t *testing.T, m Model, query string) Model {
	t.Helper()
	mm, _ := m.openSearch(query)
	m = mm.(Model)
	msg := localSearch(m.deps, m.chats, query, m.searchGen)()
	next, _ := m.update(msg)
	return next.(Model)
}

func TestSearchPanel_LocalGroupsRenderUnderTheirRules(t *testing.T) {
	m, _ := panelModel(t)
	m = search(t, m, "预算")

	kinds := make([]searchKind, 0, len(m.searchHits))
	for _, h := range m.searchHits {
		kinds = append(kinds, h.kind)
	}
	require.Equal(t, []searchKind{hitMessage, hitChat, hitPerson}, kinds)

	out := rowText(m.msgRows)
	require.Contains(t, out, "Messages")
	require.Contains(t, out, "Chats")
	require.Contains(t, out, "People")
	require.Contains(t, out, "预算审核群")
	require.Contains(t, out, "预算负责人")
	require.Contains(t, out, "a@example.com",
		"a person carries what tells two of them apart; department is only known after the detail backfill")
}

func TestSearchRowText_PrefersTheDepartmentOverTheAddress(t *testing.T) {
	h := searchHit{kind: hitPerson, user: larkcli.User{Name: "张三", Department: "财务", Email: "z@example.com"}}
	require.Contains(t, ansi.Strip(searchRowText(h)), "张三 · 财务")

	h.user.Department = ""
	require.Contains(t, ansi.Strip(searchRowText(h)), "张三 · z@example.com")
}

func TestSearchPanel_OneCursorWalksAllThreeGroups(t *testing.T) {
	m, _ := panelModel(t)
	m = search(t, m, "预算")
	require.Len(t, m.searchHits, 3)

	h, ok := m.selectedHit()
	require.True(t, ok)
	require.Equal(t, hitMessage, h.kind)

	mm, _ := m.moveSelection(1)
	m = mm.(Model)
	h, _ = m.selectedHit()
	require.Equal(t, hitChat, h.kind)

	mm, _ = m.moveSelection(1)
	m = mm.(Model)
	h, _ = m.selectedHit()
	require.Equal(t, hitPerson, h.kind)

	mm, _ = m.moveSelection(1)
	m = mm.(Model)
	require.Equal(t, 2, m.msgIdx, "the cursor stops at the last hit")
}

func TestSearchPanel_RuleRowsBelongToNoHit(t *testing.T) {
	m, _ := panelModel(t)
	m = search(t, m, "预算")
	for _, r := range m.msgRows {
		if strings.Contains(ansi.Strip(r.text), "── Chats") {
			require.True(t, r.plain, "a rule must never take the selection")
		}
	}
}

func TestSearchPanel_StaleGenerationIsDiscarded(t *testing.T) {
	m, _ := panelModel(t)
	m = search(t, m, "预算")
	before := m.searchHits

	// A result that names an older query must not land under a newer one.
	next, _ := m.update(searchMsg{gen: m.searchGen - 1, hits: nil})
	m = next.(Model)
	require.Equal(t, before, m.searchHits)
}

func TestSearchPanel_SelectedIsOnlyAMessageOnAMessageHit(t *testing.T) {
	m, _ := panelModel(t)
	m = search(t, m, "预算")

	sel, ok := m.selected()
	require.True(t, ok)
	require.Equal(t, "om_1", sel.MessageID)

	mm, _ := m.moveSelection(1) // the chat row
	m = mm.(Model)
	_, ok = m.selected()
	require.False(t, ok, "a chat row is not a message")
}

func TestOpenHit_ChatRowOpensTheChat(t *testing.T) {
	m, _ := panelModel(t)
	m = search(t, m, "预算")
	mm, _ := m.moveSelection(1)
	m = mm.(Model)

	mm, _ = m.openHit()
	m = mm.(Model)
	require.Equal(t, "oc_budget", m.pendingChat)
	require.False(t, m.searching, "opening leaves the panel")
}

func TestOpenHit_MessageRowAnchorsThePage(t *testing.T) {
	m, _ := panelModel(t)
	m = search(t, m, "预算")

	mm, _ := m.openHit()
	m = mm.(Model)
	require.Equal(t, "oc_budget", m.pendingChat)
	require.Equal(t, "om_1", m.pendingSelect.id)
	require.Equal(t, int64(100), m.pendingSince)
}

func TestOpenPerson_WithoutALocalChatPrimesTheSend(t *testing.T) {
	m, _ := panelModel(t)
	m = search(t, m, "预算")
	mm, _ := m.moveSelection(2) // the person row
	m = mm.(Model)

	mm, _ = m.openHit()
	m = mm.(Model)
	require.Equal(t, modeCommand, m.mode)
	require.Equal(t, "send ou_a ", m.cmdline.Value(),
		"someone never messaged has no chat row, so the send that starts one is primed")
}

func TestOpenPerson_WithALocalChatOpensIt(t *testing.T) {
	m, _ := panelModel(t)
	m.chats = append(m.chats, store.Chat{ChatID: "oc_a", Name: "预算负责人", ChatMode: "p2p"})
	m = search(t, m, "预算")

	var person int
	for i, h := range m.searchHits {
		if h.kind == hitPerson {
			person = i
		}
	}
	m.msgIdx = person
	mm, _ := m.openHit()
	m = mm.(Model)
	require.Equal(t, "oc_a", m.pendingChat)
	require.Equal(t, modeNormal, m.mode)
}

func TestOpenSearch_SeedsFromTheCommandArgument(t *testing.T) {
	m, _ := panelModel(t)
	mm, _ := m.runCommand("search 预算")
	m = mm.(Model)
	require.Equal(t, modeSearch, m.mode)
	require.True(t, m.searching)
	require.Equal(t, "预算", m.cmdline.Value())
	require.Equal(t, "预算", m.searchQuery)
}

func TestOnSearchKey_EscLeavesThePanel(t *testing.T) {
	m, _ := panelModel(t)
	m = search(t, m, "预算")
	mm, _ := m.onSearchKey(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = mm.(Model)
	require.False(t, m.searching)
	require.Equal(t, modeNormal, m.mode)
	require.Empty(t, m.searchHits)
}

func TestOnSearchKey_TypingRearmsTheTimerForANewQuery(t *testing.T) {
	m, _ := panelModel(t)
	mm, _ := m.openSearch("预")
	m = mm.(Model)
	gen := m.searchGen

	mm, cmd := m.onSearchKey(tea.KeyPressMsg{Code: '算', Text: "算"})
	m = mm.(Model)
	require.Equal(t, "预算", m.searchQuery)
	require.Greater(t, m.searchGen, gen, "a changed query invalidates the search in flight")
	require.NotNil(t, cmd)
}

func TestClaimSearch_FailsOncePanelIsClosed(t *testing.T) {
	m, _ := panelModel(t)
	mm, _ := m.openSearch("预算")
	m = mm.(Model)
	require.True(t, m.claimSearch(m.searchGen))
	m.closeSearch()
	require.False(t, m.claimSearch(m.searchGen))
}

func TestLocalSearch_EmptyQueryAnswersWithNothing(t *testing.T) {
	m, _ := panelModel(t)
	msg := localSearch(m.deps, m.chats, "  ", 3)().(searchMsg)
	require.Equal(t, 3, msg.gen)
	require.Empty(t, msg.hits)
}

func TestLocalSearch_ReachesAChatByPinyinInitials(t *testing.T) {
	m, _ := panelModel(t)
	msg := localSearch(m.deps, m.chats, "yssh", 1)().(searchMsg)
	var names []string
	for _, h := range msg.hits {
		if h.kind == hitChat {
			names = append(names, h.chat.Name)
		}
	}
	require.Equal(t, []string{"预算审核群"}, names)
}

// coldFake plays a Feishu holding one message this machine has never synced.
func coldFake(content string) *larkcli.Fake {
	f := larkcli.NewFake()
	f.AddMessage(larkcli.RawMessage{
		MessageID: "om_cold", ChatID: "oc_budget", MsgType: "text",
		CreateTime: 50, UpdateTime: 50,
		Sender: larkcli.RawSender{ID: "ou_a", SenderType: "user"},
		Body:   larkcli.RawBody{Content: `{"text":"` + content + `"}`},
	})
	f.Rendered["om_cold"] = larkcli.RenderedMessage{
		MessageID: "om_cold", ChatID: "oc_budget", MsgType: "text", Content: content,
	}
	return f
}

// remoteSearched runs the panel's remote command and folds the answer in.
func remoteSearched(t *testing.T, m Model, query string) Model {
	t.Helper()
	msg := remoteSearch(context.Background(), m.deps, query, m.searchGen)()
	next, _ := m.update(msg)
	return next.(Model)
}

func TestRemoteSearch_ColdHitsFollowTheStoresInTheMessagesGroup(t *testing.T) {
	m, _ := panelModel(t)
	m.deps.Client = coldFake("预算")
	m = search(t, m, "预算")
	m = remoteSearched(t, m, "预算")

	require.Len(t, m.searchRemote, 1)
	require.Equal(t, "om_cold", m.searchRemote[0].msg.MessageID)
	require.True(t, m.searchRemote[0].remote)

	kinds := make([]searchKind, 0, len(m.searchHits))
	for _, h := range m.searchHits {
		kinds = append(kinds, h.kind)
	}
	require.Equal(t, []searchKind{hitMessage, hitMessage, hitChat, hitPerson}, kinds,
		"a cold hit joins the message group rather than opening one of its own")
	require.False(t, m.searchHits[0].remote, "the store's hit leads")
	require.True(t, m.searchHits[1].remote)

	out := rowText(m.msgRows)
	require.Contains(t, out, "── Feishu")
	require.Less(t, strings.Index(out, "── Feishu"), strings.Index(out, "── Chats"))
}

func TestRemoteSearch_DropsAHitTheStoreAlreadyHas(t *testing.T) {
	m, st := panelModel(t)
	f := coldFake("预算")
	// The store already holds om_1, so Feishu answering with it too must not
	// put it on screen twice.
	f.AddMessage(larkcli.RawMessage{MessageID: "om_1", ChatID: "oc_budget", MsgType: "text",
		CreateTime: 100, UpdateTime: 100, Body: larkcli.RawBody{Content: `{"text":"预算"}`}})
	m.deps.Client = f
	_ = st

	m = search(t, m, "预算")
	m = remoteSearched(t, m, "预算")
	require.Len(t, m.searchRemote, 1)
	require.Equal(t, "om_cold", m.searchRemote[0].msg.MessageID)
}

func TestRemoteSearch_StaleGenerationIsDiscarded(t *testing.T) {
	m, _ := panelModel(t)
	m.deps.Client = coldFake("预算")
	m = search(t, m, "预算")

	next, _ := m.update(remoteSearchMsg{gen: m.searchGen - 1, hits: messageHits(store.Message{MessageID: "om_stale"})})
	m = next.(Model)
	require.Empty(t, m.searchRemote, "an answer to a query that no longer exists never lands")
}

func TestRemoteSearch_FailureLeavesTheStoresHalfOnScreen(t *testing.T) {
	m, _ := panelModel(t)
	m = search(t, m, "预算")
	local := m.searchHits

	next, _ := m.update(remoteSearchMsg{gen: m.searchGen, err: errors.New("rate limited")})
	m = next.(Model)
	require.Equal(t, local, m.searchHits)
	require.False(t, m.searchBusy)
	require.Contains(t, m.notice, "rate limited")
}

func TestStartRemote_SkipsQueriesTooShortToBeWorthARoundTrip(t *testing.T) {
	m, _ := panelModel(t)
	m.deps.Client = coldFake("预算")
	mm, _ := m.openSearch("预")
	m = mm.(Model)
	require.Nil(t, m.startRemote())
	require.False(t, m.searchBusy)

	m.searchQuery = "预算"
	require.NotNil(t, m.startRemote())
	require.True(t, m.searchBusy)
	m.cancelRemote()
}

func TestArmSearch_DropsTheRemoteSearchInFlight(t *testing.T) {
	m, _ := panelModel(t)
	m.deps.Client = coldFake("预算")
	mm, _ := m.openSearch("预算")
	m = mm.(Model)
	m.startRemote()
	require.True(t, m.searchBusy)

	m.armSearch()
	require.False(t, m.searchBusy, "a new query gives up the lane the old one holds")
	require.Nil(t, m.searchCancel)
}

func TestCloseSearch_DropsTheRemoteSearchInFlight(t *testing.T) {
	m, _ := panelModel(t)
	m.deps.Client = coldFake("预算")
	mm, _ := m.openSearch("预算")
	m = mm.(Model)
	m.startRemote()

	m.closeSearch()
	require.False(t, m.searchBusy)
	require.Nil(t, m.searchCancel)
}

func TestOpenColdHit_WithoutTheSyncLockOpensTheChatAndSaysSo(t *testing.T) {
	m, _ := panelModel(t)
	m.deps.Client = coldFake("预算")
	m = search(t, m, "预算")
	m = remoteSearched(t, m, "预算")

	m.msgIdx = 1 // the cold hit
	mm, _ := m.openHit()
	m = mm.(Model)
	require.Equal(t, "oc_budget", m.pendingChat)
	require.Contains(t, m.notice, "daemon")
	require.False(t, m.searching)
}

func TestOpenColdHit_WithTheSyncLockPullsItInFirst(t *testing.T) {
	m, st := panelModel(t)
	f := coldFake("预算")
	m.deps.Client = f
	m.deps.Syncer = &sync.Syncer{Client: f, Store: st, Clock: sync.RealClock{}}
	m = search(t, m, "预算")
	m = remoteSearched(t, m, "预算")

	m.msgIdx = 1
	mm, cmd := m.openHit()
	m = mm.(Model)
	require.Contains(t, m.notice, "fetching")
	require.NotNil(t, cmd)

	next, _ := m.update(cmd())
	m = next.(Model)
	require.Equal(t, "oc_budget", m.pendingChat)
	require.Equal(t, "om_cold", m.pendingSelect.id)

	// The message is in the store now, so the page that opens holds it.
	got, err := st.GetMessage(context.Background(), "om_cold")
	require.NoError(t, err)
	require.Equal(t, "oc_budget", got.ChatID)
}

func TestSearchTitle_SaysWhenFeishuIsStillOut(t *testing.T) {
	m, _ := panelModel(t)
	m = search(t, m, "预算")
	m.searchBusy = true
	require.Contains(t, ansi.Strip(m.renderHeader(80)), "asking Feishu")
	m.searchBusy = false
	require.Contains(t, ansi.Strip(m.renderHeader(80)), "Esc to leave")
}

func TestOnSearchKey_PageKeysWalkTheHitsAndEditingKeysDoNot(t *testing.T) {
	m, _ := panelModel(t)
	m = search(t, m, "预算")
	require.Len(t, m.searchHits, 3)

	mm, _ := m.onSearchKey(tea.KeyPressMsg{Code: tea.KeyPgDown})
	m = mm.(Model)
	require.Equal(t, 2, m.msgIdx, "a page down stops at the last hit")

	mm, _ = m.onSearchKey(tea.KeyPressMsg{Code: tea.KeyPgUp})
	m = mm.(Model)
	require.Equal(t, 0, m.msgIdx)

	// ctrl+d belongs to the line editor the query is typed into.
	before := m.msgIdx
	mm, _ = m.onSearchKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
	m = mm.(Model)
	require.Equal(t, before, m.msgIdx)
}

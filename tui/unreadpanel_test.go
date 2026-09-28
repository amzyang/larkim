package tui

import (
	"slices"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"

	"github.com/amzyang/larkim/store"
)

// onSection walks the cursor to the first message of the named chat.
func onSection(t *testing.T, m Model, chatID string) Model {
	t.Helper()
	for i, x := range m.msgs {
		if x.ChatID == chatID {
			m.msgIdx = i
			cmd := m.feedRetarget()
			return applyAll(t, m, cmd)
		}
	}
	t.Fatalf("no section for %s", chatID)
	return m
}

func TestFeed_ReplyGoesToTheChatUnderTheCursor(t *testing.T) {
	m := feedModel(t)
	require.Equal(t, "oc_platform", m.chatID)

	m = onSection(t, m, "oc_project")

	require.Equal(t, "oc_project", m.chatID, "the cursor's section is where a reply lands")
	require.Contains(t, m.renderHeader(80), "项目协作群")
}

// Sections are short and a border is one j away, so a composer holding words
// keeps the chat they were written for.
func TestFeed_TheReplyTargetHoldsWhileTheComposerHasText(t *testing.T) {
	m := feedModel(t)
	m.input.SetValue("等我看一下")

	m = onSection(t, m, "oc_project")

	require.Equal(t, "oc_platform", m.chatID, "the words stay with the chat they were written for")
	require.Equal(t, "等我看一下", m.input.Value())

	m.input.SetValue("")
	m = applyAll(t, m, m.feedRetarget())
	require.Equal(t, "oc_project", m.chatID, "and the target catches up once they are gone")
}

func TestFeed_MovingAcrossASectionCarriesTheDraftWithIt(t *testing.T) {
	m := feedModel(t)
	require.NoError(t, m.deps.Store.SaveDraft(t.Context(), store.Draft{ChatID: "oc_project", Text: "周四没问题"}, 900))

	m = onSection(t, m, "oc_project")

	require.Equal(t, "周四没问题", m.input.Value(), "the chat's own draft comes up with it")
}

// r puts a quote up without a word being typed, and the cursor is free to walk
// away to read while it stands. Retargeting there would take the quote away.
func TestFeed_AQuotePinsTheTargetTheCursorWalksAwayFrom(t *testing.T) {
	m := feedModel(t)
	m = onSection(t, m, "oc_project")
	sel, ok := m.selected()
	require.True(t, ok)
	m.setQuote(&sel, false)

	m = onSection(t, m, "oc_platform")

	require.NotNil(t, m.replyTo, "the quote stands")
	require.Equal(t, "om_j1", m.replyTo.MessageID)
	require.Equal(t, "oc_project", m.chatID, "and the answer is still bound for its chat")

	m.setQuote(nil, false)
	m = applyAll(t, m, m.feedRetarget())
	require.Equal(t, "oc_platform", m.chatID, "dropping it hands the target back to the cursor")
}

func TestFeed_EnterOpensTheChatAnchoredOnTheMessage(t *testing.T) {
	m := feedModel(t)
	m.focus = paneMessages
	m = onSection(t, m, "oc_project")

	next, _ := m.activate()
	m = next.(Model)

	require.Equal(t, "oc_project", m.pendingChat, "Enter leaves the panel for the chat itself")
	require.Equal(t, "om_j1", m.pendingSelect.id, "landing on the message it was pressed on")
	require.NotNil(t, m.feed, "the panel stands until that page arrives")

	next, _ = m.Update(messagesLoadedMsg{chatID: "oc_project", msgs: m.msgs[len(m.msgs)-1:]})
	require.Nil(t, next.(Model).feed, "and comes down when it does")
}

func TestFeed_NAndNWalkTheSections(t *testing.T) {
	m := feedModel(t)

	next, _ := m.jumpSection(1)
	m = next.(Model)
	require.Equal(t, "oc_project", m.msgs[m.msgIdx].ChatID)
	require.Equal(t, "om_j1", m.msgs[m.msgIdx].MessageID, "landing on the section's first message")

	next, _ = m.jumpSection(1)
	require.Equal(t, "om_p1", next.(Model).msgs[next.(Model).msgIdx].MessageID, "wrapping the way n walks the list")
}

func TestFeed_EscGoesBackToTheChatThatWasOpen(t *testing.T) {
	st, chats := backlog(t)
	m := New(Deps{Store: st, Self: "ou_me"})
	m.width, m.height, m.chats, m.chatID = 120, 40, chats, "oc_project"
	m.unread = map[string]int64{"oc_platform": 2, "oc_project": 1}
	next, cmd := m.openUnread()
	m = applyAll(t, next.(Model), cmd)
	require.Equal(t, "oc_platform", m.chatID, "the panel opened on the oldest backlog")

	// Through the key, not the method: closeUnread writes to its receiver, and
	// what the Esc arm hands back is what decides whether that lands.
	next, cmd = m.Update(tea.KeyPressMsg{Code: tea.KeyEscape})
	m = applyAll(t, next.(Model), cmd)

	require.Nil(t, m.feed)
	require.Equal(t, "oc_project", m.chatID, "back where the reader was")
	require.Equal(t, paneChats, m.focus)
}

// Nothing here is taken as read: the reader is looking at a page of many
// chats, and none of them has been opened.
func TestFeed_TakesNothingRead(t *testing.T) {
	m := feedModel(t)
	var opened [][]string
	m.deps.OpenURL = func(targets []string, _ bool) error { opened = append(opened, targets); return nil }

	for range 30 {
		next, cmd := m.Update(tea.KeyPressMsg{Code: 'j', Text: "j"})
		m = applyAll(t, next.(Model), cmd)
	}

	require.Empty(t, m.readKey(true), "the page is not a chat's")
	require.Empty(t, opened, "so the client is walked onto nothing")
	got, err := m.deps.Store.UnreadAnchors(t.Context())
	require.NoError(t, err)
	require.Len(t, got, 2, "and both chats still owe an answer")
}

// The marker is all that says a row is still waiting, the chats it names being
// ones the reader has not opened.
func TestFeed_TheUnreadMarkersSurviveTheCursor(t *testing.T) {
	m := feedModel(t)
	require.Len(t, m.dots, 3)

	next, _ := m.move(1)
	m = next.(Model)

	require.Len(t, m.dots, 3, "walking onto a block is not reading the chat it came from")
}

func TestFeed_ASendWaitsUnderItsOwnSection(t *testing.T) {
	m := feedModel(t)
	m.selfName = "林岚"
	m.outbox = []outboxItem{{localID: "cli_c", chatID: "oc_platform", msgType: "text",
		body: "在看了", createMs: 900, state: outSending}}
	m.applyOutbox()

	require.Equal(t, []string{"om_p1", "om_p2", "cli_c", "om_j1"}, idsOf(m.msgs),
		"under 平台组, not at the foot of the page")
}

func TestFeed_ADataRevReloadDropsAChatReadElsewhere(t *testing.T) {
	m := feedModel(t)
	require.NoError(t, m.deps.Store.MarkChatRead(t.Context(), "oc_platform", 900))

	m = applyAll(t, m, m.reloadCurrent())

	require.Equal(t, []string{"oc_project"}, []string{m.feed.sections[0].chatID},
		"the panel writes nothing, so it follows where the store settled")
	require.Len(t, m.feed.sections, 1)
	require.Equal(t, []string{"om_j1"}, idsOf(m.msgs))
}

func TestFeed_GrowingIsOffInThePanel(t *testing.T) {
	m := feedModel(t)
	m.msgTop = 0
	require.Nil(t, m.growMessages(), "the sections are anchored where the backlog starts")
}

// Ctrl+f draws over the panel through the same rows and the same cursor, so
// while it is up every key that reads m.msgs has to leave them alone.
func TestFeed_TheSearchPanelDrawsOverIt(t *testing.T) {
	m := feedModel(t)
	next, _ := m.openSearch("接口")
	m = next.(Model)
	m.searchHits = []searchHit{{kind: hitChat, chat: store.Chat{ChatID: "oc_platform", Name: "平台组"}}}
	m.rebuildMessages()

	require.False(t, m.inFeed(), "no section rule of the panel's is pinned over the hits")
	require.Contains(t, m.renderHeader(80), "Search", "and the title is the search's")

	m.closeSearch()

	require.True(t, m.inFeed(), "leaving the search puts the panel back")
	require.True(t, slices.ContainsFunc(m.msgRows, func(r msgRow) bool { return r.rule }),
		"and the page is parted by its sections again")
	require.Contains(t, m.renderHeader(80), "Unread")
}

// A section dropping out renumbers every row behind it, so the cursor is held
// by the message it was on rather than by its place.
func TestFeed_AReloadHoldsTheCursorOnItsMessage(t *testing.T) {
	m := feedModel(t)
	m = onSection(t, m, "oc_project")
	require.Equal(t, "om_j1", m.msgs[m.msgIdx].MessageID)

	// 平台组's backlog is recalled while the page is up, taking its section.
	var gone []store.Message
	for _, x := range m.msgs {
		if x.ChatID == "oc_platform" {
			x.Deleted = true
			gone = append(gone, x)
		}
	}
	_, err := m.deps.Store.UpsertMessages(t.Context(), gone, 900)
	require.NoError(t, err)
	m = applyAll(t, m, m.reloadCurrent())

	require.Equal(t, []string{"om_j1"}, idsOf(m.msgs))
	require.Equal(t, "om_j1", m.msgs[m.msgIdx].MessageID, "the cursor kept its message, not its index")
}

func TestOnClick_TheUnreadRowOpensThePanel(t *testing.T) {
	m := feedModel(t)
	m.feed, m.focus, m.chatIdx, m.chatTop = nil, paneChats, 0, 0

	next, _ := m.onClick(tea.Mouse{Button: tea.MouseLeft, X: 4, Y: 1 + headerHeight})
	m = next.(Model)

	require.NotNil(t, m.feed, "one click is enough: no chat sits under the row to be merely selected")
	require.Equal(t, paneMessages, m.focus)
}

// draftOf is what the store holds for a chat.
func draftOf(t *testing.T, m Model, chatID string) string {
	t.Helper()
	d, err := m.deps.Store.LoadDraft(t.Context(), chatID, "")
	require.NoError(t, err)
	return d.Text
}

// The panel clears the composer on its way up, and the first retarget then
// finds it empty under the chat that was open. Writing that back would file an
// empty draft over the one the panel had just saved, which SaveDraft reads as
// a delete.
func TestFeed_OpeningThePanelKeepsTheDraftOfTheChatItLeaves(t *testing.T) {
	st, chats := backlog(t)
	m := New(Deps{Store: st, Self: "ou_me"})
	m.width, m.height, m.chats, m.chatID = 120, 40, chats, "oc_project"
	m.unread = map[string]int64{"oc_platform": 2, "oc_project": 1}
	m.input.SetValue("周四没问题，我来准备")

	next, cmd := m.openUnread()
	m = applyAll(t, next.(Model), cmd)

	require.Equal(t, "oc_platform", m.chatID, "the panel opened on the oldest backlog")
	require.Equal(t, "周四没问题，我来准备", draftOf(t, m, "oc_project"))
}

// Walking across a border faster than the side load answers leaves the
// composer empty under a chat whose own draft has not been shown yet.
func TestFeed_WalkingPastAChatKeepsItsDraft(t *testing.T) {
	m := feedModel(t)
	require.NoError(t, m.deps.Store.SaveDraft(t.Context(), store.Draft{ChatID: "oc_project", Text: "周四没问题"}, 900))

	// Into 项目协作群 and straight out again, without waiting for its draft.
	for i, x := range m.msgs {
		if x.ChatID == "oc_project" {
			m.msgIdx = i
			break
		}
	}
	cmd := m.feedRetarget()
	require.Equal(t, "oc_project", m.chatID)
	m.msgIdx = 0
	// Both writes are run: the draft is erased by the command, not by the
	// call that hands it back.
	m = applyAll(t, m, tea.Batch(cmd, m.feedRetarget()))

	require.Equal(t, "oc_platform", m.chatID)
	require.Equal(t, "周四没问题", draftOf(t, m, "oc_project"), "a draft nobody showed is nobody's to erase")
}

// Words the reader typed are theirs whatever the side load has done, so they
// go back under the chat they were written for.
func TestFeed_LeavingThePanelKeepsWhatWasTyped(t *testing.T) {
	m := feedModel(t)
	m = onSection(t, m, "oc_project")
	m.input.SetValue("我看一下")

	m = applyAll(t, m, m.closeUnread())

	require.Equal(t, "我看一下", draftOf(t, m, "oc_project"))
}

// n has to move the composer with the cursor, or the title names one chat
// while i, r and submit send to another.
func TestFeed_NCarriesTheReplyTargetWithTheCursor(t *testing.T) {
	m := feedModel(t)
	require.Equal(t, "oc_platform", m.chatID)

	next, cmd := m.jumpSection(1)
	m = applyAll(t, next.(Model), cmd)

	require.Equal(t, "oc_project", m.msgs[m.msgIdx].ChatID)
	require.Equal(t, "oc_project", m.chatID, "the target went with it")
	require.Contains(t, m.renderHeader(80), "项目协作群")
}

// Pressing r names a message, which is the deliberate act the pinned target
// makes room for. The words go with it, and the title says so — without this
// submit would send under replyTo.ChatID while the only chat name on screen
// was the one the words were begun in.
func TestFeed_AnsweringAcrossASectionCarriesTheWordsAndTheTitle(t *testing.T) {
	m := feedModel(t)
	m.input.SetValue("我这边看一下接口")
	m = onSection(t, m, "oc_project")
	require.Equal(t, "oc_platform", m.chatID, "the words held the target where they were begun")

	sel, ok := m.selected()
	require.True(t, ok)
	next, cmd := m.startInsert(&sel, false)
	m = applyAll(t, next.(Model), cmd)

	require.Equal(t, "oc_project", m.chatID, "naming a message moves the target to it")
	require.Contains(t, m.renderHeader(80), "项目协作群")
	require.Equal(t, "我这边看一下接口", m.input.Value(), "the words came along")

	next, _ = m.submit()
	require.Equal(t, "oc_project", next.(Model).outbox[0].chatID, "which is where it goes")
}

// A loadMessages still in flight when the panel goes up would put one chat's
// whole history under the frozen section rules.
func TestFeed_APageForTheCursorsChatDoesNotOverwriteThePanel(t *testing.T) {
	m := feedModel(t)
	require.Equal(t, "oc_platform", m.chatID)
	before := idsOf(m.msgs)

	next, _ := m.Update(messagesLoadedMsg{chatID: "oc_platform",
		msgs: []store.Message{{MessageID: "om_p0", ChatID: "oc_platform", Content: "已经看过了", RenderedAt: 1}}})
	m = next.(Model)

	require.NotNil(t, m.feed)
	require.Equal(t, before, idsOf(m.msgs), "the page the panel holds is its own")
}

// Nothing waiting leaves the panel with no chat open, and the pane must not
// read that as an empty screen to fill with the first chat in the list.
func TestFeed_AnEmptyPanelDoesNotCloseItself(t *testing.T) {
	st := feedStore(t)
	require.NoError(t, st.EnsureChat(t.Context(), "oc_platform", 1))
	m := New(Deps{Store: st, Self: "ou_me"})
	m.width, m.height = 120, 40
	m.chats = []store.Chat{{ChatID: "oc_platform", Name: "平台组"}}
	next, cmd := m.openUnread()
	m = applyAll(t, next.(Model), cmd)
	require.Empty(t, m.chatID, "there is no section for the composer to answer")

	next, _ = m.Update(chatsLoadedMsg{chats: m.chats})
	m = next.(Model)

	require.NotNil(t, m.feed, "the panel is what is being shown, not a blank to fill")
	require.Empty(t, m.pendingChat)
}

// pointedAtProject leaves the composer pointed at 项目协作群 with nothing in
// it and that chat's own draft never shown: r moved the target, and the side
// load that would have caught m.feed.loaded up returned early while the quote
// was still up. Every write from here on is an empty one.
func pointedAtProject(t *testing.T) Model {
	t.Helper()
	m := feedModel(t)
	require.NoError(t, m.deps.Store.SaveDraft(t.Context(), store.Draft{ChatID: "oc_project", Text: "周四没问题"}, 900))

	var sel store.Message
	for _, x := range m.msgs {
		if x.ChatID == "oc_project" {
			sel = x
			break
		}
	}
	require.NotEmpty(t, sel.ChatID, "no 项目协作群 message in the backlog")

	next, cmd := m.startInsert(&sel, false)
	m = applyAll(t, next.(Model), cmd)
	require.Equal(t, "oc_project", m.chatID, "r points the composer at the message's chat")
	require.Empty(t, m.feed.loaded, "the side load left while the quote held the composer")

	// The quote comes down and nothing was typed, so the widget is empty and
	// belongs to no chat.
	m.setQuote(nil, false)
	m.input.SetValue("")
	return m
}

// The reader goes to another window while the panel is up.
func TestFeed_BlurKeepsTheDraftOfAChatOnlyPointedAt(t *testing.T) {
	m := pointedAtProject(t)

	next, cmd := m.Update(tea.BlurMsg{})
	m = applyAll(t, next.(Model), cmd)

	require.Equal(t, "周四没问题", draftOf(t, m, "oc_project"))
}

// Enter on a message leaves the panel for that message's chat.
func TestFeed_OpeningAHitKeepsTheDraftOfAChatOnlyPointedAt(t *testing.T) {
	m := pointedAtProject(t)

	next, cmd := m.openFeedHit()
	m = applyAll(t, next.(Model), cmd)

	require.Equal(t, "周四没问题", draftOf(t, m, "oc_project"))
}

// q from inside the panel.
func TestFeed_QuitKeepsTheDraftOfAChatOnlyPointedAt(t *testing.T) {
	m := pointedAtProject(t)

	m = applyAll(t, m, m.quit())

	require.Equal(t, "周四没问题", draftOf(t, m, "oc_project"))
}

func TestChatsLoaded_TheFirstListingOpensTheUnreadRow(t *testing.T) {
	m := sized(120, 36)
	// A cold start: the model has been built but no listing has landed yet,
	// so nothing is open and nothing has been asked for.
	chats := m.chats
	m.chats, m.chatID, m.msgsBase, m.msgs = nil, "", nil, nil

	mm, _ := m.update(chatsLoadedMsg{chats: chats})
	m = mm.(Model)

	require.True(t, m.visibleRows()[m.chatIdx].isFeed(), "the cursor opens on the Unread row")
	require.NotNil(t, m.feed, "and the row's page is what the message pane holds")
	require.Equal(t, paneChats, m.focus, "the page loads beside the reader, not under them")
	require.Empty(t, m.pendingChat, "no chat is asked for, so nothing is taken as read")
}

func TestChatsLoaded_ALaterListingLeavesTheOpenChatAlone(t *testing.T) {
	m := sized(120, 36)
	mm, _ := m.update(chatsLoadedMsg{chats: m.chats})
	m = mm.(Model)

	require.Nil(t, m.feed, "a chat is already open, so the panel does not come up over it")
	require.Equal(t, "oc_1", m.chatID)
}

func TestCloseUnread_WithNothingBehindItFallsBackToTheFirstChat(t *testing.T) {
	m := sized(120, 36)
	m.chatID, m.feed = "", &unreadFeed{}

	require.NotNil(t, m.closeUnread())
	require.Equal(t, "oc_0", m.pendingChat, "Esc leaves the reader in a chat, not an empty pane")
	require.Equal(t, paneChats, m.focus)
}

func TestCloseUnread_WithNoChatsLeavesThePaneEmpty(t *testing.T) {
	m := sized(120, 36)
	m.chats, m.chatID, m.feed = nil, "", &unreadFeed{}

	m.closeUnread()
	require.Nil(t, m.feed)
	require.Empty(t, m.pendingChat)
	require.Empty(t, m.msgs)
}

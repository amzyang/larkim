package tui

import (
	"path/filepath"
	"slices"
	"testing"

	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"

	"github.com/amzyang/larkim/store"
)

// feedStore is a store holding three chats with backlogs of different ages,
// so the panel has something to order and something to freeze.
func feedStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	return st
}

// say puts one message in a chat, rendered so the rows have something to draw.
func say(t *testing.T, st *store.Store, id, chatID string, ms int64, text string) store.Message {
	t.Helper()
	m := store.Message{MessageID: id, ChatID: chatID, MsgType: "text", SenderID: "ou_a",
		SenderType: "user", SenderName: "张三", ContentRaw: `{"text":"` + text + `"}`,
		Content: text, RenderedAt: 1, CreateMs: ms, UpdateMs: ms, MessagePosition: ms}
	_, err := st.UpsertMessages(t.Context(), []store.Message{m}, 1)
	require.NoError(t, err)
	return m
}

func owing(t *testing.T, st *store.Store, ids ...string) {
	t.Helper()
	unread := false
	for _, id := range ids {
		require.NoError(t, st.SetReadStatus(t.Context(), id, &unread, 100, 0))
	}
}

// backlog is the fixture the panel tests read: 平台组 waiting since 100, and
// 项目协作群 since 300, with one message in 平台组 already read behind its anchor.
func backlog(t *testing.T) (*store.Store, []store.Chat) {
	t.Helper()
	st := feedStore(t)
	ctx := t.Context()
	chats := []store.Chat{
		{ChatID: "oc_platform", Name: "平台组", ChatMode: "group", UnreadCount: 2},
		{ChatID: "oc_project", Name: "项目协作群", ChatMode: "group", UnreadCount: 1},
	}
	require.NoError(t, st.UpsertChats(ctx, chats, 1))
	say(t, st, "om_p0", "oc_platform", 50, "已经看过了")
	say(t, st, "om_p1", "oc_platform", 100, "接口什么时候好")
	say(t, st, "om_p2", "oc_platform", 200, "还有一个问题")
	say(t, st, "om_j1", "oc_project", 300, "发布推迟到周四")
	read := true
	require.NoError(t, st.SetReadStatus(ctx, "om_p0", &read, 100, 0))
	owing(t, st, "om_p1", "om_p2", "om_j1")
	return st, chats
}

func TestGatherUnread_OrdersSectionsByTheOldestBacklog(t *testing.T) {
	st, chats := backlog(t)

	secs, msgs, _, err := gatherUnread(t.Context(), st, "ou_me", chats, nil)
	require.NoError(t, err)

	require.Equal(t, []string{"oc_platform", "oc_project"}, []string{secs[0].chatID, secs[1].chatID},
		"the chat that has waited longest opens the page")
	require.Equal(t, []int64{100, 300}, []int64{secs[0].anchorMs, secs[1].anchorMs})
	require.Equal(t, []string{"om_p1", "om_p2", "om_j1"}, idsOf(msgs))
	require.False(t, secs[0].cut)
}

// The section is the conversation from the backlog on, not the unread messages
// picked out of it, so a message read after the anchor is still on the page.
func TestGatherUnread_ASectionRunsFromItsAnchorToTheNewest(t *testing.T) {
	st, chats := backlog(t)
	say(t, st, "om_p3", "oc_platform", 400, "我自己发的")
	read := true
	require.NoError(t, st.SetReadStatus(t.Context(), "om_p3", &read, 100, 0))

	_, msgs, _, err := gatherUnread(t.Context(), st, "ou_me", chats, nil)
	require.NoError(t, err)

	require.Equal(t, []string{"om_p1", "om_p2", "om_p3", "om_j1"}, idsOf(msgs))
	require.NotContains(t, idsOf(msgs), "om_p0", "nothing from before the anchor")
}

// The panel is the chat's own rows from the anchor on, so it tells the same
// story the chat does — recall included.
func TestGatherUnread_ASectionKeepsARecallInTheBacklog(t *testing.T) {
	st, chats := backlog(t)
	gone := say(t, st, "om_p3", "oc_platform", 400, "说错了")
	gone.Deleted = true
	_, err := st.UpsertMessages(t.Context(), []store.Message{gone}, 2)
	require.NoError(t, err)

	_, msgs, _, err := gatherUnread(t.Context(), st, "ou_me", chats, nil)
	require.NoError(t, err)

	require.Contains(t, idsOf(msgs), "om_p3")
}

func TestUnreadAnchors_TakeInMutedChats(t *testing.T) {
	_, chats := backlog(t)
	chats[1].Muted = true
	rows := []store.UnreadAnchor{{ChatID: "oc_platform", FirstMs: 100}, {ChatID: "oc_project", FirstMs: 300}}

	secs := unreadAnchors(rows, chats)

	require.Equal(t, []string{"oc_platform", "oc_project"}, []string{secs[0].chatID, secs[1].chatID},
		"opening the panel is the reader asking for the whole backlog, silence and all")
}

func TestUnreadAnchors_AChatTheListingDoesNotHoldIsLeftOut(t *testing.T) {
	rows := []store.UnreadAnchor{{ChatID: "oc_gone", FirstMs: 100}}
	require.Empty(t, unreadAnchors(rows, nil))
}

// manyWaiting is n chats each owing one message, the oldest backlog first.
func manyWaiting(n int) ([]store.UnreadAnchor, []store.Chat) {
	var rows []store.UnreadAnchor
	var chats []store.Chat
	for i := range n {
		id := "oc_" + string(rune('a'+i))
		rows = append(rows, store.UnreadAnchor{ChatID: id, FirstMs: int64(i + 1)})
		chats = append(chats, store.Chat{ChatID: id, Name: id, UnreadCount: 1})
	}
	return rows, chats
}

// The anchor set comes back whole. joinUnread reads a chat missing from it as
// one that has been settled, which a set already cut to the cap would say of a
// chat that is only crowded out.
func TestUnreadAnchors_AnswerWithEveryChatStillWaiting(t *testing.T) {
	rows, chats := manyWaiting(unreadFeedChats + 5)

	secs := unreadAnchors(rows, chats)

	require.Len(t, secs, unreadFeedChats+5)
	require.Equal(t, int64(1), secs[0].anchorMs, "the longest-waiting chat opens the page")
}

func TestJoinUnread_ReachesNoFurtherThanTheChatCap(t *testing.T) {
	rows, chats := manyWaiting(unreadFeedChats + 5)

	page := joinUnread(nil, unreadAnchors(rows, chats))

	require.Len(t, page, unreadFeedChats)
	require.Equal(t, int64(1), page[0].anchorMs, "the longest-waiting chats are the ones kept")
	require.Equal(t, 5, unreadMore(chats, page), "and the rest are counted, not dropped silently")
}

// A chat holding exactly the cap has nothing below it, so the section is whole.
func TestGatherUnread_ASectionOfExactlyTheCapIsNotCut(t *testing.T) {
	st, ids := firehose(t, unreadSectionLimit)
	chats := []store.Chat{{ChatID: "oc_loud", Name: "平台组", UnreadCount: int64(len(ids))}}

	secs, msgs, _, err := gatherUnread(t.Context(), st, "ou_me", chats, nil)
	require.NoError(t, err)

	require.False(t, secs[0].cut)
	require.Len(t, msgs, unreadSectionLimit)
}

// firehose is one chat owing n messages.
func firehose(t *testing.T, n int) (*store.Store, []string) {
	t.Helper()
	st := feedStore(t)
	require.NoError(t, st.EnsureChat(t.Context(), "oc_loud", 1))
	var ids []string
	for i := range n {
		id := "om_loud_" + string(rune('a'+i/26)) + string(rune('a'+i%26))
		say(t, st, id, "oc_loud", int64(i+1), "噪音")
		ids = append(ids, id)
	}
	owing(t, st, ids...)
	return st, ids
}

func TestGatherUnread_CutsAFirehoseSectionAndSaysSo(t *testing.T) {
	st, ids := firehose(t, unreadSectionLimit+10)
	chats := []store.Chat{{ChatID: "oc_loud", Name: "平台组", UnreadCount: int64(len(ids))}}

	secs, msgs, _, err := gatherUnread(t.Context(), st, "ou_me", chats, nil)
	require.NoError(t, err)

	require.True(t, secs[0].cut)
	require.Len(t, msgs, unreadSectionLimit)
	require.Equal(t, ids[0], msgs[0].MessageID, "cut at the newest end, so the backlog still starts where it starts")
}

// A chat still waiting keeps the anchor it was drawn on, so the rows above the
// reader's cursor stay where they are however much of its backlog settles.
func TestGatherUnread_AChatStillWaitingKeepsTheAnchorItWasDrawnOn(t *testing.T) {
	st, chats := backlog(t)
	secs, _, _, err := gatherUnread(t.Context(), st, "ou_me", chats, nil)
	require.NoError(t, err)

	read := true
	require.NoError(t, st.SetReadStatus(t.Context(), "om_p1", &read, 900, 0))
	fresh, err := st.UnreadAnchors(t.Context())
	require.NoError(t, err)
	require.Equal(t, int64(200), fresh[0].FirstMs, "the store has moved on")

	held, msgs, _, err := gatherUnread(t.Context(), st, "ou_me", chats, secs)
	require.NoError(t, err)
	require.Equal(t, int64(100), held[0].anchorMs, "the section has not")
	require.Equal(t, []string{"om_p1", "om_p2", "om_j1"}, idsOf(msgs))
}

// The panel writes nothing, so a chat's backlog only ever settles from outside
// it. A chat with none left leaves the page, taking the anchor it held: that
// anchor is what would re-open the read stretch when the chat next speaks.
func TestGatherUnread_AChatReadElsewhereLeavesThePage(t *testing.T) {
	st, chats := backlog(t)
	secs, _, _, err := gatherUnread(t.Context(), st, "ou_me", chats, nil)
	require.NoError(t, err)

	require.NoError(t, st.MarkChatRead(t.Context(), "oc_platform", 900))

	held, msgs, _, err := gatherUnread(t.Context(), st, "ou_me", chats, secs)
	require.NoError(t, err)
	require.Equal(t, []string{"oc_project"}, []string{held[0].chatID})
	require.Len(t, held, 1)
	require.Equal(t, []string{"om_j1"}, idsOf(msgs))
}

// The stretch a chat opens after its whole backlog is written off starts at
// what it says next, not at the history that was just read.
func TestGatherUnread_ReanchorsAChatAfterItsBacklogIsRead(t *testing.T) {
	st, chats := backlog(t)
	first, _, _, err := gatherUnread(t.Context(), st, "ou_me", chats, nil)
	require.NoError(t, err)
	require.Equal(t, int64(100), first[0].anchorMs)

	_, err = st.MarkAllRead(t.Context(), 900)
	require.NoError(t, err)
	settled, _, _, err := gatherUnread(t.Context(), st, "ou_me", chats, first)
	require.NoError(t, err)
	require.Empty(t, settled, "nothing is waiting, so nothing holds a stretch")

	say(t, st, "om_p9", "oc_platform", 500, "接口好了")
	owing(t, st, "om_p9")

	again, msgs, _, err := gatherUnread(t.Context(), st, "ou_me", chats, settled)
	require.NoError(t, err)
	require.Equal(t, []string{"oc_platform"}, []string{again[0].chatID})
	require.Equal(t, int64(500), again[0].anchorMs)
	require.Equal(t, []string{"om_p9"}, idsOf(msgs))
}

// A chat with nothing waiting is no section, however it got onto the page; an
// empty rule names nothing.
func TestGatherUnread_AChatWithNothingWaitingIsNoSection(t *testing.T) {
	st, chats := backlog(t)
	keep := []unreadSection{
		{chatID: "oc_platform", name: "平台组", anchorMs: 100},
		{chatID: "oc_empty", name: "空的", anchorMs: 100},
	}

	secs, msgs, _, err := gatherUnread(t.Context(), st, "ou_me", chats, keep)
	require.NoError(t, err)

	require.Len(t, secs, 2)
	require.Equal(t, []string{"oc_platform", "oc_project"}, []string{secs[0].chatID, secs[1].chatID},
		"the chat left with nothing to draw is no section; the rest of the page stands")
	require.Equal(t, []string{"om_p1", "om_p2", "om_j1"}, idsOf(msgs))
}

func TestUnreadMore_CountsWhatThePageLeavesOut(t *testing.T) {
	_, chats := backlog(t)
	chats = append(chats,
		store.Chat{ChatID: "oc_late", Name: "后来的", UnreadCount: 3},
		store.Chat{ChatID: "oc_muted", Name: "静音的", UnreadCount: 9, Muted: true},
		store.Chat{ChatID: "oc_quiet", Name: "安静的"})
	shown := []unreadSection{{chatID: "oc_platform"}, {chatID: "oc_project"}}

	require.Equal(t, 2, unreadMore(chats, shown),
		"the chats that started waiting after the anchors were taken, silenced ones among them")
}

// A chat that starts waiting while the panel is up joins the page. It goes
// last however old its backlog: everything above it is already drawn, and the
// reader's cursor is somewhere in it.
func TestGatherUnread_AChatThatStartsWaitingJoinsThePage(t *testing.T) {
	st, chats := backlog(t)
	held, _, _, err := gatherUnread(t.Context(), st, "ou_me", chats, nil)
	require.NoError(t, err)

	require.NoError(t, st.EnsureChat(t.Context(), "oc_late", 1))
	say(t, st, "om_l1", "oc_late", 10, "后来的")
	owing(t, st, "om_l1")
	chats = append(chats, store.Chat{ChatID: "oc_late", Name: "后来的", UnreadCount: 1})

	secs, msgs, _, err := gatherUnread(t.Context(), st, "ou_me", chats, held)
	require.NoError(t, err)

	require.Len(t, secs, 3)
	require.Equal(t, []string{"oc_platform", "oc_project", "oc_late"},
		[]string{secs[0].chatID, secs[1].chatID, secs[2].chatID})
	require.Equal(t, []int64{100, 300, 10}, []int64{secs[0].anchorMs, secs[1].anchorMs, secs[2].anchorMs},
		"the sections already drawn keep the anchors they were drawn on")
	require.Equal(t, []string{"om_p1", "om_p2", "om_j1", "om_l1"}, idsOf(msgs))
}

// A panel opened with nothing waiting holds an empty page, which is the one
// state where every chat is a newcomer.
func TestGatherUnread_AnEmptyPageTakesTheFirstChatToStartWaiting(t *testing.T) {
	st := feedStore(t)
	held, msgs, _, err := gatherUnread(t.Context(), st, "ou_me", nil, nil)
	require.NoError(t, err)
	require.Empty(t, held)
	require.Empty(t, msgs)

	require.NoError(t, st.EnsureChat(t.Context(), "oc_late", 1))
	say(t, st, "om_l1", "oc_late", 100, "第一条")
	owing(t, st, "om_l1")
	chats := []store.Chat{{ChatID: "oc_late", Name: "后来的", UnreadCount: 1}}

	secs, msgs, _, err := gatherUnread(t.Context(), st, "ou_me", chats, held)
	require.NoError(t, err)

	require.Len(t, secs, 1)
	require.Equal(t, []string{"oc_late"}, []string{secs[0].chatID})
	require.Equal(t, []string{"om_l1"}, idsOf(msgs))
}

func TestJoinUnread_AChatThatStoppedWaitingLeavesThePage(t *testing.T) {
	held := []unreadSection{{chatID: "oc_platform", anchorMs: 100}, {chatID: "oc_project", anchorMs: 300}}

	out := joinUnread(held, []unreadSection{{chatID: "oc_project", anchorMs: 300}})

	require.Equal(t, []string{"oc_project"}, []string{out[0].chatID})
	require.Len(t, out, 1)
}

// The cap belongs to the joined page rather than to the anchor set: a chat the
// reader is already looking at keeps its place however old a newcomer's
// backlog is.
func TestJoinUnread_AHeldChatIsNotEvictedByAnOlderNewcomer(t *testing.T) {
	var held, fresh []unreadSection
	for i := range unreadFeedChats {
		held = append(held, unreadSection{chatID: "oc_held_" + string(rune('a'+i)), anchorMs: int64(100 + i)})
	}
	for i := range 5 {
		fresh = append(fresh, unreadSection{chatID: "oc_new_" + string(rune('a'+i)), anchorMs: int64(i + 1)})
	}

	out := joinUnread(held, append(fresh, held...))

	require.Equal(t, held, out, "the page the reader is on keeps every row of it")
}

// The page reaches no further than the cap however many chats join it.
func TestJoinUnread_ANewcomerPastTheCapIsLeftOut(t *testing.T) {
	held := make([]unreadSection, 0, unreadFeedChats)
	for i := range unreadFeedChats {
		held = append(held, unreadSection{chatID: "oc_" + string(rune('a'+i)), anchorMs: int64(i + 1)})
	}
	fresh := append(slices.Clone(held), unreadSection{chatID: "oc_late", anchorMs: 1})

	out := joinUnread(held, fresh)

	require.Len(t, out, unreadFeedChats)
	require.Equal(t, "oc_a", out[0].chatID)
	require.NotEqual(t, "oc_late", out[len(out)-1].chatID)
}

// The held page is the model's own slice, and the reader is looking at it.
func TestJoinUnread_TheHeldPageIsNotWrittenThrough(t *testing.T) {
	held := make([]unreadSection, 1, 4)
	held[0] = unreadSection{chatID: "oc_platform", anchorMs: 100}

	out := joinUnread(held, []unreadSection{{chatID: "oc_platform", anchorMs: 100}, {chatID: "oc_late", anchorMs: 10}})

	require.Equal(t, []string{"oc_platform", "oc_late"}, []string{out[0].chatID, out[1].chatID})
	require.Empty(t, held[:cap(held)][1].chatID, "the newcomer went into a slice of its own")
}

func TestUnreadSection_TheRuleAnswersWhatTheChatsRowWould(t *testing.T) {
	s := unreadSection{name: "平台组", count: 3, atMe: true, muted: true}
	out := ansi.Strip(s.rule())
	require.Equal(t, "平台组 · 3 @ "+muteGlyph, out,
		"the page runs with the chats list out of sight, so the rule carries its marks")
	require.Contains(t, s.rule(), stMentionMe.Render("@"), "the same badge the chat row wears")
}

func TestUnreadSection_ARuleWithNothingToAddIsJustTheName(t *testing.T) {
	require.Equal(t, "项目协作群", ansi.Strip(unreadSection{name: "项目协作群"}.rule()))
	require.Equal(t, "oc_nameless", ansi.Strip(unreadSection{chatID: "oc_nameless"}.rule()),
		"an unnamed chat falls back to its id, the way label does")
}

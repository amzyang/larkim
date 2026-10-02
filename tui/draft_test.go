package tui

import (
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// draftModel is a model over a real store holding two chats, each with one
// message, so a draft can be watched across a switch between them.
func draftModel(t *testing.T) (Model, *store.Store) {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	for _, c := range []struct{ id, name, body string }{
		{"oc_group", "平台组", "发布计划定了吗"},
		{"oc_peer", "张三", "在吗"},
	} {
		require.NoError(t, st.EnsureChat(ctx, c.id, 1))
		_, err = st.UpsertMessages(ctx, []store.Message{{
			MessageID: "om_" + c.id, ChatID: c.id, MsgType: "text", SenderID: "ou_x",
			SenderName: c.name, ContentRaw: `{"text":"` + c.body + `"}`, CreateMs: 100, UpdateMs: 100,
		}}, 1)
		require.NoError(t, err)
	}
	m := New(Deps{Store: st, Self: "ou_me"})
	m.width, m.height = 120, 36
	return m, st
}

// enter walks the model into a chat the way a cursor move does: ask for the
// page, run what that asks for, then play the page back in.
func enter(t *testing.T, m Model, st *store.Store, chatID string) Model {
	t.Helper()
	cmd := m.openChat(chatID)
	collect(cmd)
	return arrive(t, m, st, chatID)
}

// The composer is one widget shared by every chat. Without a per-chat draft,
// a half-written message follows the reader into the next chat and Enter sends
// it to the wrong person.
func TestEnterChat_DraftDoesNotFollowTheReaderIntoTheNextChat(t *testing.T) {
	m, st := draftModel(t)
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("半句话")

	m = enter(t, m, st, "oc_peer")

	assert.Empty(t, m.input.Value(), "the next chat opens on its own empty composer")
}

func TestEnterChat_DraftComesBackWithTheChatItWasTypedIn(t *testing.T) {
	m, st := draftModel(t)
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("半句话")

	m = enter(t, m, st, "oc_peer")
	m = enter(t, m, st, "oc_group")

	assert.Equal(t, "半句话", m.input.Value())
}

// The draft is on disk, not in the model, so it outlives the process.
func TestSaveComposer_DraftOutlivesTheProcess(t *testing.T) {
	m, st := draftModel(t)
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("重开还在")

	collect(m.saveComposer())

	fresh := New(Deps{Store: st, Self: "ou_me"})
	fresh.width, fresh.height = 120, 36
	fresh = enter(t, fresh, st, "oc_group")
	assert.Equal(t, "重开还在", fresh.input.Value())
}

// A draft that answers something is only that draft while it still says what
// it answers.
func TestEnterChat_DraftCarriesItsQuoteBack(t *testing.T) {
	m, st := draftModel(t)
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("好的")
	m.setQuote(&m.msgs[0], true)

	m = enter(t, m, st, "oc_peer")
	m = enter(t, m, st, "oc_group")

	require.NotNil(t, m.replyTo)
	assert.Equal(t, "om_oc_group", m.replyTo.MessageID)
	assert.True(t, m.inThrd)
}

// A quote whose message is no longer on the page cannot be drawn, so the text
// comes back without it rather than pointing at nothing.
func TestEnterChat_DraftQuotingAMissingMessageKeepsOnlyItsText(t *testing.T) {
	m, st := draftModel(t)
	ctx := t.Context()
	require.NoError(t, st.SaveDraft(ctx, store.Draft{
		ChatID: "oc_group", Text: "好的", ReplyTo: "om_elsewhere", InThread: true,
	}, 1))

	m = enter(t, m, st, "oc_group")

	assert.Equal(t, "好的", m.input.Value())
	assert.Nil(t, m.replyTo)
}

// A reload is not an entry: a tick landing a new message must not stomp what
// is being typed.
func TestMessagesLoaded_ReloadDoesNotStompTheComposer(t *testing.T) {
	m, st := draftModel(t)
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("正在打字")

	m = arrive(t, m, st, "oc_group") // same chat, no pendingChat: a reload

	assert.Equal(t, "正在打字", m.input.Value())
}

// Sending empties the composer, and every path that persists writes that
// emptiness through, so no marker survives the send.
func TestSaveComposer_SendingLeavesNoDraftBehind(t *testing.T) {
	m, st := draftModel(t)
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("发出去")
	collect(m.saveComposer())
	m.input.Reset()

	collect(m.saveComposer())

	all, err := st.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, all)
}

// The open chat draws no draft at all: its row sits beside the composer the
// draft is in, and the client's list leaves the conversation being typed in
// bare of its own draft.
func TestDraftForRow_TheOpenChatDrawsNoDraft(t *testing.T) {
	m, st := draftModel(t)
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("正在打字")
	m.drafts = map[string]store.Draft{"oc_peer": {ChatID: "oc_peer", Text: "别处的"}}

	assert.Empty(t, m.draftForRow("oc_group"), "the open row leaves the composer to say it")
	assert.Equal(t, "别处的", m.draftForRow("oc_peer").Text, "every other row reads the map")
}

// Whether a draft counts is store.Draft.Empty's to say, so the open row and
// every other row answer it the same way: a composer holding only blanks marks
// nothing, and marks nothing still once the reader has moved on.
func TestDraftForRow_BlanksAreNotADraft(t *testing.T) {
	m, st := draftModel(t)
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("   ")

	assert.Empty(t, selfMark(m.draftForRow("oc_group")))

	collect(m.saveComposer())
	all, err := st.Drafts(t.Context())
	require.NoError(t, err)
	assert.Empty(t, all, "blanks leave no row behind to mark the chat with later")
}

func TestSelfMark_DrawnOnlyForAChatHoldingADraft(t *testing.T) {
	assert.Empty(t, selfMark(store.Draft{}))
	// Styled, so the pair the glyph and its cell make is read behind the
	// escapes that underline puts around each of them.
	assert.Contains(t, ansi.Strip(selfMark(store.Draft{Text: "半句"})), draftGlyph)
}

// The marker belongs to the chat, not to its newest message: the summary line
// may have moved on while somebody is still waiting on the reader.
func TestChatSummaryLine_AtMeIsDrawnWhateverTheSummarySays(t *testing.T) {
	c := store.Chat{ChatID: "oc_group", Name: "平台组", ChatMode: "group",
		LastSenderName: "张三", LastContent: "别的事", LastRenderedAt: 1, UnreadMention: true}

	line, _ := chatSummaryLine(c, store.Draft{}, 0, gistOf(listRow{chat: c}, "ou_me", emojiPics{}), 36)

	assert.Contains(t, line, "@", "the chat wears the badge even though its newest message names nobody")
}

func TestChatSummaryLine_NoBadgeWithoutAnUnreadMention(t *testing.T) {
	c := store.Chat{ChatID: "oc_group", Name: "平台组", ChatMode: "group",
		LastSenderName: "张三", LastContent: "别的事", LastRenderedAt: 1}

	line, _ := chatSummaryLine(c, store.Draft{}, 0, gistOf(listRow{chat: c}, "ou_me", emojiPics{}), 36)

	assert.NotContains(t, ansi.Strip(line), "@")
}

// Being named is the louder of the two, so it takes the slot the reactions
// would have had.
func TestChatSummaryLine_AtMeOutranksTheReactionChips(t *testing.T) {
	c := reactedP2P("OK")
	c.UnreadMention = true

	line, segs := chatSummaryLine(c, store.Draft{}, 0, gistOf(listRow{chat: c}, "ou_me", emojiPics{}), 36)

	assert.Empty(t, segs, "the badge is text, so the line stays one string")
	assert.Contains(t, line, "@")
	assert.NotContains(t, ansi.Strip(line), chipLeft, "the badge stands in place of the reactions")
}

// draftChatRow is what a group with a last message in it is laid out from:
// the latest is somebody else's words, so a draft standing in for it is the
// reader's over the chat's.
func draftChatRow() store.Chat {
	return store.Chat{ChatID: "oc_group", Name: "平台组", ChatMode: "group",
		LastMessageID: "om_1", LastSenderName: "张三", LastContent: "发布计划定了吗", LastRenderedAt: 1}
}

// The saved draft stands in for the last message with no sender's name before
// it: the pencil in the marker slot already says whose words they are.
func TestChatSummaryLine_DraftGistCarriesNoSenderPrefix(t *testing.T) {
	c := draftChatRow()
	d := store.Draft{ChatID: c.ChatID, Text: "半句话"}
	g := gistOf(listRow{chat: c}, "ou_me", emojiPics{}).withDraft(d)

	line, _ := chatSummaryLine(c, d, 0, g, 36)

	stripped := ansi.Strip(line)
	assert.Contains(t, stripped, "半句话")
	assert.Contains(t, stripped, draftGlyph)
	assert.NotContains(t, stripped, "张三:")
}

// A draft goes through the same laying out a summary does, so the room runs
// out on it the same way and the mute mark keeps its edge.
func TestChatSummaryLine_DraftGistTruncatesLikeASummary(t *testing.T) {
	c := draftChatRow()
	c.Muted = true
	d := store.Draft{ChatID: c.ChatID, Text: strings.Repeat("a", 40) + "尾"}
	g := gistOf(listRow{chat: c}, "ou_me", emojiPics{}).withDraft(d)

	line, _ := chatSummaryLine(c, d, 0, g, 36)

	stripped := ansi.Strip(line)
	assert.NotContains(t, stripped, "尾", "the line gives the draft no more room than a summary")
	assert.Contains(t, stripped, muteGlyph, "the mute mark keeps the far edge")
	assert.Equal(t, 36, ansi.StringWidth(stripped))
}

// A draft written over more than one line is one line on the row: the row has
// two, and the second is the gist's alone.
func TestChatSummaryLine_MultiLineDraftIsOneLine(t *testing.T) {
	c := draftChatRow()
	d := store.Draft{ChatID: c.ChatID, Text: "第一行\n第二行\t字"}
	g := gistOf(listRow{chat: c}, "ou_me", emojiPics{}).withDraft(d)

	line, _ := chatSummaryLine(c, d, 0, g, 60)

	stripped := ansi.Strip(line)
	assert.Contains(t, stripped, "第一行 第二行 字")
	assert.NotContains(t, stripped, "\n")
}

// Saving is what puts a draft on its row, and the open chat's row is bare
// even then: the composer beside it already holds the words.
func TestRenderChats_TypingAloneDoesNotChangeTheGist(t *testing.T) {
	m, st := draftModel(t)
	m = deliver(t, m, loadChats(m.deps))
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("半句话")

	out := ansi.Strip(m.renderChats(m.chatsBodyHeight()))

	assert.NotContains(t, out, draftGlyph, "the composer beside the row is the draft")
	assert.NotContains(t, out, "半句话", "an unsaved draft is not yet the row's to say")
	assert.Contains(t, out, "发布计划定了吗")
}

// A save lands the draft on disk, but the row of the chat still open stays
// bare: another row — after a switch — is where the words belong.
func TestRenderChats_TheOpenChatRowStaysBareEvenSaved(t *testing.T) {
	m, st := draftModel(t)
	m = deliver(t, m, loadChats(m.deps))
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("abc")
	m = deliver(t, m, m.saveComposer())

	out := ansi.Strip(m.renderChats(m.chatsBodyHeight()))

	assert.NotContains(t, out, "abc")
	assert.NotContains(t, out, draftGlyph)
	assert.Contains(t, out, "发布计划定了吗")
}

// The row a switch just left carries its draft without waiting for a listing
// no revision bump will bring: drafts sit outside data_rev.
func TestRenderChats_SavedDraftIsTheGistLine(t *testing.T) {
	m, st := draftModel(t)
	m = deliver(t, m, loadChats(m.deps))
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("半句话")
	m = deliver(t, m, m.saveComposer())
	m = enter(t, m, st, "oc_peer")

	out := ansi.Strip(m.renderChats(m.chatsBodyHeight()))

	assert.Contains(t, out, "半句话")
	assert.NotContains(t, out, "发布计划定了吗")
}

// Clearing the composer writes the emptiness through, so the row falls back
// to the chat's last message and the pencil goes with the draft.
func TestRenderChats_ClearedDraftRevertsToTheLastMessage(t *testing.T) {
	m, st := draftModel(t)
	m = deliver(t, m, loadChats(m.deps))
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("abc")
	m = deliver(t, m, m.saveComposer())
	m = enter(t, m, st, "oc_peer")
	require.Contains(t, ansi.Strip(m.renderChats(m.chatsBodyHeight())), "abc")

	m = enter(t, m, st, "oc_group")
	m.input.Reset()
	m = deliver(t, m, m.saveComposer())
	m = enter(t, m, st, "oc_peer")

	out := ansi.Strip(m.renderChats(m.chatsBodyHeight()))
	assert.NotContains(t, out, "abc")
	assert.Contains(t, out, "发布计划定了吗")
	assert.NotContains(t, out, draftGlyph)
}

// The maps start nil — no listing has landed yet — and a blur-save in the
// first moments of a session must not find that out the hard way.
func TestDraftSavedMsg_PatchesTheDraftsMap(t *testing.T) {
	m, _ := draftModel(t)

	next, _ := m.Update(draftSavedMsg{draft: store.Draft{ChatID: "oc_group", Text: "半句话"}})
	m = next.(Model)
	assert.Equal(t, "半句话", m.drafts["oc_group"].Text)

	next, _ = m.Update(draftSavedMsg{draft: store.Draft{ChatID: "oc_group"}})
	m = next.(Model)
	assert.NotContains(t, m.drafts, "oc_group", "an empty write is a delete, so the gist falls back")
}

// A write the store refused leaves the maps a refresh behind: a gist drawing a
// draft the disk never took would promise words the box has lost.
func TestDraftSavedMsg_FailedWriteLeavesTheMapsAlone(t *testing.T) {
	m, _ := draftModel(t)

	next, _ := m.Update(draftSavedMsg{draft: store.Draft{ChatID: "oc_group", Text: "半句话"}, err: errors.New("disk full")})
	m = next.(Model)

	assert.Empty(t, m.drafts)
}

// The pencil wears the client's draft colour on a dotted underline, so it
// reads as the reader's own words still to send rather than another mark the
// chat collected. The underline is the pencil's alone: the style splits per
// cluster, and a styled separator would re-assert it straight — a line one
// cell off the pencil it belongs to.
func TestSelfMark_WearsTheDraftColourUnderlined(t *testing.T) {
	out := selfMark(store.Draft{Text: "半句话"})

	assert.Contains(t, out, "38;2;245;74;69", "the red the client paints a draft with")
	assert.Contains(t, out, "4:4", "a dotted underline, not the filter's straight one")
	assert.True(t, strings.HasSuffix(out, "\x1b[m"+enSpace), "the separator cell stays clear of the underline")
}

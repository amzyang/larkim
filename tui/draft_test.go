package tui

import (
	"context"
	"path/filepath"
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
	ctx := context.Background()
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
	m.setReply(&m.msgs[0], true)

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
	ctx := context.Background()
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

	all, err := st.Drafts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, all)
}

func TestDraftForRow_OpenChatAnswersFromTheComposer(t *testing.T) {
	m, st := draftModel(t)
	m = enter(t, m, st, "oc_group")
	m.input.SetValue("  正在打字  ")
	m.drafts = map[string]store.Draft{"oc_peer": {ChatID: "oc_peer", Text: "别处的"}}

	assert.Equal(t, "  正在打字  ", m.draftForRow("oc_group").Text, "the open row reads the live composer")
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
	all, err := st.Drafts(context.Background())
	require.NoError(t, err)
	assert.Empty(t, all, "blanks leave no row behind to mark the chat with later")
}

func TestSelfMark_DrawnOnlyForAChatHoldingADraft(t *testing.T) {
	assert.Empty(t, selfMark(store.Draft{}))
	assert.Contains(t, selfMark(store.Draft{Text: "半句"}), draftGlyph)
}

// The marker belongs to the chat, not to its newest message: the summary line
// may have moved on while somebody is still waiting on the reader.
func TestChatSummaryLine_AtMeIsDrawnWhateverTheSummarySays(t *testing.T) {
	c := store.Chat{ChatID: "oc_group", Name: "平台组", ChatMode: "group",
		LastSenderName: "张三", LastContent: "别的事", LastRenderedAt: 1, UnreadMention: true}

	line, _ := chatSummaryLine(c, store.Draft{}, "ou_me", emojiPics{}, 36)

	assert.Contains(t, line, "@", "the chat wears the badge even though its newest message names nobody")
}

func TestChatSummaryLine_NoBadgeWithoutAnUnreadMention(t *testing.T) {
	c := store.Chat{ChatID: "oc_group", Name: "平台组", ChatMode: "group",
		LastSenderName: "张三", LastContent: "别的事", LastRenderedAt: 1}

	line, _ := chatSummaryLine(c, store.Draft{}, "ou_me", emojiPics{}, 36)

	assert.NotContains(t, ansi.Strip(line), "@")
}

// Being named is the louder of the two, so it takes the slot the reactions
// would have had.
func TestChatSummaryLine_AtMeOutranksTheReactionChips(t *testing.T) {
	c := reactedP2P("OK")
	c.UnreadMention = true

	line, segs := chatSummaryLine(c, store.Draft{}, "ou_me", emojiPics{}, 36)

	assert.Empty(t, segs, "the badge is text, so the line stays one string")
	assert.Contains(t, line, "@")
}

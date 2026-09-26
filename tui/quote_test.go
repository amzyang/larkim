package tui

import (
	"context"
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

// TestLoadMeta_LoadsTheQuotedParents covers the case the page cannot: the
// message a reply answers is older than the page it arrives on, so it has to
// be fetched by id, together with its sender's account suffix.
func TestLoadMeta_LoadsTheQuotedParents(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })

	require.NoError(t, st.UpsertContacts(ctx, []store.Contact{
		{OpenID: "ou_a", Name: "李明", Email: "liming01@example.com"},
	}, 1))
	_, err = st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_old", ChatID: "oc_a", CreateMs: 10, MessagePosition: 1, SenderID: "ou_a", SenderName: "李明", RawJSON: "{}"},
		{MessageID: "om_new", ChatID: "oc_a", CreateMs: 20, MessagePosition: 2, SenderID: "ou_b", SenderName: "唐婉", ReplyTo: "om_old", RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, st.UpdateRendered(ctx, "om_old", "瞅一眼", "", "", 2))
	require.NoError(t, st.UpdateRendered(ctx, "om_new", "好的", "", "", 2))

	page, err := st.ListMessages(ctx, store.MessageQuery{ChatID: "oc_a", SinceMs: 15})
	require.NoError(t, err)
	require.Len(t, page, 1, "the parent is off the page")

	meta, err := loadMeta(ctx, st, "ou_me", page)
	require.NoError(t, err)
	require.Equal(t, "瞅一眼", meta.parents["om_old"].Content)
	require.Equal(t, "01", meta.suffix["ou_a"], "the quoted sender is named as the lists name them")

	st2 := msgStyle{width: 60, self: "ou_me", now: testNow, suffix: meta.suffix, parents: meta.parents}
	require.Contains(t, rowText(renderRows(page, st2)), "▏李明01: 瞅一眼")
}

func TestRenderRows_QuoteReadsRecalledAndUnrenderedParents(t *testing.T) {
	gone := store.Message{MessageID: "om_gone", SenderName: "孙琪", Content: "原文", Deleted: true, RenderedAt: 1}
	raw := store.Message{MessageID: "om_raw", SenderName: "孙琪", MsgType: "image", ContentRaw: `{"image_key":"img_1"}`}
	msgs := []store.Message{
		{MessageID: "om_a", SenderName: "李四", Content: "无关", CreateMs: msgAt(23, 9, 0), RenderedAt: 1},
		{MessageID: "om_b", SenderName: "沈知远", Content: "答一", ReplyTo: "om_gone", CreateMs: msgAt(23, 9, 1), RenderedAt: 1},
		{MessageID: "om_c", SenderName: "沈知远", Content: "答二", ReplyTo: "om_raw", CreateMs: msgAt(23, 9, 2), RenderedAt: 1},
	}
	st := baseStyle()
	st.parents = map[string]store.Message{"om_gone": gone, "om_raw": raw}
	out := ansi.Strip(rowText(renderRows(msgs, st)))
	require.Contains(t, out, "▏孙琪: (Recalled)")
	require.Contains(t, out, "▏孙琪: [图片]", "a parent still waiting for its rendering is named by its type")
}

// quoteModel is a chat whose last message answers its first, with one message
// in between so the quote line is drawn at all.
func quoteModel(t *testing.T) Model {
	t.Helper()
	m := New(Deps{Self: "ou_me"})
	m.width, m.height = 100, 30
	m.chatID = "oc_a"
	m.chats = []store.Chat{{ChatID: "oc_a", Name: "平台组", ChatMode: "group"}}
	m.msgsBase = []store.Message{
		{MessageID: "om_root", ChatID: "oc_a", SenderID: "ou_a", SenderName: "李四", Content: "原文", CreateMs: msgAt(23, 9, 0), RenderedAt: 1},
		{MessageID: "om_mid", ChatID: "oc_a", SenderID: "ou_b", SenderName: "王五", Content: "插话", CreateMs: msgAt(23, 9, 1), RenderedAt: 1},
		{MessageID: "om_reply", ChatID: "oc_a", SenderID: "ou_b", SenderName: "王五", Content: "答", ReplyTo: "om_root", CreateMs: msgAt(23, 9, 2), RenderedAt: 1},
	}
	m.meta = msgMeta{parents: map[string]store.Message{"om_root": m.msgsBase[0]}}
	m.applyOutbox()
	m.layout()
	m.focus, m.msgIdx = paneMessages, len(m.msgs)-1
	m.rebuildMessages()
	return m
}

// pressQuote presses the quote line of the messages pane in the coordinates
// the terminal reports, and keeps the model clickAt throws away.
func pressQuote(t *testing.T, m Model) (Model, tea.Cmd) {
	t.Helper()
	for i, r := range m.msgRows {
		for _, z := range r.zones {
			if z.jump == "" {
				continue
			}
			next, cmd := m.onClick(tea.Mouse{Button: tea.MouseLeft,
				X: chatsWidth + 1 + z.x0, Y: i - m.msgTop + 1 + msgHeaderHeight})
			return next.(Model), cmd
		}
	}
	t.Fatal("no quote line on the page")
	return m, nil
}

func TestRenderRows_QuoteLineCarriesAJumpZone(t *testing.T) {
	parent := store.Message{MessageID: "om_root", SenderName: "李四", Content: "原文", RenderedAt: 1}
	msgs := []store.Message{
		{MessageID: "om_mid", SenderName: "王五", Content: "插话", CreateMs: msgAt(23, 9, 1), RenderedAt: 1},
		{MessageID: "om_reply", SenderName: "王五", Content: "答", ReplyTo: "om_root", CreateMs: msgAt(23, 9, 2), RenderedAt: 1},
	}
	st := baseStyle()
	st.parents = map[string]store.Message{"om_root": parent}

	var quote msgRow
	for _, r := range renderRows(msgs, st) {
		if strings.Contains(ansi.Strip(r.text), "▏李四: 原文") {
			quote = r
			continue
		}
		require.Empty(t, r.zones, "the quote line is the only one that leads back")
	}
	require.Len(t, quote.zones, 1)
	z := quote.zones[0]
	require.Equal(t, "om_root", z.jump)
	require.True(t, z.live())
	require.Equal(t, quote.lead.cols(), z.x0, "the zone starts where the text does")
	require.Equal(t, quote.lead.cols()+ansi.StringWidth(ansi.Strip(quote.text)), z.x1, "the whole line is the target")
}

func TestOnClick_QuoteLineJumpsToTheMessageItNames(t *testing.T) {
	m := quoteModel(t)
	m, cmd := pressQuote(t, m)
	require.Nil(t, cmd, "a message already on the page needs no page")
	require.Equal(t, 0, m.msgIdx, "the cursor stands on the quoted message")
	require.Equal(t, paneMessages, m.focus)
}

func TestJumpToQuoted_FromTheThreadLandsInTheChatPane(t *testing.T) {
	m := quoteModel(t)
	// A thread reply can answer something said in the chat itself, which the
	// thread pane does not list.
	m.rightKind, m.threadID, m.thread = rightThread, "omt_1", m.msgs[2:]
	m.focus, m.threadIdx = paneThread, 0
	m.threadMeta = m.meta
	m.layout()

	next, cmd := m.jumpToQuoted(paneThread, "om_root")
	m = next.(Model)
	require.Nil(t, cmd)
	require.Equal(t, paneMessages, m.focus)
	require.Equal(t, 0, m.msgIdx)
}

func TestJumpToQuoted_ParentOffThePageReopensTheChatAtIt(t *testing.T) {
	m := quoteModel(t)
	parent := store.Message{MessageID: "om_old", ChatID: "oc_a", SenderName: "李四", Content: "很早以前", CreateMs: msgAt(21, 9, 0), RenderedAt: 1}
	m.meta.parents["om_old"] = parent

	next, cmd := m.jumpToQuoted(paneMessages, "om_old")
	m = next.(Model)
	require.NotNil(t, cmd)
	require.Equal(t, "om_old", m.pendingSelect.id)
	require.Equal(t, "oc_a", m.pendingChat)
	require.Equal(t, parent.CreateMs, m.pendingSince, "the page is cut so the quoted message opens it")
}

func TestJumpToQuoted_UnsyncedParentWithoutTheLockSaysSo(t *testing.T) {
	m := quoteModel(t)
	next, cmd := m.jumpToQuoted(paneMessages, "om_elsewhere")
	m = next.(Model)
	require.Nil(t, cmd, "only the process holding the lock may write")
	require.Contains(t, m.notice, "synced")
	require.Empty(t, m.pendingSelect.id)
}

func TestJumpToQuoted_UnsyncedParentIsPulledThenOpened(t *testing.T) {
	ctx := t.Context()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	require.NoError(t, st.EnsureChat(ctx, "oc_a", 1))

	f := larkcli.NewFake()
	f.AddMessage(larkcli.RawMessage{
		MessageID: "om_old", ChatID: "oc_a", MsgType: "text", CreateTime: 50, UpdateTime: 50,
		Sender: larkcli.RawSender{ID: "ou_a", SenderType: "user"},
		Body:   larkcli.RawBody{Content: `{"text":"很早以前"}`},
	})
	f.Rendered["om_old"] = larkcli.RenderedMessage{MessageID: "om_old", ChatID: "oc_a", MsgType: "text", Content: "很早以前"}

	m := quoteModel(t)
	m.deps.Store = st
	m.deps.Syncer = &sync.Syncer{Client: f, Store: st, Clock: sync.RealClock{}}

	next, cmd := m.jumpToQuoted(paneMessages, "om_old")
	m = next.(Model)
	require.Contains(t, m.notice, "fetching")
	require.NotNil(t, cmd)

	next, _ = m.update(cmd())
	m = next.(Model)
	require.Equal(t, "om_old", m.pendingSelect.id)
	require.Equal(t, "oc_a", m.pendingChat)
	require.Equal(t, int64(50), m.pendingSince)

	got, err := st.GetMessage(ctx, "om_old")
	require.NoError(t, err)
	require.Equal(t, "很早以前", got.Content)
}

package tui

import (
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
	"github.com/stretchr/testify/require"
)

// bundleStore holds one expanded bundle: two messages and a nested bundle of
// its own, all of them from a chat larkim never synced.
func bundleStore(t *testing.T) *store.Store {
	t.Helper()
	st, err := store.Open(filepath.Join(t.TempDir(), "t.db"))
	require.NoError(t, err)
	t.Cleanup(func() { st.Close() })
	ctx := t.Context()
	_, err = st.UpsertMessages(ctx, []store.Message{
		{MessageID: "om_fwd", ChatID: "oc_a", MsgType: "merge_forward", CreateMs: 100,
			ContentRaw: `{"text":"Merged and Forwarded Message"}`, RawJSON: "{}"},
	}, 1)
	require.NoError(t, err)
	require.NoError(t, st.AddForwardRoots(ctx, []string{"om_fwd"}))
	require.NoError(t, st.SaveForwarded(ctx, "om_fwd", []store.Forwarded{
		{UpperMessageID: "om_fwd", MessageID: "om_a", ChatID: "oc_src", MsgType: "text",
			SenderID: "ou_a", SenderName: "张三", CreateMs: 10, ContentRaw: `{"text":"预算定了"}`},
		{UpperMessageID: "om_fwd", MessageID: "om_pic", ChatID: "oc_src", MsgType: "image", Seq: 1,
			SenderID: "ou_b", SenderName: "李四", CreateMs: 20, ContentRaw: `{"image_key":"img_inside"}`},
		{UpperMessageID: "om_fwd", MessageID: "om_inner", ChatID: "oc_src", MsgType: "merge_forward", Seq: 2,
			SenderID: "ou_b", SenderName: "李四", CreateMs: 30, ContentRaw: `{"text":"Merged and Forwarded Message"}`},
		{UpperMessageID: "om_inner", MessageID: "om_deep", ChatID: "oc_other", MsgType: "text",
			SenderID: "ou_a", SenderName: "张三", CreateMs: 5, ContentRaw: `{"text":"里面那条"}`},
	}, 200))
	return st
}

func TestLoadForward_ListsOneLevelAndLeavesTheNestedBundleAsARow(t *testing.T) {
	st := bundleStore(t)
	d := Deps{Store: st, Self: "ou_me"}

	msg := loadForward(d, "om_fwd", "om_fwd")().(forwardLoadedMsg)

	ids := make([]string, 0, len(msg.msgs))
	for _, x := range msg.msgs {
		ids = append(ids, x.MessageID)
	}
	require.Equal(t, []string{"om_a", "om_pic", "om_inner"}, ids,
		"one frame is one level; the nested bundle opens a frame of its own")
	require.Equal(t, 3, msg.gist.ChildCount)
	require.True(t, msg.gist.Expanded)
	require.Equal(t, 3, msg.meta.forwards["om_inner"].ChildCount+2,
		"the nested line is counted from the children that came down with the tree")
}

func TestLoadForward_ANestedLevelListsItsOwnChildren(t *testing.T) {
	st := bundleStore(t)

	msg := loadForward(Deps{Store: st}, "om_fwd", "om_inner")().(forwardLoadedMsg)

	require.Len(t, msg.msgs, 1)
	require.Equal(t, "om_deep", msg.msgs[0].MessageID)
	require.Equal(t, "oc_other", msg.msgs[0].ChatID, "a child keeps the chat it was actually sent to")
}

func TestLoadForward_APictureChildGetsOnlyItsOwnOfTheBundlesResources(t *testing.T) {
	st := bundleStore(t)
	ctx := t.Context()
	// Every picture in the tree is registered against the bundle, because the
	// resource endpoint refuses a child's id.
	require.NoError(t, st.AddPendingResources(ctx, []store.ResourceRef{
		{MessageID: "om_fwd", FileKey: "img_inside", Type: "image"},
		{MessageID: "om_fwd", FileKey: "img_elsewhere", Type: "image"},
	}))

	msg := loadForward(Deps{Store: st}, "om_fwd", "om_fwd")().(forwardLoadedMsg)

	require.Len(t, msg.meta.res["om_pic"], 1)
	require.Equal(t, "img_inside", msg.meta.res["om_pic"][0].FileKey)
	require.Empty(t, msg.meta.res["om_a"],
		"handing every child the whole list would make o offer every picture in the bundle on every row")
}

func TestForwardedRow_AnImageChildPlacesItsPicture(t *testing.T) {
	x := forwardedRow(store.Forwarded{MessageID: "om_pic", MsgType: "image",
		ContentRaw: `{"image_key":"img_inside"}`, CreateMs: 20})

	require.NotZero(t, x.RenderedAt, "lark-cli never renders a child, so the row carries its own")
	keys, _ := splitImages(x.Content)
	require.Equal(t, []string{"img_inside"}, keys)
}

func TestForwardedRow_APostKeepsTheDimStandIn(t *testing.T) {
	// A post's rendering is markdown, which only lark-cli builds. Feeding the
	// markdown path a flattened line would turn the sender's punctuation into
	// formatting, so the words come through the stand-in instead.
	x := forwardedRow(store.Forwarded{MessageID: "om_post", MsgType: "post",
		ContentRaw: `{"title":"周报","content":[[{"tag":"text","text":"3 * 4 * 5"}]]}`, CreateMs: 20})

	require.Zero(t, x.RenderedAt)
	require.Contains(t, pendingText(x.MsgType, x.ContentRaw), "3 * 4 * 5")
}

func TestOnForwardLoaded_AnUnexpandedBundleIsAskedForNow(t *testing.T) {
	st := bundleStore(t)
	m := sized(140, 36)
	m.deps = Deps{Store: st, Syncer: &sync.Syncer{Store: st}}
	m.rightKind, m.threadID, m.rightRoot = rightForward, "om_new", "om_new"

	next, cmd := m.onForwardLoaded(forwardLoadedMsg{bundleID: "om_new", level: "om_new"})
	m = next.(Model)

	require.Equal(t, noteExpanding, m.rightNote)
	require.NotNil(t, cmd, "the reader who opened it is why the interactive lane exists")
}

func TestOnForwardLoaded_WithoutTheSyncLockItSaysToWait(t *testing.T) {
	m := sized(140, 36)
	m.rightKind, m.threadID, m.rightRoot = rightForward, "om_new", "om_new"

	next, cmd := m.onForwardLoaded(forwardLoadedMsg{bundleID: "om_new", level: "om_new"})

	require.Equal(t, "还没展开，等同步", next.(Model).rightNote,
		"only the process holding the sync lock may write")
	require.Nil(t, cmd)
}

func TestOnForwardLoaded_ARefusedBundleSaysSoRatherThanShowingAnEmptyFrame(t *testing.T) {
	m := sized(140, 36)
	m.rightKind, m.threadID, m.rightRoot = rightForward, "om_gone", "om_gone"

	next, cmd := m.onForwardLoaded(forwardLoadedMsg{bundleID: "om_gone", level: "om_gone",
		gist: store.ForwardGist{Refused: true}})

	require.Equal(t, noteRefused, next.(Model).rightNote)
	require.Nil(t, cmd, "a forward is frozen: asking again cannot change the answer")
}

func TestOnForwardLoaded_IgnoresAnAnswerForAFrameTheReaderLeft(t *testing.T) {
	// The reader popped back to the thread while the children were in
	// flight; the answer names a frame that is no longer on screen.
	m := onThread(t)
	was := m.thread

	next, _ := m.onForwardLoaded(forwardLoadedMsg{bundleID: "om_fwd", level: "om_fwd",
		msgs: []store.Message{{MessageID: "om_a"}}})

	require.Equal(t, rightThread, next.(Model).rightKind)
	require.Equal(t, was, next.(Model).thread)
}

func TestForwardFrame_AChildCannotBeAnswered(t *testing.T) {
	// A child is a message of another chat. Replying, reacting or recalling
	// would put the result somewhere the reader is not looking.
	m := onThread(t)
	m, _ = m.pushRight(rightFrame{kind: rightForward, id: "om_fwd", root: "om_fwd"})
	m.thread = []store.Message{{MessageID: "om_a", ChatID: "oc_src", SenderID: "ou_me",
		SenderName: "林岚", Content: "预算定了", RenderedAt: 1}}
	m.rebuildThread()

	for name, run := range map[string]func() (Model, string){
		"reply":  func() (Model, string) { n, _ := m.onNormalKey("r"); return n.(Model), "" },
		"react":  func() (Model, string) { n, _ := m.onNormalKey("e"); return n.(Model), "" },
		"recall": func() (Model, string) { n, _ := m.onNormalKey("D"); return n.(Model), "" },
	} {
		got, _ := run()
		require.Contains(t, got.notice, "belongs to its own chat", name)
		require.Equal(t, modeNormal, got.mode, name)
	}
}

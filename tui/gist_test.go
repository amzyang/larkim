package tui

import (
	"strings"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

// getKey is the emoji Feishu writes as [了解] in a text message and draws as a
// picture of its own: no Unicode character carries it, so a summary line that
// cannot place a picture has only the bracketed name to show.
const getKey = "Get"

// gistStyle is a message pane that draws pictures out of a data dir holding
// one emoji.
func gistStyle(t *testing.T) msgStyle {
	t.Helper()
	dir := t.TempDir()
	writeTestEmoji(t, dir, getKey)
	st := baseStyle()
	st.dataDir, st.place = dir, picturesIn(dir).place
	return st
}

// gistPics is the same data dir as the chat list asks for it.
func gistPics(t *testing.T) emojiPics {
	t.Helper()
	dir := t.TempDir()
	writeTestEmoji(t, dir, getKey)
	return emojiPics{dir: dir, place: picturesIn(dir).place}
}

// picSegs is how many pieces of a line are pictures.
func picSegs(segs []rowSeg) int {
	n := 0
	for _, s := range segs {
		if s.pic.cols > 0 {
			n++
		}
	}
	return n
}

func TestChatSummary_DrawsAnEmojiInTheBodyAsAPicture(t *testing.T) {
	c := store.Chat{ChatID: "oc_peer", Name: "张三", ChatMode: "p2p", LastMessageID: "om_1",
		LastMessageMs: at(-1), LastRenderedAt: 1, LastSenderID: "ou_me", LastSenderName: "林岚",
		LastContent: "[了解]"}

	row := renderChatRow(textAvatars{}, listRow{chat: c}, store.Draft{}, 0, "ou_me", testNow, 40, gistPics(t), nil)

	require.Empty(t, row.bottom, "a picture in the line is what puts it in pieces")
	require.Equal(t, 1, picSegs(row.segs), "the emoji is the client's own picture")
	require.NotContains(t, rowText([]msgRow{{segs: row.segs}}), "[了解]",
		"the name it was spelled with is not what the client shows")
	require.Contains(t, rowText([]msgRow{{segs: row.segs}}), "You:")
	require.Equal(t, chatTextWidth(40), segsWidth(row.segs), "the line still fills its column exactly")
}

func TestChatSummary_KeepsAnEmojiNameWhenNoPictureCanBeDrawn(t *testing.T) {
	c := store.Chat{ChatID: "oc_peer", Name: "张三", ChatMode: "p2p", LastMessageID: "om_1",
		LastMessageMs: at(-1), LastRenderedAt: 1, LastSenderID: "ou_me", LastSenderName: "林岚",
		LastContent: "[了解]"}

	_, bottom := plainRow(c, 0, 40)

	require.Contains(t, bottom, "[了解]", "the name is all a terminal without graphics has")
}

func TestRenderRows_QuoteDrawsTheEmojiItsParentSpelled(t *testing.T) {
	parent := store.Message{MessageID: "om_1", SenderName: "孙琪", Content: "[了解]",
		CreateMs: msgAt(23, 9, 0), RenderedAt: 1}
	msgs := []store.Message{
		parent,
		{MessageID: "om_2", SenderName: "沈知远", Content: "1234", ReplyTo: "om_1",
			CreateMs: msgAt(23, 9, 1), RenderedAt: 1},
	}
	st := gistStyle(t)
	st.parents = map[string]store.Message{"om_1": parent}

	quote := quoteLine(t, renderRows(msgs, st))

	require.Equal(t, 1, picSegs(quote.segs))
	require.NotContains(t, rowText([]msgRow{quote}), "[了解]")
	require.Contains(t, rowText([]msgRow{quote}), "▏Reply to 孙琪: ")
	require.Equal(t, quote.lead.cols()+segsWidth(quote.segs), quote.zones[0].x1,
		"the whole line is still the target")
}

// quoteLine is the row a reply's quote took.
func quoteLine(t *testing.T, rows []msgRow) msgRow {
	t.Helper()
	for _, r := range rows {
		if strings.Contains(ansi.Strip(rowText([]msgRow{r})), "Reply to") {
			return r
		}
	}
	t.Fatal("no quote line on the page")
	return msgRow{}
}

func TestThreadSummary_DrawsTheEmojiItsLastReplySpelled(t *testing.T) {
	root := store.Message{MessageID: "om_root", ChatID: "oc_a", SenderName: "孙琪", Content: "开个话题",
		ThreadID: "omt_1", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}
	st := gistStyle(t)
	st.threads = map[string]store.ThreadGist{"omt_1": {Replies: 1,
		SenderID: "ou_b", SenderName: "沈知远", Content: "[了解]", RenderedAt: 1}}

	rows := renderRows([]store.Message{root}, st)

	line := summaryLine(t, rows, "⤷")
	require.Equal(t, 1, picSegs(line.segs))
	require.NotContains(t, rowText([]msgRow{line}), "[了解]")
	require.Equal(t, "omt_1", line.zones[0].open)
	require.Equal(t, line.lead.cols()+segsWidth(line.segs), line.zones[0].x1)
}

func TestForwardSummary_DrawsTheEmojiItsPreviewSpelled(t *testing.T) {
	x := store.Message{MessageID: "om_fwd", ChatID: "oc_a", SenderName: "孙琪",
		MsgType: "merge_forward", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}
	st := gistStyle(t)
	st.forwards = map[string]store.ForwardGist{"om_fwd": {ChildCount: 1, Preview: []store.ForwardChild{
		{SenderID: "ou_b", SenderName: "沈知远", MsgType: "text", ContentRaw: `{"text":"[了解]"}`},
	}}}

	rows := renderRows([]store.Message{x}, st)

	line := summaryLine(t, rows, "沈知远: ")
	require.Equal(t, 1, picSegs(line.segs))
	require.NotContains(t, rowText([]msgRow{line}), "[了解]")
	require.Equal(t, "om_fwd", line.zones[0].open)
	require.Equal(t, line.lead.cols()+segsWidth(line.segs), line.zones[0].x1)
}

// summaryLine is the row holding mark.
func summaryLine(t *testing.T, rows []msgRow, mark string) msgRow {
	t.Helper()
	for _, r := range rows {
		if strings.Contains(ansi.Strip(rowText([]msgRow{r})), mark) {
			return r
		}
	}
	t.Fatalf("no line carrying %q on the page", mark)
	return msgRow{}
}

func TestTruncateSegs_DropsAPictureItCannotDrawWhole(t *testing.T) {
	segs := []rowSeg{{text: "abc"}, {pic: picture{cols: 2}}, {text: "def"}}

	require.Equal(t, segs, truncateSegs(segs, 8), "a line that fits is left alone")
	require.Equal(t, 6, segsWidth(truncateSegs(segs, 6)))
	require.Equal(t, 1, picSegs(truncateSegs(segs, 6)), "the picture still fits whole")
	require.Zero(t, picSegs(truncateSegs(segs, 4)), "half a picture names no image")
	require.Nil(t, truncateSegs(segs, 0))
}

// gistModel is a composer on a terminal that draws pictures out of a data dir
// holding one emoji.
func gistModel(t *testing.T) Model {
	t.Helper()
	dir := t.TempDir()
	writeTestEmoji(t, dir, getKey)
	m := New(Deps{Self: "ou_me", DataDir: dir})
	m.width, m.height = 100, 30
	m.pics = picturesIn(dir)
	return m
}

func TestRenderReplyBar_DrawsTheEmojiTheQuotedMessageSpelled(t *testing.T) {
	m := gistModel(t)
	m.replyTo = &store.Message{MessageID: "om_1", SenderName: "孙琪", Content: "[了解]", RenderedAt: 1}
	w := m.width - 2

	head, gist, room := m.replyBarParts(w)
	require.Equal(t, 1, picSegs(gistSegs(head, gist, room, stDim, m.chatPics().gist)))

	line := ansi.Strip(m.renderReplyBar(w))
	require.NotContains(t, line, "[了解]", "the name it was spelled with is not what the client shows")
	require.Contains(t, line, "孙琪")
	require.Equal(t, w, ansi.StringWidth(line), "the bar still fills the composer's width")
}

func TestRenderForward_DrawsTheEmojiTheForwardedMessageSpelled(t *testing.T) {
	m := gistModel(t)
	m.fwd.msg = store.Message{MessageID: "om_1", SenderName: "孙琪", Content: "[了解]", RenderedAt: 1}
	w := m.width - 2

	require.Equal(t, 1, picSegs(m.fwdGistSegs(w)))
	require.NotContains(t, ansi.Strip(m.fwdGist(w)), "[了解]")
}

func TestBodyRows_DrawAnEmojiInAnUnrenderedBodyAsAPicture(t *testing.T) {
	// A merged forward's children keep this path for good: lark-cli's
	// expansion answers with raw bodies and renders none of them.
	msgs := []store.Message{{MessageID: "om_1", SenderName: "孙琪", MsgType: "text",
		ContentRaw: `{"text":"[了解] 收到"}`, CreateMs: msgAt(23, 9, 0)}}

	rows := renderRows(msgs, gistStyle(t))

	body := summaryLine(t, rows, "收到")
	require.Equal(t, 1, picSegs(body.segs))
	require.NotContains(t, rowText([]msgRow{body}), "[了解]")
}

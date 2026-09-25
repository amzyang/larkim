package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// videoMessage is a media message as Feishu sends one: the body names the
// clip, its cover frame and how long it runs, and lark-cli renders that body
// into the markup the card replaces.
func videoMessage() []store.Message {
	return []store.Message{{MessageID: "om_1", SenderName: "张三", MsgType: "media",
		ContentRaw: `{"file_key":"file_clip","file_name":"screen.mp4","image_key":"img_cover","duration":25046}`,
		Content:    `<video key="file_clip" name="screen.mp4" duration="25s" cover_image_key="img_cover"/>`,
		CreateMs:   msgAt(23, 9, 0), RenderedAt: 1}}
}

func fileMessage() []store.Message {
	return []store.Message{{MessageID: "om_1", SenderName: "张三", MsgType: "file",
		ContentRaw: `{"file_key":"file_conf","file_name":"dev.yaml"}`,
		Content:    `<file key="file_conf" name="dev.yaml"/>`,
		CreateMs:   msgAt(23, 9, 0), RenderedAt: 1}}
}

// downloaded is the style of a pane whose attachments are all here.
func downloaded(rs ...store.Resource) msgStyle {
	st := baseStyle()
	st.dataDir = "/data"
	st.res = map[string][]store.Resource{"om_1": rs}
	return st
}

func TestBodyRows_AVideoDrawsItsCoverUnderAPlayBadge(t *testing.T) {
	st := downloaded(
		store.Resource{FileKey: "file_clip", Type: "file", LocalPath: "resources/file_clip.mp4", Status: "done", SizeBytes: 900000},
		store.Resource{FileKey: "img_cover", Type: "cover", LocalPath: "resources/img_cover.png", Status: "done"})
	st.place = func(path string, maxCols, maxRows int) picture {
		require.Equal(t, "resources/img_cover.png", path, "the cover is the picture a video is drawn as")
		return picture{path: path, cols: 30, rows: 8}
	}
	rows := renderRows(videoMessage(), st)

	cover := 0
	for _, r := range rows {
		if r.pic.cols > 0 {
			cover++
		}
	}
	require.Equal(t, 8, cover, "one row per cell row of the cover")
	out := rowText(rows)
	require.Contains(t, out, "🎬 00:26", "the badge rounds the tail up, the way the client's does")
	require.NotContains(t, out, "<video", "the card replaces the markup rather than printing it")
	require.NotContains(t, out, "screen.mp4", "a clip is its picture, not its file name")
}

func TestBodyRows_AVideoWithoutItsCoverIsStillAClip(t *testing.T) {
	out := rowText(renderRows(videoMessage(), baseStyle()))
	require.Contains(t, out, "🎬 00:26")
	require.NotContains(t, out, "<video")
}

func TestBodyRows_AVideoOpensTheFileItBroughtDown(t *testing.T) {
	st := downloaded(
		store.Resource{FileKey: "file_clip", Type: "file", LocalPath: "resources/file_clip.mp4", Status: "done"},
		store.Resource{FileKey: "img_cover", Type: "cover", LocalPath: "resources/img_cover.png", Status: "done"})
	st.place = func(path string, maxCols, maxRows int) picture {
		return picture{path: path, cols: 30, rows: 2}
	}
	want := filepath.Join("/data", "resources/file_clip.mp4")
	targets := 0
	for _, r := range renderRows(videoMessage(), st) {
		if len(r.zones) == 0 {
			continue
		}
		targets++
		z := firstZone(r)
		require.Equal(t, []string{want}, z.urls)
		require.Equal(t, leadWidth, z.x0, "a target starts where the card is drawn")
		require.Contains(t, z.note, "file_clip.mp4")
	}
	require.Equal(t, 3, targets, "the cover and the badge below it all play the clip")
}

func TestBodyRows_AnUndownloadedClipHasNothingToOpen(t *testing.T) {
	st := downloaded(store.Resource{FileKey: "file_clip", Type: "file", Status: "pending"})
	for _, r := range renderRows(videoMessage(), st) {
		require.Empty(t, r.zones, "the file is not on this machine yet")
	}
}

func TestBodyRows_AFileCardsItsNameBesideItsSize(t *testing.T) {
	st := downloaded(store.Resource{FileKey: "file_conf", Type: "file",
		LocalPath: "resources/file_conf.yaml", Status: "done", SizeBytes: 525})
	rows := renderRows(fileMessage(), st)
	out := rowText(rows)
	require.Contains(t, out, "📄 dev.yaml")
	require.Contains(t, out, "525 B", "the size is what the client puts under the name")
	require.NotContains(t, out, "<file", "the card replaces the markup rather than printing it")

	card, ok := zoneRow(rows)
	require.True(t, ok, "a downloaded file is opened from its card: %q", out)
	require.Equal(t, []string{filepath.Join("/data", "resources/file_conf.yaml")}, firstZone(card).urls)
}

func TestBodyRows_AFileKeepsItsSizeAfterBeingSkipped(t *testing.T) {
	st := downloaded(store.Resource{FileKey: "file_conf", Type: "file", Status: "skipped", SizeBytes: 12 * 1024 * 1024})
	rows := renderRows(fileMessage(), st)
	require.Contains(t, rowText(rows), "12.0 MB", "what was too large to keep is still worth naming")
	_, ok := zoneRow(rows)
	require.False(t, ok, "nothing was kept, so nothing opens")
}

func TestBodyRows_AVoiceMessageReadsAsItsLength(t *testing.T) {
	msgs := []store.Message{{MessageID: "om_1", SenderName: "张三", MsgType: "audio",
		ContentRaw: `{"file_key":"file_voice","duration":21000}`,
		Content:    `<audio key="file_voice" duration="21s"/>`,
		CreateMs:   msgAt(23, 9, 0), RenderedAt: 1}}
	out := rowText(renderRows(msgs, baseStyle()))
	require.Contains(t, out, "🎤 00:21")
	require.NotContains(t, out, "<audio")
}

func TestBodyRows_ADownloadedVoiceMessageStillOpensNothing(t *testing.T) {
	st := downloaded(store.Resource{FileKey: "file_voice", Type: "file",
		LocalPath: "resources/file_voice", Status: "done", SizeBytes: 47515})
	msgs := []store.Message{{MessageID: "om_1", SenderName: "张三", MsgType: "audio",
		ContentRaw: `{"file_key":"file_voice","duration":21000}`,
		Content:    `<audio key="file_voice" duration="21s"/>`,
		CreateMs:   msgAt(23, 9, 0), RenderedAt: 1}}
	rows := renderRows(msgs, st)
	require.Contains(t, rowText(rows), "🎤 00:21", "the card is drawn either way")
	require.Empty(t, rowZones(rows),
		"Feishu sends voice as an extensionless Ogg Opus file, which macOS has nothing to open")
}

func TestBodyRows_AnAttachmentCardsBeforeItsRenderingLands(t *testing.T) {
	msgs := fileMessage()
	msgs[0].Content, msgs[0].RenderedAt = "", 0
	require.Contains(t, rowText(renderRows(msgs, baseStyle())), "dev.yaml",
		"the body names the whole card, so it costs no render call")
}

func TestBodyRows_AnUnreadableAttachmentBodyKeepsTheRendering(t *testing.T) {
	msgs := fileMessage()
	msgs[0].ContentRaw = "not json"
	require.Contains(t, rowText(renderRows(msgs, baseStyle())), "<file")
}

func TestBodyRows_ALongFileNameStaysOnOneLine(t *testing.T) {
	msgs := fileMessage()
	msgs[0].ContentRaw = `{"file_key":"file_conf","file_name":"` + strings.Repeat("长", 80) + `.pdf"}`
	rows := renderRows(msgs, baseStyle())
	cards := 0
	for _, r := range rows {
		if strings.Contains(rowText([]msgRow{r}), "📕") {
			cards++
		}
	}
	require.Equal(t, 1, cards, "a card is one line, whatever the name's length")
}

func TestChatSummary_NamesAnAttachmentRatherThanItsMarkup(t *testing.T) {
	c := store.Chat{ChatID: "oc_1", Name: "平台组", LastMessageID: "om_1", LastSenderName: "张三",
		LastMsgType: "file", LastContentRaw: `{"file_key":"file_conf","file_name":"dev.yaml"}`,
		LastContent: `<file key="file_conf" name="dev.yaml"/>`, LastRenderedAt: 1}
	require.Equal(t, "张三: [文件] dev.yaml", ansi.Strip(chatSummary(c, "ou_me")))

	c.LastMsgType, c.LastContentRaw = "media", `{"file_key":"file_clip","image_key":"img_cover","duration":25046}`
	c.LastContent = `<video key="file_clip" name="18446744072109523023.mp4" duration="25s" cover_image_key="img_cover"/>`
	require.Equal(t, "张三: [视频]", ansi.Strip(chatSummary(c, "ou_me")), "a clip's file name is a serial number")
}

func TestReplyGist_NamesAnAttachmentRatherThanItsMarkup(t *testing.T) {
	require.Equal(t, "[文件] dev.yaml", replyGist(store.Message{MsgType: "file", RenderedAt: 1,
		ContentRaw: `{"file_key":"file_conf","file_name":"dev.yaml"}`,
		Content:    `<file key="file_conf" name="dev.yaml"/>`}))
}

func TestClipLength_SpellsThePlayersBadge(t *testing.T) {
	require.Equal(t, "00:26", clipLength(25046), "a part second still has to play")
	require.Equal(t, "10:08", clipLength(608000))
	require.Equal(t, "1:02:05", clipLength(3725000))
	require.Equal(t, "--:--", clipLength(0), "a body that names no length says so")
}

// zoneRow is the first row carrying a click target, and false when none does.
func zoneRow(rows []msgRow) (msgRow, bool) {
	for _, r := range rows {
		if len(r.zones) > 0 {
			return r, true
		}
	}
	return msgRow{}, false
}

// firstZone is the target a row draws first, and the zero zone when it draws
// none.
func firstZone(r msgRow) clickZone {
	if len(r.zones) == 0 {
		return clickZone{}
	}
	return r.zones[0]
}

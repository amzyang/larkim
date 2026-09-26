package tui

import (
	"path/filepath"
	"testing"

	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

// picturePost is a rich-text message naming n pictures, all of them on this
// machine.
func picturePost(n int) ([]store.Message, msgStyle) {
	body, res := "看这些", []store.Resource{}
	for i := range n {
		key := string(rune('a' + i))
		body += "\n![Image](img_" + key + ")"
		res = append(res, store.Resource{FileKey: "img_" + key, Type: "image",
			LocalPath: "resources/" + key + ".png", Status: "done"})
	}
	st := baseStyle()
	st.dataDir = "/data"
	st.res = map[string][]store.Resource{"om_1": res}
	st.place = func(path string, maxCols, maxRows int) picture {
		return picture{path: path, cols: 10, rows: 2}
	}
	return []store.Message{{MessageID: "om_1", SenderID: "ou_a", SenderName: "张三", MsgType: "post",
		Content: body, CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}, st
}

func TestBodyRows_APictureOpensTheFileItWasDrawnFrom(t *testing.T) {
	msgs, st := picturePost(1)
	zones := rowZones(renderRows(msgs, st))
	require.NotEmpty(t, zones)
	require.Equal(t, []string{filepath.Join("/data", "resources/a.png")}, zones[0].urls)
	require.Equal(t, "image", zones[0].label)
}

func TestBodyRows_EveryPictureOfAMessageOpensTheWholeSet(t *testing.T) {
	msgs, st := picturePost(3)
	byFirst := map[string][]string{}
	for _, z := range rowZones(renderRows(msgs, st)) {
		require.Len(t, z.urls, 3, "pressing one picture opens the message's pictures together")
		require.Equal(t, "3 images", z.label)
		byFirst[z.urls[0]] = z.urls
	}
	require.Len(t, byFirst, 3, "each picture leads the set it is pressed from")
	for first, urls := range byFirst {
		require.Equal(t, first, urls[0], "the one pressed is the one the viewer opens on")
		require.ElementsMatch(t, []string{
			filepath.Join("/data", "resources/a.png"),
			filepath.Join("/data", "resources/b.png"),
			filepath.Join("/data", "resources/c.png"),
		}, urls, "the rest of the message comes along behind it")
	}
}

func TestBodyRows_AStickerOpensNothing(t *testing.T) {
	st := baseStyle()
	st.dataDir = "/data"
	st.res = map[string][]store.Resource{"om_1": {{FileKey: "img_s", Type: "image",
		LocalPath: "resources/s.png", Status: "done"}}}
	st.place = func(path string, maxCols, maxRows int) picture {
		return picture{path: path, cols: 10, rows: 2}
	}
	msgs := []store.Message{{MessageID: "om_1", SenderName: "张三", MsgType: "sticker",
		ContentRaw: `{"file_key":"img_s"}`, Content: "[Sticker]", CreateMs: msgAt(23, 9, 0), RenderedAt: 1}}
	require.Empty(t, rowZones(renderRows(msgs, st)),
		"pressing a sticker opens nothing in the client either")
}

func TestBodyRows_AnUndownloadedPictureHasNothingToOpen(t *testing.T) {
	msgs, st := picturePost(1)
	st.res = map[string][]store.Resource{"om_1": {{FileKey: "img_a", Type: "image", Status: "pending"}}}
	st.place = func(string, int, int) picture { return picture{} }
	require.Empty(t, rowZones(renderRows(msgs, st)))
}

// targetPage is a chat holding one message, with the opener replaced so the
// hand-overs a keypress fires are recorded instead of reaching macOS.
func targetPage(t *testing.T, msg store.Message, res []store.Resource) (Model, *[]openCall) {
	t.Helper()
	var calls []openCall
	m := New(Deps{Self: "ou_me", DataDir: "/data", OpenURL: func(targets []string, background bool) error {
		calls = append(calls, openCall{targets, background})
		return nil
	}})
	m.width, m.height = 120, 36
	m.chatID = "oc_a"
	m.msgsBase = []store.Message{msg}
	if res != nil {
		m.meta.res = map[string][]store.Resource{msg.MessageID: res}
	}
	m.applyOutbox()
	m.layout()
	m.focus, m.msgIdx = paneMessages, 0
	m.rebuildMessages()
	return m, &calls
}

func linkMessage(content string) store.Message {
	return store.Message{MessageID: "om_1", ChatID: "oc_a", SenderID: "ou_a", SenderName: "张三",
		MsgType: "post", Content: content, CreateMs: msgAt(23, 9, 0), MessagePosition: 227, RenderedAt: 1}
}

func TestOnNormalKey_OOpensTheOneTargetWithoutAsking(t *testing.T) {
	m, calls := targetPage(t, linkMessage("[了解](https://example.com/x)"), nil)
	model, cmd := m.onNormalKey("o")
	require.Equal(t, modeNormal, model.(Model).mode, "one target needs no chooser")
	collect(cmd)
	require.Equal(t, []openCall{opened("https://example.com/x", false)}, *calls)
}

func TestOnNormalKey_OOffersAChooserWhenAMessageLeadsSeveralWays(t *testing.T) {
	m, _ := targetPage(t, linkMessage("[甲](https://a.example.com/1) [乙](https://b.example.com/2)"), nil)
	got := press(t, m, "o")
	require.Equal(t, modeTarget, got.mode)
	require.Len(t, got.targets.zones, 3, "the two links, then the message itself")
	require.Equal(t, "甲", got.targets.zones[0].label)
	require.Contains(t, got.renderTargets(), "a.example.com",
		"a link is drawn as its label alone, so the chooser says where it goes")
}

func TestOpenTargets_TheMessageItselfClosesTheList(t *testing.T) {
	m, calls := targetPage(t, linkMessage("[甲](https://a.example.com/1) [乙](https://b.example.com/2)"), nil)
	m = press(t, m, "o")
	require.Len(t, m.targets.zones, 3, "what larkim cannot hand over is still reachable through the client")
	last := m.targets.zones[2]
	require.Equal(t, "open in Feishu", last.label)
	require.Equal(t, []string{feishuChatLink("oc_a", 227)}, last.urls)

	_, cmd := press(t, m, "G").onTargetKey(keyMsg("enter"))
	collect(cmd)
	require.Equal(t, []openCall{opened(feishuChatLink("oc_a", 227), false)}, *calls)
}

func TestOpenTargets_TheMessageIsNotListedTwice(t *testing.T) {
	msg := cardOf(cardActionRow(
		cardButton("详情", `{"type":"open_url","action":{"url":"https://example.com/run/1"}}`),
		cardButton("同意", cardCallback)), nil)
	msg.MessageID, msg.ChatID, msg.MessagePosition = "om_1", "oc_a", 227
	m, _ := targetPage(t, msg, nil)
	m = press(t, m, "o")
	require.Len(t, m.targets.zones, 2,
		"a callback button already leads to the message, so the fallback is not added again")
}

func TestSelectedZones_ListsTheSamePlaceOnce(t *testing.T) {
	m, _ := targetPage(t, linkMessage("[甲](https://example.com/x) [乙](https://example.com/x)"), nil)
	require.Len(t, m.selectedZones(), 1, "the same place twice would read as two different ones")
}

func TestSelectedZones_CountsAWrappedLinkOnce(t *testing.T) {
	m, _ := targetPage(t, linkMessage("["+"long label to wrap "+"](https://example.com/x)"), nil)
	m.width = 30
	m.layout()
	m.rebuildMessages()
	require.Len(t, m.selectedZones(), 1)
}

func TestOnTargetKey_EnterOpensTheHighlightedTarget(t *testing.T) {
	m, calls := targetPage(t, linkMessage("[甲](https://a.example.com/1) [乙](https://b.example.com/2)"), nil)
	m = press(t, m, "o", "j")
	require.Equal(t, modeTarget, m.mode)
	next, cmd := m.onTargetKey(keyMsg("enter"))
	require.Equal(t, modeNormal, next.(Model).mode, "choosing closes the chooser")
	collect(cmd)
	require.Equal(t, []openCall{opened("https://b.example.com/2", false)}, *calls)
}

func TestOnTargetKey_ADigitReachesATargetStraightOff(t *testing.T) {
	m, calls := targetPage(t, linkMessage("[甲](https://a.example.com/1) [乙](https://b.example.com/2)"), nil)
	m = press(t, m, "o")
	_, cmd := m.onTargetKey(keyMsg("2"))
	collect(cmd)
	require.Equal(t, []openCall{opened("https://b.example.com/2", false)}, *calls)
}

func TestOnTargetKey_EscLeavesEverythingClosed(t *testing.T) {
	m, calls := targetPage(t, linkMessage("[甲](https://a.example.com/1) [乙](https://b.example.com/2)"), nil)
	require.Equal(t, modeNormal, press(t, m, "o", "esc").mode)
	require.Empty(t, *calls)
}

func TestOnNormalKey_OStillOpensTheMessageWhenItLeadsNowhere(t *testing.T) {
	m, calls := targetPage(t, linkMessage("没有链接"), nil)
	_, cmd := m.onNormalKey("o")
	collect(cmd)
	require.Equal(t, []openCall{
		opened("lark://applink.feishu.cn/client/chat/open?openChatId=oc_a&position=227", false)}, *calls)
}

func TestTargetHint_SaysWhereEachKindLeads(t *testing.T) {
	require.Equal(t, "git.example.com", targetHint([]string{"https://git.example.com/a/b"}))
	require.Equal(t, "Feishu", targetHint([]string{"lark://vc.feishu.cn/j/100000000"}))
	require.Equal(t, "docx", targetHint([]string{"https://example.feishu.cn/docx/AbC123"}),
		"a row showing a title no longer shows the token, and every document in a tenant shares one host")
	require.Equal(t, "sheet", targetHint([]string{"https://example.feishu.cn/sheets/Xyz789"}))
	require.Equal(t, "example.feishu.cn", targetHint([]string{"https://example.feishu.cn/minutes/obcnAbC123"}),
		"what is not a document is still named by where it leads")
	require.Equal(t, "resources", targetHint([]string{"/data/resources/a.png"}))
	require.Equal(t, "3 files", targetHint([]string{"/a.png", "/b.png", "/c.png"}))
	require.Empty(t, targetHint(nil))
}

func TestOnClick_ALinkOpensFromTheColumnsItsLabelIsDrawnIn(t *testing.T) {
	m, calls := targetPage(t, linkMessage("前面 [了解详情](https://example.com/x) 后面"), nil)
	var row, x0, x1 int
	for i, r := range m.msgRows {
		if len(r.zones) > 0 {
			row, x0, x1 = i, r.zones[0].x0, r.zones[0].x1
		}
	}
	require.NotZero(t, x1, "no link on the page")
	for _, x := range []int{x0, x1 - 1} {
		*calls = nil
		collect(clickAt(m, row, x))
		require.Equal(t, []openCall{opened("https://example.com/x", false)}, *calls,
			"column %d is inside the label a wide-character body drew", x)
	}
	*calls = nil
	collect(clickAt(m, row, x0-1))
	collect(clickAt(m, row, x1))
	require.Empty(t, *calls, "the text either side of the label is not the link")
}

func TestComposerRows_TheChooserGrowsToItsList(t *testing.T) {
	var body string
	for i := range 6 {
		body += "[链接" + string(rune('a'+i)) + "](https://example.com/" + string(rune('a'+i)) + ") "
	}
	m, _ := targetPage(t, linkMessage(body), nil)
	rest := m.composerHeight()
	open := press(t, m, "o")
	require.Len(t, open.targets.zones, 7, "six links, then the message itself")
	require.GreaterOrEqual(t, open.targetRows(), 7, "the whole list is read without scrolling")
	require.Greater(t, open.composerHeight(), rest)
	require.Equal(t, rest, press(t, open, "esc").composerHeight(), "the box goes back to the one at rest")
}

func TestComposerRows_TheChooserNeverGrowsPastWhatItNeeds(t *testing.T) {
	m, _ := targetPage(t, linkMessage("[甲](https://a.example.com/1) [乙](https://b.example.com/2)"), nil)
	rest := m.composerHeight()
	open := press(t, m, "o")
	require.Len(t, open.targets.zones, 3)
	require.Equal(t, rest, open.composerHeight(),
		"three lines fit the box at rest, so nothing above the box moves")
	require.GreaterOrEqual(t, open.targetRows(), 3)
}

func TestOnTargetKey_ADigitMeansTheLineItIsDrawnOn(t *testing.T) {
	var body string
	for i := range 20 {
		body += "[链接" + string(rune('a'+i)) + "](https://example.com/" + string(rune('a'+i)) + ") "
	}
	m, calls := targetPage(t, linkMessage(body), nil)
	m = press(t, m, "o")
	require.Len(t, m.targets.zones, 21, "twenty links, then the message itself")
	require.Less(t, m.targetRows(), 21, "the list did not overflow; the test proves nothing")

	m = press(t, m, "G")
	require.Greater(t, m.targets.top, 0, "the list scrolled")
	_, cmd := m.onTargetKey(keyMsg("1"))
	collect(cmd)
	want := "https://example.com/" + string(rune('a'+m.targets.top))
	require.Equal(t, []openCall{opened(want, false)}, *calls,
		"the first line on screen is what 1 reaches, not the first of the list")
}

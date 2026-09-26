package tui

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestClassify_PlainChatStaysText(t *testing.T) {
	for _, draft := range []string{
		"",
		"好的",
		"- 好的", // a single-line marker is how people answer, not a list
		"1. 我来",
		"3*4=12",
		"#1 优先", // no space after the hash, so not a heading
		"https://example.com/a_b_c",
		"[Sticker]",
		":OK:",
		`<at user_id="ou_a">张三</at>`,
		"a | b",     // a pipe without a separator row is not a table
		"改成 *这样* 吧", // italics are deliberately not a signal
		"路径是 user_id_map",
	} {
		require.Equal(t, kindText, classify(draft), "draft %q", draft)
	}
}

func TestClassify_MarkdownBecomesPost(t *testing.T) {
	for _, draft := range []string{
		"## 发布说明",
		"# 标题\n正文",
		"```go\nx := 1\n```",
		"- 修复了 A\n- 修复了 B",
		"1. 先这样\n2. 再那样",
		"| 项 | 值 |\n|---|---|\n| a | 1 |",
		"> 引用一句",
		"**粗体**",
		"~~删除~~",
		"看 [链接](https://example.com)",
		"跑 `go test`",
		"上面\n---\n下面",
	} {
		require.Equal(t, kindPost, classify(draft), "draft %q", draft)
	}
}

func TestClassify_TableNeedsItsSeparatorRow(t *testing.T) {
	require.Equal(t, kindText, classify("| a | b |\n| c | d |"))
	require.Equal(t, kindPost, classify("| a | b |\n|---|---|\n| c | d |"))
}

func TestClassify_FencedContentIsNotRescanned(t *testing.T) {
	// The fence answers first, so the markers inside it never decide anything.
	require.Equal(t, kindPost, classify("```\n- not a list\n```"))
}

func TestClassify_LoneImageRefIsAnImageMessage(t *testing.T) {
	require.Equal(t, kindImage, classify("![截图](~/Desktop/shot.png)"))
	require.Equal(t, kindImage, classify("  ![](img_v3_shot)  "))
	// Anything beside the image makes it a rich-text message instead.
	require.Equal(t, kindPost, classify("![截图](img_v3_shot) 看看"))
	require.Equal(t, kindPost, classify("![a](img_a)\n![b](img_b)"))
}

func TestPlanDraft_PicksTheBodyTheTypeNeeds(t *testing.T) {
	f := draftFiles{}
	p, err := f.planDraft("  好的  ")
	require.NoError(t, err)
	require.Equal(t, kindText, p.kind)
	require.Equal(t, "好的", p.body)
	require.Equal(t, "好的", p.send.Text)

	p, err = f.planDraft("## 发布说明")
	require.NoError(t, err)
	require.Equal(t, kindPost, p.kind)
	require.Equal(t, "## 发布说明", p.send.Markdown)
	require.Empty(t, p.send.Text)
}

func TestSubmit_MarkdownDraftSendsAPost(t *testing.T) {
	m, f := newOutboxModel(t)
	m.input.SetValue("## 发布说明\n\n- 修复了 A")

	mm, cmd := m.submit()
	m = mm.(Model)
	require.NotNil(t, cmd)
	cmd() // the send runs in a command, so drain it before asking the fake

	require.Len(t, f.Sent, 1)
	require.Equal(t, "## 发布说明\n\n- 修复了 A", f.Sent[0].Markdown)
	require.Empty(t, f.Sent[0].Text)
	require.Equal(t, "post", m.outbox[0].msgType)
	require.Equal(t, "post", m.msgs[0].MsgType)
	require.Equal(t, "## 发布说明\n\n- 修复了 A", m.msgs[0].Content)
}

func TestSubmit_PlainDraftStillSendsText(t *testing.T) {
	m, f := newOutboxModel(t)
	m.input.SetValue("好的")

	mm, cmd := m.submit()
	m = mm.(Model)
	cmd()

	require.Len(t, f.Sent, 1)
	require.Equal(t, "好的", f.Sent[0].Text)
	require.Empty(t, f.Sent[0].Markdown)
	require.Equal(t, "text", m.msgs[0].MsgType)
}

func TestReply_CarriesTheDraftsType(t *testing.T) {
	m, f := newOutboxModel(t)
	f.AddMessage(larkcli.RawMessage{MessageID: "om_elsewhere", ChatID: "oc_1", MsgType: "text"})
	m.setReply(&store.Message{MessageID: "om_elsewhere", ChatID: "oc_1"}, false)
	m.input.SetValue("- a\n- b")

	mm, cmd := m.submit()
	m = mm.(Model)
	cmd()

	require.Len(t, f.Sent, 1)
	require.Equal(t, "- a\n- b", f.Sent[0].Markdown)
}

func TestRenderInput_BadgeNamesTheResolvedType(t *testing.T) {
	m, _ := newOutboxModel(t)
	for draft, want := range map[string]string{
		"好的":                 "text",
		"## 发布说明":            "post",
		"![截图](img_v3_shot)": "image",
	} {
		mm, _ := m.startInsert(nil, false)
		m = mm.(Model)
		m.input.SetValue(draft)
		m.replan()

		badge := ansi.Strip(m.renderBadge(m.width - 2))
		require.Contains(t, badge, want, "draft %q", draft)
		require.Contains(t, badge, composerHint)
	}
}

func TestComposerHeight_StandsStillWhateverModeTheReaderIsIn(t *testing.T) {
	m, _ := newOutboxModel(t)
	require.Equal(t, restingComposer, m.composerHeight(), "the badge row is claimed in every mode")

	for _, md := range []mode{modeInsert, modeCommand, modeFilter, modeEmoji, modeVisual} {
		m.mode = md
		require.Equal(t, restingComposer, m.composerHeight(), "mode %v moves the box", md)
	}

	// The composer box has to be exactly as tall as it claims, or the panes
	// above it give up the wrong number of rows.
	m.mode = modeInsert
	require.Equal(t, m.composerHeight()+2, lipgloss.Height(m.renderInput()))
}

func TestRenderBadge_NamesTheKeyThatFitsTheMode(t *testing.T) {
	m, _ := newOutboxModel(t)
	out := ansi.Strip(m.renderBadge(m.width - 2))
	require.Contains(t, out, writeHint, "outside insert mode the row says how to get in")
	require.NotContains(t, out, "Enter send")

	mm, _ := m.startInsert(nil, false)
	out = ansi.Strip(mm.(Model).renderBadge(m.width - 2))
	require.Contains(t, out, composerHint)
	require.NotContains(t, out, writeHint, "a reader already writing is not told to start")
}

func TestHelp_DocumentsTheComposerTypes(t *testing.T) {
	require.True(t, helpHas("markdown sends as a post"))
	require.True(t, helpHas("![](path) sends an image"))
	require.True(t, helpHas("the badge under the draft names the type"))
	require.True(t, helpHas("Ctrl+o previews a post or an image"))
	require.True(t, helpHas("Ctrl+g opens the draft in $VISUAL or $EDITOR"))
	require.True(t, helpHas("Ctrl+v pastes an image, a file path or text from the clipboard"))
}

// fakeFiles answers Stat for the paths named, so a test never touches the
// real filesystem or the real home.
func fakeFiles(sizes map[string]int64) draftFiles {
	return draftFiles{Home: "/Users/linlan", Stat: func(path string) (os.FileInfo, error) {
		n, ok := sizes[path]
		if !ok {
			return nil, os.ErrNotExist
		}
		return fakeInfo{name: filepath.Base(path), size: n}, nil
	}}
}

type fakeInfo struct {
	name string
	size int64
	dir  bool
}

func (f fakeInfo) Name() string { return f.name }
func (f fakeInfo) Size() int64  { return f.size }
func (f fakeInfo) Mode() os.FileMode {
	if f.dir {
		return os.ModeDir
	}
	return 0
}
func (f fakeInfo) ModTime() time.Time { return time.Time{} }
func (f fakeInfo) IsDir() bool        { return f.dir }
func (f fakeInfo) Sys() any           { return nil }

func TestResolveDraft_ExpandsTildeAndRelativePaths(t *testing.T) {
	cwd, err := os.Getwd()
	require.NoError(t, err)
	f := fakeFiles(map[string]int64{
		"/Users/linlan/Desktop/shot.png": 2048,
		filepath.Join(cwd, "a.png"):      512,
	})

	p, err := f.planDraft("![截图](~/Desktop/shot.png)")
	require.NoError(t, err)
	require.Equal(t, kindImage, p.kind)
	require.Equal(t, "/Users/linlan/Desktop/shot.png", p.images[0].local)
	require.Equal(t, "[Image: img_local_0]", p.body)
	require.Equal(t, "img_local_0", p.send.ImageKey)

	p, err = f.planDraft("![a](./a.png)")
	require.NoError(t, err)
	require.Equal(t, filepath.Join(cwd, "a.png"), p.images[0].local)
}

func TestResolveDraft_MissingFileIsAUserError(t *testing.T) {
	f := fakeFiles(nil)
	_, err := f.planDraft("![x](~/nope.png)")
	require.ErrorContains(t, err, "no such file: ~/nope.png")
}

func TestResolveDraft_RefusesAnOversizeImage(t *testing.T) {
	f := fakeFiles(map[string]int64{"/Users/linlan/big.png": 14 << 20})
	_, err := f.planDraft("![x](~/big.png)")
	require.ErrorContains(t, err, "big.png is 14.0 MB, over the 10.0 MB limit")
}

func TestResolveDraft_RefusesADirectoryAndAnotherPersonsHome(t *testing.T) {
	f := draftFiles{Home: "/Users/linlan", Stat: func(string) (os.FileInfo, error) {
		return fakeInfo{name: "Desktop", dir: true}, nil
	}}
	_, err := f.planDraft("![x](~/Desktop)")
	require.ErrorContains(t, err, "is a directory")

	_, err = f.planDraft("![x](~zhangsan/a.png)")
	require.ErrorContains(t, err, "cannot expand")
}

func TestResolveDraft_PassesAnImageKeyThrough(t *testing.T) {
	f := fakeFiles(nil)
	p, err := f.planDraft("![截图](img_v3_shot)")
	require.NoError(t, err)
	require.Equal(t, kindImage, p.kind)
	require.Empty(t, p.images[0].local, "a key needs no upload")
	require.Equal(t, "img_v3_shot", p.send.ImageKey)
	require.Empty(t, p.uploads())
}

func TestResolveDraft_PostKeepsTheDraftAroundItsImages(t *testing.T) {
	f := fakeFiles(map[string]int64{"/Users/linlan/shot.png": 2048})
	p, err := f.planDraft("## 周报\n\n![截图](~/shot.png)\n\n见图")
	require.NoError(t, err)
	require.Equal(t, kindPost, p.kind)
	require.Equal(t, "## 周报\n\n![截图](img_local_0)\n\n见图", p.body)
	require.Equal(t, "## 周报\n\n![截图](img_local_0)\n\n见图", p.send.Markdown)
	require.Len(t, p.uploads(), 1)
}

func TestSubmit_ImageDraftUploadsThenSends(t *testing.T) {
	m, f := newOutboxModel(t)
	m.files = fakeFiles(map[string]int64{"/Users/linlan/Desktop/shot.png": 2048})
	m.input.SetValue("![截图](~/Desktop/shot.png)")

	mm, cmd := m.submit()
	m = mm.(Model)
	msg := cmd().(sentMsg)

	require.NoError(t, msg.err)
	require.Equal(t, []string{"/Users/linlan/Desktop/shot.png"}, f.Uploads)
	require.Len(t, f.Sent, 1)
	require.Equal(t, "img_fake_1", f.Sent[0].ImageKey)
	require.Equal(t, []string{"img_fake_1"}, msg.keys)
	require.Equal(t, "image", m.msgs[0].MsgType)
	require.Equal(t, "[Image: img_local_0]", m.msgs[0].Content)
}

func TestSubmit_MarkdownWithLocalImageUploadsAndRewrites(t *testing.T) {
	m, f := newOutboxModel(t)
	m.files = fakeFiles(map[string]int64{"/Users/linlan/shot.png": 2048})
	m.input.SetValue("## 周报\n\n![截图](~/shot.png)")

	mm, cmd := m.submit()
	m = mm.(Model)
	cmd()

	require.Equal(t, "## 周报\n\n![截图](img_fake_1)", f.Sent[0].Markdown)
	require.NotContains(t, f.Sent[0].Markdown, "shot.png")
}

func TestSubmit_TypoedPathKeepsTheDraft(t *testing.T) {
	m, f := newOutboxModel(t)
	m.files = fakeFiles(nil)
	m.input.SetValue("![x](~/nope.png)")

	mm, cmd := m.submit()
	m = mm.(Model)

	require.Nil(t, cmd, "nothing goes on the wire")
	require.Empty(t, m.outbox)
	require.Empty(t, f.Sent)
	require.Equal(t, "![x](~/nope.png)", m.input.Value(), "the draft stays so it can be fixed")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "no such file")
}

func TestApplyOutbox_PendingImageCarriesItsLocalFile(t *testing.T) {
	m, _ := newOutboxModel(t)
	m.files = fakeFiles(map[string]int64{"/Users/linlan/shot.png": 2048})
	m.input.SetValue("![截图](~/shot.png)")

	mm, _ := m.submit()
	m = mm.(Model)

	rs := m.meta.res[m.outbox[0].localID]
	require.Len(t, rs, 1)
	require.Equal(t, "img_local_0", rs[0].FileKey)
	require.Equal(t, "done", rs[0].Status)
	require.Equal(t, "/Users/linlan/shot.png", rs[0].LocalPath)

	// A redraw must not stack a second copy of the same row.
	m.refreshPanes()
	require.Len(t, m.meta.res[m.outbox[0].localID], 1)
}

func TestRetry_DoesNotUploadTwice(t *testing.T) {
	m, f := newOutboxModel(t)
	m.files = fakeFiles(map[string]int64{"/Users/linlan/shot.png": 2048})
	m.input.SetValue("![截图](~/shot.png)")
	mm, cmd := m.submit()
	m = mm.(Model)

	// The upload lands, the send behind it does not.
	f.SendErr = errors.New("network down")
	msg := cmd().(sentMsg)
	require.Error(t, msg.err)
	mm, _ = m.Update(msg)
	m = mm.(Model)
	require.Equal(t, outFailed, m.outbox[0].state)
	require.Equal(t, []string{"img_fake_1"}, m.outbox[0].keys)

	f.SendErr = nil
	m.msgIdx = 0
	mm, retry := m.retryFailed()
	m = mm.(Model)
	require.NotNil(t, retry)
	retry()

	require.Len(t, f.Uploads, 1, "the key from the first attempt is reused")
	require.Equal(t, "img_fake_1", f.Sent[len(f.Sent)-1].ImageKey)
}

func TestRenderBadge_NamesTheFileAndTheError(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.files = fakeFiles(map[string]int64{"/Users/linlan/shot.png": 2048})

	m.input.SetValue("![截图](~/shot.png)")
	m.replan()
	require.Contains(t, ansi.Strip(m.renderBadge(m.width-2)), "shot.png")

	m.input.SetValue("## 周报\n\n![a](~/shot.png)\n![b](~/shot.png)")
	m.replan()
	require.Contains(t, ansi.Strip(m.renderBadge(m.width-2)), "2 images")

	m.input.SetValue("![x](~/nope.png)")
	m.replan()
	require.Contains(t, ansi.Strip(m.renderBadge(m.width-2)), "no such file: ~/nope.png")
}

func TestBodyRows_HeadingsDropTheirHashes(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	// --markdown demotes what the user typed, so the list must not show the
	// hashes lark-cli put there.
	m.input.SetValue("#### 发布说明\n\n正文")
	m.replan()
	m.layout()

	require.NotEmpty(t, m.previewRows)
	line, _ := m.rowLine(m.previewRows[0], m.width-2)
	got := ansi.Strip(line)
	require.Contains(t, got, "发布说明")
	require.NotContains(t, got, "#")
}

// uploadSpy records what actually reached UploadImage, contents included, so
// a test can prove the fetched bytes are the bytes that went up.
type uploadSpy struct {
	*larkcli.Fake
	paths []string
	bytes [][]byte
}

func (u *uploadSpy) UploadImage(ctx context.Context, path string) (string, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	u.paths = append(u.paths, path)
	u.bytes = append(u.bytes, b)
	return u.Fake.UploadImage(ctx, path)
}

func TestResolveDraft_AcceptsARemoteImage(t *testing.T) {
	f := fakeFiles(nil) // no file on disk, and none is wanted
	p, err := f.planDraft("![图](https://example.com/a.png)")
	require.NoError(t, err, "a URL is not a missing file")
	require.Equal(t, kindImage, p.kind)
	require.Equal(t, "https://example.com/a.png", p.images[0].url)
	require.Empty(t, p.images[0].local)
	require.Len(t, p.uploads(), 1, "it still has to reach Feishu before the send")
}

func TestResolveDraft_StillRefusesAMissingLocalFile(t *testing.T) {
	f := fakeFiles(nil)
	_, err := f.planDraft("![x](~/nope.png)")
	require.ErrorContains(t, err, "no such file", "only http(s) is exempt from the stat")
}

func TestSubmit_RemoteImageIsFetchedThenUploaded(t *testing.T) {
	m, fake := newOutboxModel(t)
	spy := &uploadSpy{Fake: fake}
	m.deps.Client = spy
	m.files = fakeFiles(nil)
	m.deps.Fetch = func(_ context.Context, url string) ([]byte, string, error) {
		require.Equal(t, "https://example.com/a.png", url)
		return []byte("\x89PNG-bytes"), "image/png", nil
	}
	m.input.SetValue("## 周报\n\n![图](https://example.com/a.png)")

	mm, cmd := m.submit()
	m = mm.(Model)
	msg := cmd().(sentMsg)

	require.NoError(t, msg.err)
	require.Len(t, spy.bytes, 1)
	require.Equal(t, []byte("\x89PNG-bytes"), spy.bytes[0], "what was fetched is what went up")
	require.Equal(t, ".png", filepath.Ext(spy.paths[0]))
	require.NoFileExists(t, spy.paths[0], "the scratch download does not outlive the upload")
	require.Contains(t, fake.Sent[0].Markdown, "img_fake_1")
	require.NotContains(t, fake.Sent[0].Markdown, "example.com", "the URL never reaches Feishu")
}

func TestSubmit_RemoteImageFailureKeepsTheBubbleRetryable(t *testing.T) {
	m, fake := newOutboxModel(t)
	m.files = fakeFiles(nil)
	m.deps.Fetch = func(context.Context, string) ([]byte, string, error) {
		return nil, "", errors.New("dial tcp: no route to host")
	}
	m.input.SetValue("![图](https://example.com/a.png)")

	mm, cmd := m.submit()
	m = mm.(Model)
	msg := cmd().(sentMsg)
	require.ErrorContains(t, msg.err, "no route to host")
	require.Empty(t, fake.Sent, "nothing goes out when an image could not be had")

	mm, _ = m.Update(msg)
	m = mm.(Model)
	require.Equal(t, outFailed, m.outbox[0].state, ". can send it again")
}

func TestSubmit_RemoteImageAtTheFetchCeilingIsRefused(t *testing.T) {
	m, fake := newOutboxModel(t)
	m.files = fakeFiles(nil)
	m.deps.Fetch = func(context.Context, string) ([]byte, string, error) {
		// The shared fetcher truncates at its ceiling rather than failing, so
		// a body this size may be half a picture.
		return make([]byte, remoteCeiling), "image/png", nil
	}
	m.input.SetValue("![图](https://example.com/huge.png)")

	mm, cmd := m.submit()
	m = mm.(Model)
	msg := cmd().(sentMsg)
	require.ErrorContains(t, msg.err, "over the")
	require.Empty(t, fake.Uploads, "a truncated download is never uploaded")
	require.Empty(t, fake.Sent)
}

func TestSubmit_RemoteImageRefusesAnEmptyBody(t *testing.T) {
	m, fake := newOutboxModel(t)
	m.files = fakeFiles(nil)
	m.deps.Fetch = func(context.Context, string) ([]byte, string, error) { return nil, "image/png", nil }
	m.input.SetValue("![图](https://example.com/a.png)")

	mm, cmd := m.submit()
	m = mm.(Model)
	require.ErrorContains(t, cmd().(sentMsg).err, "empty response")
	require.Empty(t, fake.Uploads)
}

func TestRemoteExt_NamesTheFileAfterTheContentType(t *testing.T) {
	require.Equal(t, ".jpg", remoteExt("image/jpeg"))
	require.Equal(t, ".gif", remoteExt("image/gif"))
	require.Equal(t, ".webp", remoteExt("image/webp"))
	require.Equal(t, ".png", remoteExt("image/png"))
	require.Equal(t, ".png", remoteExt(""), "an unhelpful server still gets a plausible name")
}

func TestRenderBadge_NamesARemoteImage(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.files = fakeFiles(nil)
	m.input.SetValue("![图](https://example.com/a.png)")
	m.replan()

	badge := ansi.Strip(m.renderBadge(m.width - 2))
	require.Contains(t, badge, "image")
	require.Contains(t, badge, "example.com")
}

// A draft that renders to fewer rows than the preview band could hold must
// still leave the badge on the composer's last row: the band is drawn above
// the badge, so rows it claims without filling pad the box underneath.
func TestComposerRows_ShortPreviewLeavesNoRowUnderBadge(t *testing.T) {
	m := sized(120, 40)
	m.mode, m.previewOpen = modeInsert, true
	m.input.SetValue("- abc\n- def")
	m.input.Focus()
	m.replan()
	m.layout()

	require.Less(t, len(m.previewRows), previewMaxRows, "the fixture has to be shorter than the band allows")

	lines := strings.Split(ansi.Strip(m.renderInput()), "\n")
	last := lines[len(lines)-2] // the row above the box's bottom border
	require.Contains(t, last, m.draft.kind.msgType(), "the badge is the composer's last row")
	require.Equal(t, len(m.previewRows), m.composerRows().preview, "the band claims only the rows the preview has")
}

package tui

import (
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"
	"github.com/stretchr/testify/require"
)

func TestForward_BracketedPasteReplansTheDraft(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)

	mm, _ = m.Update(tea.PasteMsg{Content: "- a\n- b"})
	m = mm.(Model)

	require.Equal(t, "- a\n- b", m.input.Value())
	require.Equal(t, kindPost, m.draft.kind, "a paste changes the draft as surely as a keystroke")
	require.Contains(t, ansi.Strip(m.renderBadge(m.width-2)), "post")
}

func TestForward_BracketedPasteGrowsTheComposer(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	require.Equal(t, inputHeight, m.composerRows().input)

	mm, _ = m.Update(tea.PasteMsg{Content: strings.Repeat("line\n", 5) + "line"})
	m = mm.(Model)

	require.Equal(t, 6, m.composerRows().input)
	require.Equal(t, 6, m.input.Height(), "the textarea was laid out again")
}

func TestForward_LeavesOtherModesAlone(t *testing.T) {
	m, _ := newOutboxModel(t)
	require.Equal(t, modeNormal, m.mode)

	mm, _ := m.Update(tea.PasteMsg{Content: "## 发布说明"})
	m = mm.(Model)

	require.Empty(t, m.input.Value(), "a paste outside the composer reaches nothing")
	require.Equal(t, kindText, m.draft.kind)
}

// The flavour lists below are what `osascript -e 'clipboard info'` actually
// printed on macOS for each case.
func TestClipFlavour_DispatchesOnWhatTheClipboardNames(t *testing.T) {
	for _, tc := range []struct {
		name string
		info string
		want clipKind
	}{
		{"screenshot", "«class PNGf», 144441, «class AVIF», 5116, «class 8BPS», 1037488, GIF picture, 6137, TIFF picture, 30885678", clipImage},
		{"copied file", "«class furl», 25", clipFile},
		{"alias record", "alias, 252", clipFile},
		{"plain text", "«class utf8», 11, «class ut16», 24, string, 11, Unicode text, 22", clipText},
		{"empty", "«class utf8», 0, «class ut16», 2, string, 0, Unicode text, 0", clipText},
		{"nothing known", "", clipEmpty},
	} {
		require.Equal(t, tc.want, clipFlavour(tc.info), tc.name)
	}
}

func TestClipFlavour_TextIsNeverAFile(t *testing.T) {
	// Asking the clipboard for a file URL succeeds on plain text and returns
	// "/hello" for the text "hello". Text must never reach that coercion.
	require.Equal(t, clipText, clipFlavour("«class utf8», 5, «class ut16», 12, string, 5, Unicode text, 10"))
}

func TestIsImagePath_KnowsWhatFeishuDraws(t *testing.T) {
	for _, p := range []string{"/a/shot.png", "/a/shot.JPG", "/a/b.jpeg", "/a/b.gif", "/a/b.webp", "/a/b.bmp"} {
		require.True(t, isImagePath(p), p)
	}
	for _, p := range []string{"/a/合同.pdf", "/a/clip.mp4", "/a/notes", "/a/b.png.txt"} {
		require.False(t, isImagePath(p), p)
	}
}

func TestImageRef_WrapsOnlyAPathThatNeedsIt(t *testing.T) {
	require.Equal(t, "![](/Users/linlan/shot.png)", imageRef("/Users/linlan/shot.png"))
	require.Equal(t, "![](</Users/linlan/Screenshot 2026-09-25 at 08.25.13.png>)",
		imageRef("/Users/linlan/Screenshot 2026-09-25 at 08.25.13.png"))
}

// pasteInto drives one clipboard read into a composer already holding draft,
// the way ctrl+v does.
func pasteInto(t *testing.T, m Model, draft string, c clip, err error) Model {
	t.Helper()
	m.deps.Clipboard = func(string) (clip, error) { return c, err }
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.input.SetValue(draft)
	m.replan()

	out, cmd := m.onInsertKey(tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl})
	m = out.(Model)
	require.NotNil(t, cmd, "ctrl+v reads the clipboard")
	require.Equal(t, draft, m.input.Value(), "the textarea never saw the key")

	out, _ = m.Update(cmd())
	return out.(Model)
}

func TestPaste_ImageStagesAndInsertsAReference(t *testing.T) {
	m, _ := newOutboxModel(t)
	staged := filepath.Join(t.TempDir(), "paste-1758790000000.png")
	require.NoError(t, os.WriteFile(staged, []byte("png"), 0o600))

	m = pasteInto(t, m, "", clip{kind: clipImage, path: staged}, nil)

	require.Equal(t, imageRef(staged), m.input.Value())
	require.Equal(t, kindImage, m.draft.kind)
	require.NoError(t, m.draftErr)
	require.Equal(t, staged, m.draft.images[0].local)
	require.Contains(t, ansi.Strip(m.renderBadge(m.width-2)), "image")
}

func TestPaste_ImageIntoProseBecomesAPost(t *testing.T) {
	m, _ := newOutboxModel(t)
	staged := filepath.Join(t.TempDir(), "paste-1.png")
	require.NoError(t, os.WriteFile(staged, []byte("png"), 0o600))

	m = pasteInto(t, m, "看这个 ", clip{kind: clipImage, path: staged}, nil)

	require.Equal(t, "看这个 "+imageRef(staged), m.input.Value())
	require.Equal(t, kindPost, m.draft.kind)
	require.Len(t, m.draft.uploads(), 1)
}

func TestPaste_FileGoesInAsAPictureOrAnAttachment(t *testing.T) {
	dir := t.TempDir()
	shot := filepath.Join(dir, "shot.png")
	pdf := filepath.Join(dir, "合同.pdf")
	require.NoError(t, os.WriteFile(shot, []byte("png"), 0o600))
	require.NoError(t, os.WriteFile(pdf, []byte("pdf"), 0o600))

	m, _ := newOutboxModel(t)
	m = pasteInto(t, m, "", clip{kind: clipFile, path: shot}, nil)
	require.Equal(t, imageRef(shot), m.input.Value())
	require.Equal(t, kindImage, m.draft.kind)

	// Pasted beside text the draft is a post carrying a link, because a file
	// message carries nothing but the file.
	m, _ = newOutboxModel(t)
	m = pasteInto(t, m, "看这个 ", clip{kind: clipFile, path: pdf}, nil)
	require.Equal(t, "看这个 "+fileRef(pdf), m.input.Value())
	require.Equal(t, kindPost, m.draft.kind)

	// Pasted into an empty composer it is the attachment it was copied as.
	m, _ = newOutboxModel(t)
	m = pasteInto(t, m, "", clip{kind: clipFile, path: pdf}, nil)
	require.Equal(t, fileRef(pdf), m.input.Value())
	require.Equal(t, kindFile, m.draft.kind)
	require.Equal(t, pdf, m.draft.file.local)
}

func TestPaste_PathWithSpacesIsWrappedInAngleBrackets(t *testing.T) {
	dir := t.TempDir()
	shot := filepath.Join(dir, "Screenshot 2026-09-25 at 08.25.13.png")
	require.NoError(t, os.WriteFile(shot, []byte("png"), 0o600))

	m, _ := newOutboxModel(t)
	m = pasteInto(t, m, "", clip{kind: clipFile, path: shot}, nil)

	require.Equal(t, "![](<"+shot+">)", m.input.Value())
	require.Equal(t, kindImage, m.draft.kind, "the angle form still reads as one image")
	require.NoError(t, m.draftErr, "and the path inside it still resolves")
	require.Equal(t, shot, m.draft.images[0].local)
}

func TestPaste_TextInsertsAtTheCursor(t *testing.T) {
	m, _ := newOutboxModel(t)
	m = pasteInto(t, m, "看这个 ", clip{kind: clipText, text: "**重点**"}, nil)

	require.Equal(t, "看这个 **重点**", m.input.Value())
	require.Equal(t, kindPost, m.draft.kind)
}

func TestPaste_EmptyClipboardSaysSo(t *testing.T) {
	m, _ := newOutboxModel(t)
	m = pasteInto(t, m, "好的", clip{}, nil)

	require.Equal(t, "好的", m.input.Value(), "nothing was inserted")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "empty")
}

func TestPaste_FailureKeepsTheDraft(t *testing.T) {
	m, _ := newOutboxModel(t)
	m = pasteInto(t, m, "好的", clip{}, errors.New("osascript: exit status 1"))

	require.Equal(t, "好的", m.input.Value())
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "osascript")
}

func TestClassify_AngleBracketImageIsStillAnImage(t *testing.T) {
	require.Equal(t, kindImage, classify("![](</Users/linlan/a b.png>)"))
	require.Equal(t, kindPost, classify("看 ![](</Users/linlan/a b.png>)"))
}

func TestPrunePasted_DropsOnlyWhatIsStale(t *testing.T) {
	data := t.TempDir()
	dir := filepath.Join(data, pastedDir)
	require.NoError(t, os.MkdirAll(dir, 0o700))
	now := time.Now()
	stale := filepath.Join(dir, "paste-old.png")
	fresh := filepath.Join(dir, "paste-new.png")
	for _, p := range []string{stale, fresh} {
		require.NoError(t, os.WriteFile(p, []byte("png"), 0o600))
	}
	require.NoError(t, os.Chtimes(stale, now.Add(-8*24*time.Hour), now.Add(-8*24*time.Hour)))

	prunePasted(data, now)

	require.NoFileExists(t, stale)
	require.FileExists(t, fresh)

	// A data dir that was never pasted into is not a reason to fail starting.
	require.NotPanics(t, func() { prunePasted(t.TempDir(), now) })
}

func TestClipFlavour_WebSelectionIsHTMLBeforeItIsText(t *testing.T) {
	// A browser puts the rich flavour on the pasteboard beside the plain one,
	// so utf8 alone is not what decides.
	require.Equal(t, clipHTML, clipFlavour("«class HTML», 138, «class utf8», 24, string, 24, Unicode text, 48"))
	// A Finder copy carries HTML too; the file it names still wins.
	require.Equal(t, clipFile, clipFlavour("«class furl», 25, «class HTML», 96, «class utf8», 11"))
}

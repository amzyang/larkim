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
	t.Parallel()
	m, _ := newOutboxModel(t)
	m = paste(t, press(t, pickerModel(t), "i"), "- a\n- b")

	require.Equal(t, "- a\n- b", m.input.Value())
	require.Equal(t, kindPost, m.draft.kind, "a paste changes the draft as surely as a keystroke")
	require.Contains(t, ansi.Strip(m.renderBadge(m.width-2)), "post")
}

func TestForward_BracketedPasteGrowsTheComposer(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	require.Equal(t, inputHeight, m.composerRows().input)

	m = paste(t, m, strings.Repeat("line\n", 5)+"line")

	require.Equal(t, 6, m.composerRows().input)
	require.Equal(t, m.textHeight(sideMain, m.composerRows()), m.input.Height(), "the textarea was laid out again")
}

func TestForward_LeavesOtherModesAlone(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	require.Equal(t, modeNormal, m.mode)

	mm, _ := m.Update(tea.PasteMsg{Content: "## 发布说明"})
	m = mm.(Model)

	require.Empty(t, m.input.Value(), "a paste outside the composer reaches nothing")
	require.Equal(t, kindText, m.draft.kind)
}

// Every text input takes a paste the way it takes the keys typed into it:
// forward has to reach the same input onKey does, and whatever the input's
// value drives has to move with it.
func TestForward_EveryInputTakesAPaste(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name  string
		open  func(t *testing.T) Model
		check func(t *testing.T, m Model)
	}{
		{"composer", func(t *testing.T) Model { return press(t, pickerModel(t), "i") },
			func(t *testing.T, m Model) { require.Equal(t, "平台", m.input.Value()) }},
		{"ai prompt", func(t *testing.T) Model {
			f := newFakeAI()
			m := aiFixture(t, f)
			return press(t, m, "a")
		}, func(t *testing.T, m Model) {
			require.Equal(t, "平台", m.aiP.input.Value())
			require.False(t, m.pumShowing(), "a question completes against nothing")
		}},
		{"thread composer", func(t *testing.T) Model {
			m := threadFrame(130, 30)
			m.focus = paneThread
			mm, _ := m.startInsert(nil, false)
			return mm.(Model)
		}, func(t *testing.T, m Model) { require.Equal(t, "平台", m.rightInput.Value()) }},
		{"command line", func(t *testing.T) Model { return cmdModel(t) },
			func(t *testing.T, m Model) { require.Equal(t, "平台", m.cmdline.Value()) }},
		{"chat filter", func(t *testing.T) Model { return press(t, cmdModel(t), "esc", "/") },
			func(t *testing.T, m Model) {
				require.Equal(t, "平台", m.chatFilter)
				vis := m.visibleRows()
				require.Equal(t, "oc_team", rowKeyAt(vis, 0))
				require.Equal(t, "", rowKeyAt(vis, 1), "the list narrows to it")
			}},
		{"search", func(t *testing.T) Model {
			m, _ := panelModel(t)
			mm, _ := m.openSearch("")
			return mm.(Model)
		}, func(t *testing.T, m Model) { require.Equal(t, "平台", m.searchQuery) }},
		{"emoji picker", func(t *testing.T) Model { return press(t, pickerModel(t), "e") },
			func(t *testing.T, m Model) { require.Equal(t, "平台", m.picker.input.Value()) }},
		{"forward", func(t *testing.T) Model {
			m, _ := fwdModel(t)
			mm, _ := m.openForward()
			return mm.(Model)
		}, func(t *testing.T, m Model) {
			require.Equal(t, "平台", m.fwd.input.Value())
			require.Equal(t, "oc_group", m.fwd.hits[0].chatID, "the destinations narrow to it")
		}},
		{"help filter", func(t *testing.T) Model { return press(t, helpModel(100, 30), "/") },
			func(t *testing.T, m Model) {
				require.Equal(t, "平台", m.help.input.Value())
				require.Equal(t, helpSearch("平台"), m.help.hits)
			}},
		{"config filter", func(t *testing.T) Model { return press(t, configModel(t), "/") },
			func(t *testing.T, m Model) { require.Equal(t, configSearch("平台"), m.config.hits) }},
		{"config editor", func(t *testing.T) Model {
			return press(t, configModel(t).openConfig("ai.model"), "enter", "ctrl+u")
		}, func(t *testing.T, m Model) { require.Equal(t, "平台", m.config.editor.Value()) }},
		{"todoist project chooser", func(t *testing.T) Model {
			return openProjects(t, projectModel(t, &fakeTasks{projects: fakeAccount}))
		}, func(t *testing.T, m Model) {
			require.Equal(t, "平台", m.config.project.input.Value())
			require.Equal(t, []string{"平台组"}, projectOffers(m), "the chooser narrows to it")
		}},
		{"silence picker", func(t *testing.T) Model { return press(t, silenceModel(t), "a", "enter") },
			func(t *testing.T, m Model) {
				require.Equal(t, "oc_quiet", m.config.silence.form.pick.hits[0].id, "the picker searches on it")
			}},
		{"silence contains", func(t *testing.T) Model { return press(t, silenceModel(t), "a", "tab", "tab") },
			func(t *testing.T, m Model) { require.Equal(t, "平台", m.config.silence.form.contains.Value()) }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			tc.check(t, paste(t, tc.open(t), "平台"))
		})
	}
}

func TestForward_APasteOnTheCommandLineOpensItsCompletions(t *testing.T) {
	t.Parallel()
	m := paste(t, cmdModel(t), "goto 平台")
	require.Equal(t, []string{"平台组"}, offers(m))
}

// The flavour lists below are what `osascript -e 'clipboard info'` actually
// printed on macOS for each case.
func TestClipFlavour_DispatchesOnWhatTheClipboardNames(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	// Asking the clipboard for a file URL succeeds on plain text and returns
	// "/hello" for the text "hello". Text must never reach that coercion.
	require.Equal(t, clipText, clipFlavour("«class utf8», 5, «class ut16», 12, string, 5, Unicode text, 10"))
}

func TestIsImagePath_KnowsWhatFeishuDraws(t *testing.T) {
	t.Parallel()
	for _, p := range []string{"/a/shot.png", "/a/shot.JPG", "/a/b.jpeg", "/a/b.gif", "/a/b.webp", "/a/b.bmp"} {
		require.True(t, isImagePath(p), p)
	}
	for _, p := range []string{"/a/合同.pdf", "/a/clip.mp4", "/a/notes", "/a/b.png.txt"} {
		require.False(t, isImagePath(p), p)
	}
}

func TestImageRef_WrapsOnlyAPathThatNeedsIt(t *testing.T) {
	t.Parallel()
	require.Equal(t, "![](/Users/linlan/shot.png)", imageRef("/Users/linlan/shot.png"))
	require.Equal(t, "![](</Users/linlan/Screenshot 2026-09-25 at 08.25.13.png>)",
		imageRef("/Users/linlan/Screenshot 2026-09-25 at 08.25.13.png"))
}

// pasteInto drives one clipboard read into a composer already holding draft,
// the way ctrl+v or super+v (Cmd+V) does.
func pasteInto(t *testing.T, m Model, draft string, c clip, err error) Model {
	return pasteIntoKey(t, m, draft, c, err, tea.KeyPressMsg{Code: 'v', Mod: tea.ModCtrl}, "ctrl+v reads the clipboard")
}

func pasteIntoKey(t *testing.T, m Model, draft string, c clip, err error, key tea.KeyPressMsg, readLabel string) Model {
	t.Helper()
	m.deps.Clipboard = func(string) (clip, error) { return c, err }
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.input.SetValue(draft)
	m.replan()

	out, cmd := m.onInsertKey(key)
	m = out.(Model)
	require.NotNil(t, cmd, readLabel)
	require.Equal(t, draft, m.input.Value(), "the textarea never saw the key")

	out, _ = m.Update(cmd())
	return out.(Model)
}

func TestPaste_ImageStagesAndInsertsAReference(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	m, _ := newOutboxModel(t)
	staged := filepath.Join(t.TempDir(), "paste-1.png")
	require.NoError(t, os.WriteFile(staged, []byte("png"), 0o600))

	m = pasteInto(t, m, "看这个 ", clip{kind: clipImage, path: staged}, nil)

	require.Equal(t, "看这个 "+imageRef(staged), m.input.Value())
	require.Equal(t, kindPost, m.draft.kind)
	require.Len(t, m.draft.uploads(), 1)
}

func TestPaste_FileGoesInAsAPictureOrAnAttachment(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
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

func TestPaste_SuperVReadsClipboard(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m = pasteIntoKey(t, m, "", clip{kind: clipText, text: "from cmd"}, nil,
		tea.KeyPressMsg{Code: 'v', Mod: tea.ModSuper}, "super+v reads the clipboard")
	require.Equal(t, "from cmd", m.input.Value())
}

// pasteFromClipboardKey drives ctrl+v / super+v through Update the way the app does.
func pasteFromClipboardKey(t *testing.T, m Model, c clip, key tea.KeyPressMsg) Model {
	t.Helper()
	m.deps.Clipboard = func(string) (clip, error) { return c, nil }
	next, cmd := m.Update(key)
	m = next.(Model)
	require.NotNil(t, cmd, key.String()+" should read the clipboard")
	out, _ := m.Update(cmd())
	return out.(Model)
}

func TestSilenceContains_SuperVReadsClipboard(t *testing.T) {
	t.Parallel()
	m := press(t, silenceModel(t), "a", "tab", "tab")
	m = pasteFromClipboardKey(t, m, clip{kind: clipText, text: "nightly build"},
		tea.KeyPressMsg{Code: 'v', Mod: tea.ModSuper})
	require.Equal(t, "nightly build", m.config.silence.form.contains.Value())
}

func TestConfigEditor_SuperVReadsClipboard(t *testing.T) {
	t.Parallel()
	m := press(t, configModel(t).openConfig("ai.model"), "enter", "ctrl+u")
	m = pasteFromClipboardKey(t, m, clip{kind: clipText, text: "gpt-4"},
		tea.KeyPressMsg{Code: 'v', Mod: tea.ModSuper})
	require.Equal(t, "gpt-4", m.config.editor.Value())
}

func TestPaste_BracketedPasteReReadsPasteboard(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.deps.Clipboard = func(string) (clip, error) {
		return clip{kind: clipText, text: "**bold**"}, nil
	}
	next, cmd := m.Update(tea.PasteMsg{Content: "plain from terminal"})
	require.NotNil(t, cmd)
	out, _ := next.(Model).Update(cmd())
	m = out.(Model)
	require.Equal(t, "**bold**", m.input.Value(), "the pasteboard wins over bracketed text")
}

func TestPaste_TextInsertsAtTheCursor(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m = pasteInto(t, m, "看这个 ", clip{kind: clipText, text: "**重点**"}, nil)

	require.Equal(t, "看这个 **重点**", m.input.Value())
	require.Equal(t, kindPost, m.draft.kind)
}

func TestPaste_EmptyClipboardSaysSo(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m = pasteInto(t, m, "好的", clip{}, nil)

	require.Equal(t, "好的", m.input.Value(), "nothing was inserted")
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "empty")
}

func TestPaste_FailureKeepsTheDraft(t *testing.T) {
	t.Parallel()
	m, _ := newOutboxModel(t)
	m = pasteInto(t, m, "好的", clip{}, errors.New("osascript: exit status 1"))

	require.Equal(t, "好的", m.input.Value())
	require.True(t, m.noticeErr)
	require.Contains(t, m.notice, "osascript")
}

func TestClassify_AngleBracketImageIsStillAnImage(t *testing.T) {
	t.Parallel()
	require.Equal(t, kindImage, classify("![](</Users/linlan/a b.png>)"))
	require.Equal(t, kindPost, classify("看 ![](</Users/linlan/a b.png>)"))
}

func TestPrunePasted_DropsOnlyWhatIsStale(t *testing.T) {
	t.Parallel()
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
	t.Parallel()
	// A browser puts the rich flavour on the pasteboard beside the plain one,
	// so utf8 alone is not what decides.
	require.Equal(t, clipHTML, clipFlavour("«class HTML», 138, «class utf8», 24, string, 24, Unicode text, 48"))
	// A Finder copy carries HTML too; the file it names still wins.
	require.Equal(t, clipFile, clipFlavour("«class furl», 25, «class HTML», 96, «class utf8», 11"))
}

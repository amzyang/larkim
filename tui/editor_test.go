package tui

import (
	"errors"
	"image"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func envOf(kv map[string]string) func(string) string {
	return func(k string) string { return kv[k] }
}

func TestEditorFor_PrefersVisualThenEditorThenVi(t *testing.T) {
	require.Equal(t, []string{"nvim"}, editorFor(envOf(map[string]string{"VISUAL": "nvim", "EDITOR": "nano"})))
	require.Equal(t, []string{"nano"}, editorFor(envOf(map[string]string{"EDITOR": "nano"})))
	require.Equal(t, []string{"vi"}, editorFor(envOf(nil)))
	// An editor named with arguments keeps them.
	require.Equal(t, []string{"code", "--wait"}, editorFor(envOf(map[string]string{"VISUAL": "code --wait"})))
	// A variable set to whitespace is no editor at all.
	require.Equal(t, []string{"vi"}, editorFor(envOf(map[string]string{"VISUAL": "  "})))
}

func TestOnInsertKey_CtrlGDoesNotReachTheTextarea(t *testing.T) {
	m, _ := newOutboxModel(t)
	m.deps.Env = envOf(map[string]string{"EDITOR": "true"})
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.input.SetValue("好的")

	mm, cmd := m.onInsertKey(tea.KeyPressMsg{Code: 'g', Mod: tea.ModCtrl})
	m = mm.(Model)

	require.NotNil(t, cmd, "the editor is launched")
	require.Equal(t, "好的", m.input.Value(), "the textarea never saw the key")
}

func TestEdited_CleanExitTakesTheFileVerbatim(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.input.SetValue("好的")

	path := filepath.Join(t.TempDir(), "larkim.md")
	require.NoError(t, os.WriteFile(path, []byte("## 发布说明\n\n- 修复了 A"), 0o644))

	mm, _ = m.Update(editedMsg{path: path})
	m = mm.(Model)

	require.Equal(t, "## 发布说明\n\n- 修复了 A", m.input.Value())
	require.Equal(t, kindPost, m.draft.kind, "the edited draft is classified again")
	require.NoFileExists(t, path, "the scratch file does not outlive the edit")
}

func TestEdited_AnEmptyFileClearsTheDraft(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.input.SetValue("好的")

	path := filepath.Join(t.TempDir(), "larkim.md")
	require.NoError(t, os.WriteFile(path, nil, 0o644))

	mm, _ = m.Update(editedMsg{path: path})
	m = mm.(Model)

	require.Empty(t, m.input.Value(), "an emptied file is the user deleting their draft")
	require.Equal(t, kindText, m.draft.kind)
}

func TestEdited_NonZeroExitKeepsTheDraft(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	m.input.SetValue("好的")

	path := filepath.Join(t.TempDir(), "larkim.md")
	require.NoError(t, os.WriteFile(path, []byte("half typed"), 0o644))

	mm, _ = m.Update(editedMsg{path: path, err: errors.New("exit status 1")})
	m = mm.(Model)

	require.Equal(t, "好的", m.input.Value(), "quitting without saving leaves the draft alone")
	require.True(t, m.noticeErr)
	require.NoFileExists(t, path)
}

func TestEditExternally_WritesTheDraftToAMarkdownFile(t *testing.T) {
	cmd := editExternally(envOf(map[string]string{"EDITOR": "true"}), "## 发布说明")
	require.NotNil(t, cmd)

	// The command is a tea.ExecProcess, so the file it made is found by name.
	entries, err := filepath.Glob(filepath.Join(os.TempDir(), "larkim-*.md"))
	require.NoError(t, err)
	require.NotEmpty(t, entries, "the draft is handed over as .md so the editor highlights it")
	var found bool
	for _, e := range entries {
		b, err := os.ReadFile(e)
		if err == nil && string(b) == "## 发布说明" {
			found = true
			os.Remove(e)
		}
	}
	require.True(t, found)
}

func TestPicturesForget_DropsPlacementsButKeepsFileFacts(t *testing.T) {
	p := &pictures{dataDir: t.TempDir(), size: map[string]image.Point{"/a.png": {X: 10, Y: 10}},
		failed: map[string]bool{"/b.png": true}, id: map[string]int{"k": 1},
		used: map[string]int64{"k": 1}, drew: map[string]bool{"/c.png": true}}

	p.forget()

	require.Empty(t, p.id, "the terminal no longer holds these placements")
	require.Empty(t, p.used)
	require.Len(t, p.size, 1, "a file's pixel size did not change")
	require.Len(t, p.failed, 1)
	require.Len(t, p.drew, 1, "a disc already on disk is still there")

	var nilPics *pictures
	require.NotPanics(t, func() { nilPics.forget() }, "a terminal without graphics answers no picture")
}

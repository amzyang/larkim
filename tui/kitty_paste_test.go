package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

func TestClipboardPasteTarget_SilenceContainsNotGeneralBrowse(t *testing.T) {
	m := silenceModel(t)
	require.False(t, m.clipboardPasteTarget(), "browsing the rule list has no text field")

	m = press(t, m, "a", "tab", "tab")
	require.True(t, m.clipboardPasteTarget())
}

func TestPastedMsg_RoutesToSilenceContains(t *testing.T) {
	m := press(t, silenceModel(t), "a", "tab", "tab")
	m.deps.Clipboard = func(string) (clip, error) {
		return clip{kind: clipText, text: "unused"}, nil
	}
	next, _ := m.Update(pastedMsg{clip: clip{kind: clipText, text: "nightly build"}})
	m = next.(Model)
	require.Equal(t, "nightly build", m.config.silence.form.contains.Value())
}

func TestPastedMsg_ComposerStillUsesTextarea(t *testing.T) {
	m, _ := newOutboxModel(t)
	mm, _ := m.startInsert(nil, false)
	m = mm.(Model)
	next, _ := m.Update(pastedMsg{clip: clip{kind: clipText, text: "hello"}})
	m = next.(Model)
	require.Equal(t, "hello", m.input.Value())
}

func TestIsClipboardPasteKey(t *testing.T) {
	require.True(t, isClipboardPasteKey("ctrl+v"))
	require.True(t, isClipboardPasteKey("super+v"))
	require.False(t, isClipboardPasteKey("v"))
}

func TestPasteFromClipboardKey_UsesOnKeyPath(t *testing.T) {
	m := press(t, helpModel(100, 30), "/")
	key := tea.KeyPressMsg{Code: 'v', Mod: tea.ModSuper}
	require.True(t, isClipboardPasteKey(key.String()))
	require.True(t, m.clipboardPasteTarget())
}

func TestKittyClaimPaste_EmitsSetUserVarWhenInKitty(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	cmd := kittyClaimPaste()
	require.NotNil(t, cmd)
	require.Equal(t, tea.RawMsg{Msg: kittySetInLarkim}, cmd())
}

func TestKittyClaimPaste_IsAbsentOutsideKitty(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "")
	require.Nil(t, kittyClaimPaste())
}

func TestKittyReleasePaste_EmitsClearWhenInKitty(t *testing.T) {
	t.Setenv("KITTY_WINDOW_ID", "1")
	cmd := kittyReleasePaste()
	require.NotNil(t, cmd)
	raw, ok := cmd().(tea.RawMsg)
	require.True(t, ok)
	require.Equal(t, kittyClearInLarkim, raw.Msg)
}

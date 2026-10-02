package tui

import (
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/stretchr/testify/require"
)

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

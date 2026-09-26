package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWindowTitle_PlainWhenNothingUnread(t *testing.T) {
	rows, unread := headRows("uu")
	unread["a"], unread["b"] = 0, 0
	require.Equal(t, "larkim", windowTitle(rows, unread))
}

func TestWindowTitle_PlainBeforeTheChatsLoad(t *testing.T) {
	require.Equal(t, "larkim", windowTitle(nil, nil))
}

func TestWindowTitle_LeadsWithTheCount(t *testing.T) {
	rows, unread := headRows("uuu")
	unread["a"] = 5
	require.Equal(t, "(7) larkim", windowTitle(rows, unread),
		"seven messages are waiting, spread over three chats")
}

func TestWindowTitle_LeavesMutedChatsOut(t *testing.T) {
	rows, unread := headRows("mm")
	require.Equal(t, "larkim", windowTitle(rows, unread))
}

func TestWindowTitle_CapsTheCount(t *testing.T) {
	rows, unread := headRows(strings.Repeat("u", 120))
	require.Equal(t, "(99+) larkim", windowTitle(rows, unread))
}

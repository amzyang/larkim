package tui

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestWindowTitle_PlainWhenNothingUnread(t *testing.T) {
	chats, unread := headChats("uu")
	unread["a"], unread["b"] = 0, 0
	require.Equal(t, "larkim", windowTitle(chats, unread))
}

func TestWindowTitle_PlainBeforeTheChatsLoad(t *testing.T) {
	require.Equal(t, "larkim", windowTitle(nil, nil))
}

func TestWindowTitle_LeadsWithTheCount(t *testing.T) {
	chats, unread := headChats("uuu")
	unread["a"] = 5
	require.Equal(t, "(3) larkim", windowTitle(chats, unread),
		"three chats are waiting, not seven messages")
}

func TestWindowTitle_LeavesMutedChatsOut(t *testing.T) {
	chats, unread := headChats("mm")
	require.Equal(t, "larkim", windowTitle(chats, unread))
}

func TestWindowTitle_CapsTheCount(t *testing.T) {
	chats, unread := headChats(strings.Repeat("u", 120))
	require.Equal(t, "(99+) larkim", windowTitle(chats, unread))
}

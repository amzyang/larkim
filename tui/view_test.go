package tui

import (
	"fmt"
	"strings"
	"testing"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func sized(w, h int) Model {
	m := New(Deps{Self: "ou_me"})
	m.width, m.height = w, h
	for i := range 80 {
		name := fmt.Sprintf("群 %d 【语言】灵创问题及需求沟通群很长很长的名字 %d", i, i)
		if i%3 == 0 {
			name = "line1\nline2 " + name
		}
		m.chats = append(m.chats, store.Chat{ChatID: fmt.Sprintf("oc_%d", i), Name: name, ChatMode: "group"})
	}
	m.chatID = "oc_1"
	for i := range 40 {
		m.msgs = append(m.msgs, store.Message{MessageID: fmt.Sprintf("om_%d", i), ChatID: "oc_1", SenderName: "邹洋", SenderID: "ou_me",
			Content: strings.Repeat("内容很长 content ", 12), RenderedAt: 1, CreateMs: int64(i) * 1000})
	}
	m.layout()
	return m
}

func TestPanesShareHeight(t *testing.T) {
	m := sized(120, 40)
	h := m.bodyHeight()
	require.Equal(t, h+2, lipgloss.Height(m.renderChats(h)), "chats pane = body + border")
	require.Equal(t, h+2, lipgloss.Height(m.renderMessages(h)), "messages pane = body + border")
	m.threadOpen, m.threadID = true, "omt_1"
	m.thread = m.msgs[:5]
	m.layout()
	require.Equal(t, h+2, lipgloss.Height(m.renderThread(h)))
	v := m.View()
	require.Equal(t, m.height, lipgloss.Height(v.Content), "whole view fits the terminal exactly")
	for _, line := range strings.Split(v.Content, "\n") {
		require.LessOrEqual(t, lipgloss.Width(line), m.width, "no line wider than the terminal: %q", line)
	}
}

func TestChatsPaneKeepsOneRowPerChat(t *testing.T) {
	m := sized(120, 30)
	h := m.bodyHeight()
	lines := strings.Split(m.renderChats(h), "\n")
	require.Len(t, lines, h+2)
	require.Contains(t, lines[2], "line1 line2", "multi-line names are flattened onto one row")
}

func TestScrollToKeepsSelectionVisible(t *testing.T) {
	m := sized(120, 20)
	m.msgIdx = len(m.msgs) - 1
	m.scrollMessagesToSelection()
	last := lastRow(m.msgRows, m.msgIdx)
	require.Less(t, last-m.msgTop, m.bodyHeight())
	m.msgIdx = 0
	m.scrollMessagesToSelection()
	require.Zero(t, m.msgTop)
}

func TestHitMapsPanes(t *testing.T) {
	m := sized(120, 30)
	p, row := m.hit(2, 3)
	require.Equal(t, paneChats, p)
	require.Equal(t, 2, row)
	p, row = m.hit(chatsWidth+5, 3)
	require.Equal(t, paneMessages, p)
	require.Equal(t, 1, row, "message body rows start below the header")
	p, _ = m.hit(5, m.bodyHeight()+3)
	require.Equal(t, paneInput, p)
}

func TestStatusBarStaysOneLine(t *testing.T) {
	m := sized(120, 36)
	m = m.notify("no messages match "+strings.Repeat("z", 200), true)
	s := m.renderStatus()
	require.Equal(t, 1, lipgloss.Height(s))
	require.Equal(t, m.width, lipgloss.Width(s))
}

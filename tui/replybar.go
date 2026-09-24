package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/store"
)

// replyBarHint names the key that drops the quote, since nothing else on
// screen says a draft can outlive its target.
const replyBarHint = "^r drops the quote"

// composerHeight is the inner height of the composer: the writing area, and
// above it the row quoting the message a reply will attach to.
func (m Model) composerHeight() int {
	// The emoji picker stands where the composer does, so the panes above it
	// give up exactly the rows it takes and the message being reacted to stays
	// on screen.
	if m.mode == modeEmoji {
		return m.pickerRows() + pickerChrome - 2 // the border is counted outside
	}
	if m.replyTo != nil {
		return inputHeight + 1
	}
	return inputHeight
}

// renderReplyBar quotes the reply's target above the composer the way the
// Feishu client does — who wrote it and how it reads — because a message id
// is not something a person recognises a message by.
func (m Model) renderReplyBar(w int) string {
	x := m.replyTo
	mark, kind := "↩", "reply to "
	if m.inThrd {
		mark, kind = "⤷", "reply in thread to "
	}
	head := stAccent.Render(mark+" "+kind) + stBold.Render(displaySender(*x, m.deps.Self, m.suffixOf(x.SenderID))) + stDim.Render(": ")
	hint := stDim.Render(replyBarHint)
	line := head + stDim.Render(truncate(replyGist(*x), w-lipgloss.Width(head)-lipgloss.Width(hint)-1))
	gap := max(1, w-lipgloss.Width(line)-lipgloss.Width(hint))
	return line + strings.Repeat(" ", gap) + hint
}

// replyGist is the quoted message on one line, styles stripped so it can be
// cut to the width left over beside the hint.
func replyGist(x store.Message) string {
	switch {
	case x.Deleted:
		return "(Recalled)"
	case x.RenderedAt == 0:
		return flatten(expandEmoji(pendingText(x.MsgType, x.ContentRaw)))
	}
	if a, ok := attachmentOf(x.MsgType, x.ContentRaw); ok {
		return attachGist(a)
	}
	keys, rest := splitImages(x.Content)
	if text := flatten(expandEmoji(plainInline(rest))); text != "" {
		return text
	}
	if len(keys) > 0 {
		return "[图片]"
	}
	return msgTypeLabel(x.MsgType)
}

// plainInline is renderInline without the styling: a link keeps its label,
// bold and underlined runs keep their text, and an @-everyone reads as the
// name the client gives it.
func plainInline(s string) string {
	s = strings.ReplaceAll(s, allKey, allName)
	return inlineMD.ReplaceAllStringFunc(s, func(m string) string {
		g := inlineMD.FindStringSubmatch(m)
		for _, alt := range g[1:] {
			if strings.TrimSpace(alt) != "" {
				return alt
			}
		}
		return ""
	})
}

// suffixOf reads the account suffix from whichever pane loaded the sender,
// so the quote names a person exactly as the lists do.
func (m Model) suffixOf(senderID string) string {
	if s := m.meta.suffix[senderID]; s != "" {
		return s
	}
	return m.threadMeta.suffix[senderID]
}

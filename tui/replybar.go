package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/store"
)

// replyBarHint names the key that drops the quote, since nothing else on
// screen says a draft can outlive its target.
const replyBarHint = "^r drops the quote"

const (
	// composerMaxRows is as tall as the writing area grows. Past this the
	// message panes are giving up more than the draft is worth.
	composerMaxRows = 10
	// previewMaxRows is as tall as the rendered draft gets shown.
	previewMaxRows = 6
)

// composerRows is how the composer's inner height is split, so the box, the
// textarea and the panes above cannot disagree about who owns which row.
type composerRows struct {
	quote   int // the message a reply will attach to
	preview int // the draft as the message list will draw it
	rule    int // the line parting the preview from the writing area
	pum     int // the completion popup, which sits closest to what it completes
	input   int // the writing area
	badge   int // the row naming the message type the draft will be sent as
}

func (r composerRows) total() int {
	return r.quote + r.preview + r.rule + r.pum + r.input + r.badge
}

// composerRows claims rows in the order the reader needs them: the quote and
// the badge are one line each, the writing area grows with the draft, and the
// preview takes what is left over, up to the rows it has to show. The thing
// being typed into outranks the preview, so on a short terminal the preview
// shrinks and then goes.
//
// The preview band is measured against previewRows, so a caller that changes
// the draft rebuilds the preview before reading the split back.
//
// The badge row is claimed in every mode, including the ones that never draw a
// badge. A box that changed height with the mode would jump the panes above it
// every time the reader pressed i or e, which costs more than the row.
func (m Model) composerRows() composerRows {
	var r composerRows
	if m.replyTo != nil {
		r.quote = 1
	}
	r.badge = 1
	r.input = inputHeight
	// Growing the composer and previewing the draft are both extras, so they
	// spend only the rows left once the panes above have the floor the emoji
	// picker also leaves them. The composer at rest is not held to it: that
	// is the box this client has always drawn.
	extra := max(0, m.height-statusHeight-4-minListRows-r.total()) // composer border + pane border
	room := func(want int) int {
		return clamp(want-inputHeight, 0, min(extra, composerMaxRows-inputHeight))
	}
	switch m.mode {
	case modeInsert:
		grow := room(draftRows(m.input.Value(), m.input.Width()))
		r.input += grow
		// The popup outranks the preview: it is what the reader is choosing
		// from, where the preview only shows what they have already written.
		r.pum = clamp(len(m.pum.hits), 0, min(pumMaxRows, extra-grow))
		if m.previewOpen && m.draft.kind != kindText {
			// Held to the rows the preview has as well as the rows there is
			// room for, and one row short of them for the rule the band is
			// drawn over. Every row the box draws has to be claimed: the box
			// pads an under-filled one below the badge, which is drawn last,
			// and drops the top off an over-filled one.
			r.preview = clamp(extra-grow-r.pum-1, 0, min(previewMaxRows, len(m.previewRows)))
			if r.preview > 0 {
				r.rule = 1
			}
		}
	case modeTarget:
		// The chooser grows to its list the way the writing area grows to a
		// draft. Three rows at rest would put a card's links behind a scroll
		// for the sake of a box that does not move, and the reader pressed o
		// to read the list.
		// One row per target. The line the chooser is titled by rides the
		// badge row, which every mode claims anyway.
		r.input += room(len(m.targets.zones))
	}
	return r
}

// draftRows is how many rows a draft takes once the composer wraps it.
// textarea.LineCount counts logical lines, so a long paragraph typed without
// a newline reports one row while occupying several.
func draftRows(value string, width int) int {
	if width < 1 {
		return 1
	}
	n := 0
	for _, line := range strings.Split(value, "\n") {
		n += max(1, (lipgloss.Width(line)+width-1)/width)
	}
	return max(1, n)
}

// composerHeight is the inner height of the composer, and of the emoji picker
// that stands in its place.
func (m Model) composerHeight() int { return m.composerRows().total() }

// previewBottom is as far as the preview band scrolls.
func (m Model) previewBottom() int {
	return max(0, len(m.previewRows)-m.composerRows().preview)
}

// composerBand names the part of the composer a row inside the box belongs to,
// so the wheel scrolls what the pointer is over. Only the two scrollable parts
// are named; the quote, the rule under the preview, the popup and the badge
// are all bandOther, and so is a row outside the box.
type composerBand int

const (
	bandOther composerBand = iota
	bandPreview
	bandInput
)

// composerBand reads the same row split renderInput draws and cursorAt counts.
func (m Model) composerBand(row int) composerBand {
	r := m.composerRows()
	first := len(m.composerAbove(m.width - 2))
	switch {
	case row >= 0 && row < r.preview:
		return bandPreview
	case row >= first && row < first+r.input:
		return bandInput
	}
	return bandOther
}

// composerAbove is what the box draws over the writing area, top row first.
// renderInput lays these rows out and cursorAt counts them, and the two must
// not disagree about which row the writing area starts on. It draws exactly
// the rows composerRows claimed above the writing area, so the box neither
// pads nor clips.
func (m Model) composerAbove(w int) []string {
	var lines []string
	if r := m.composerRows(); r.preview > 0 {
		for _, row := range m.previewRows[m.previewTop:min(len(m.previewRows), m.previewTop+r.preview)] {
			line, _ := m.rowLine(row, w)
			lines = append(lines, line)
		}
		lines = append(lines, paneRule(w))
	}
	if m.replyTo != nil {
		lines = append(lines, m.renderReplyBar(w))
	}
	// Last, so the offers sit directly over the run being completed — which at
	// the bottom of the screen is where a popup menu opens.
	return append(lines, m.pumLines(w)...)
}

// renderReplyBar quotes the reply's target above the composer the way the
// Feishu client does — who wrote it and how it reads — because a message id
// is not something a person recognises a message by.
func (m Model) renderReplyBar(w int) string {
	hint := stDim.Render(replyBarHint)
	head, gist, room := m.replyBarParts(w)
	line, used := "", 0
	if segs := gistSegs(head, gist, room, stDim, m.chatPics().gist); segs != nil {
		line, used = m.joinSegsWidth(segs), segsWidth(segs)
	} else {
		line = head + stDim.Render(truncate(gist, room-lipgloss.Width(head)))
		used = lipgloss.Width(line)
	}
	return line + strings.Repeat(" ", max(1, w-used-lipgloss.Width(hint))) + hint
}

// replyBarParts is the bar's head, the gist beside it and the columns the two
// share once the hint at the far edge has taken its own. The pane asks for
// them twice — once to claim the gist's pictures from the renderer, once to
// draw them — so they are worked out in one place.
func (m Model) replyBarParts(w int) (head, gist string, room int) {
	x := m.replyTo
	mark, kind := "↩", "reply to "
	if m.inThrd {
		mark, kind = "⤷", "reply in thread to "
	}
	head = stAccent.Render(mark+" "+kind) + stBold.Render(displaySender(*x, m.deps.Self, m.suffixOf(x.SenderID))) + stDim.Render(": ")
	return head, replyGist(*x), w - lipgloss.Width(stDim.Render(replyBarHint)) - 1
}

// replyGist is the quoted message on one line, styles stripped so it can be
// cut to the width left over beside the hint.
func replyGist(x store.Message) string {
	switch {
	case x.Deleted:
		return "(Recalled)"
	case x.MsgType == "merge_forward":
		return msgTypeLabel(x.MsgType)
	case x.RenderedAt == 0:
		return flatten(expandEmoji(pendingText(x.MsgType, x.ContentRaw)))
	}
	if a, ok := attachmentOf(x.MsgType, x.ContentRaw); ok {
		return attachGist(a)
	}
	if c, ok := card.Parse(x.ContentRaw); ok {
		return flatten(expandEmoji(cardGist(c)))
	}
	keys, rest := splitImages(x.Content)
	if text := flatten(expandEmoji(plainInline(rest))); text != "" {
		return text
	}
	if len(keys) > 0 {
		return "[Image]"
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

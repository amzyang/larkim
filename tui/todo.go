package tui

import (
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/store"
	"github.com/amzyang/larkim/sync"
)

// todoRows card a task the way the client draws one: the checkbox with the
// summary, and the deadline under it. The box is the one state a body never
// carries — it comes from the task list — so the card's text is the
// rendering's, with the open box standing in until one lands.
//
// The box and the summary beside it lead different ways, the way they do in
// the client: pressing the box toggles the task, while the text opens the
// task's detail page.
func todoRows(x store.Message, idx int, st msgStyle, g *leads) []msgRow {
	guid, ok := sync.ParseTodo(x.ContentRaw)
	if !ok {
		return nil
	}
	link := applink.TodoLink(guid)
	lines := cardLines{g: g, idx: idx, label: "the task", note: "opening the task"}
	display, done := todoDisplay(x)
	wrapped := wrap(display, st.inner())
	for i, l := range wrapped {
		if i > 0 {
			// The lines past the first are the detail lines a card dims:
			// the deadline, and the paragraphs under a long summary.
			lines.add(stDim.Render(l), link)
			continue
		}
		lines.rows = append(lines.rows, todoBoxRow(guid, l, done, idx, g))
	}
	return lines.rows
}

// todoDisplay reads the card's text out of the rendering, swapping the plain
// box it stores for the circles the terminal draws a task's state with. What
// the database holds stays free of private-use glyphs; the swap is the
// card's alone, and done reports the state the swapped glyph stands for.
//
// A leftover <todo>…</todo> from before the checkbox lived in the text is
// not a rendering this reader can use: the first space in that tag is not
// the box, so the body is what the card is built from.
func todoDisplay(x store.Message) (display string, done bool) {
	stored := x.Content
	if !todoStored(stored) {
		stored = sync.TodoText(x.ContentRaw)
	}
	done = x.TodoDone
	box := todoBoxOpen
	if done {
		box = todoBoxDone
	}
	var out []string
	for i, l := range strings.Split(stored, "\n") {
		if i == 0 {
			if _, summary, ok := strings.Cut(l, " "); ok {
				l = box + " " + summary
			} else {
				l = box
			}
		}
		out = append(out, l)
	}
	return strings.Join(out, "\n"), done
}

// todoStored reports a rendering that already carries the checkbox
// todoText writes. Anything else — empty, leftover XML, a type label — is
// not a card this swap can read.
func todoStored(s string) bool {
	return strings.HasPrefix(s, "\u2610") || strings.HasPrefix(s, "\u2611")
}

// todoBoxRow is the card's first line, split between the box and its
// summary: the circles alone toggle the task, the rest of the line opens the
// detail page. Zone columns are measured before the hyperlink goes on, whose
// escapes draw nothing; the icon is the two runes the display began with,
// which wrap never moves.
func todoBoxRow(guid, l string, done bool, idx int, g *leads) msgRow {
	row := msgRow{lead: g.take(), idx: idx, text: stBold.Render(l)}
	link := applink.TodoLink(guid)
	if link == "" {
		return row
	}
	// wrap pads every row to the width it was asked for; styling that
	// padding would strike through the space past the words, so the summary
	// is trimmed back to the text it holds. The space the display put
	// between the icon and the summary stays unstyled and unlinked: it is
	// air, not part of either target.
	rs := []rune(l)
	icon, summary := string(rs[:2]), strings.TrimSpace(string(rs[2:]))
	x0 := row.lead.cols()
	w := lipgloss.Width(icon)
	row.zones = []clickZone{
		{x0: x0, x1: x0 + w, task: guid, taskDone: done},
		{x0: x0 + w + 1, x1: x0 + lipgloss.Width(l), urls: []string{link},
			label: "the task", note: "opening the task"},
	}
	// The done circle takes the green the client checks a task in and its
	// summary the strike through what is finished; the open ones stay the
	// bold of the line they head.
	styled, struck := stBold, stBold
	if done {
		styled, struck = stTodoDone, stTodoStruck
	}
	row.text = styled.Render(icon) + " " + hyperlink(link, struck.Render(summary))
	return row
}

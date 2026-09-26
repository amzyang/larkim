package tui

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/fuzzy"
	"github.com/charmbracelet/x/ansi"
)

// helpEntry is one row of the ? panel. An entry with no keys is a note about
// the binding above it: it carries no key of its own but is still matched
// against, because the word a reader remembers is as often in the prose as in
// the key.
type helpEntry struct {
	mode string
	keys string
	desc string
}

// helpEntries is what ? documents, one binding per row. The prose is kept
// short enough to sit beside its key: a line that has to be read twice is the
// thing the panel exists to avoid.
var helpEntries = []helpEntry{
	{"NORMAL", "j/k", "move"},
	{"NORMAL", "gg/G", "the ends of the list"},
	{"NORMAL", "Ctrl+d/Ctrl+u", "page"},
	{"NORMAL", "Tab/Shift+Tab", "focus the next/previous pane"},
	{"NORMAL", "h/l", "move between panes"},
	{"NORMAL", "Enter", "open the chat, the container, or the reply under the cursor"},
	{"NORMAL", "i", "write"},
	{"NORMAL", "r", "reply"},
	{"NORMAL", "R", "reply in thread"},
	{"NORMAL", "t", "the thread or forwarded bundle under the cursor, in the right pane"},
	{"NORMAL", "I", "the chat's own card in the right pane: what it is, who is in it, the person across a pair"},
	{"NORMAL", "v", "select a range"},
	{"NORMAL", "o", "open what the selected message carries"},
	{"NORMAL", "", "a link, a file, its pictures, the call it invites to, or the message itself in Feishu"},
	{"NORMAL", "e", "react to the selected message"},
	{"NORMAL", "f", "forward it: type to filter chats and people, Enter sends"},
	{"NORMAL", "D", "recall your own message, after a y/n it asks for"},
	{"NORMAL", "n/N", "the next/previous chat with something waiting, muted ones skipped"},
	{"NORMAL", "Y", "copy agent context"},
	{"NORMAL", "yy", "copy the message id"},
	{"NORMAL", "yr", "copy the raw json"},
	{"NORMAL", "yc", "copy the content"},
	{"NORMAL", ".", "send a failed message again"},
	{"NORMAL", "x", "drop a failed message"},
	{"NORMAL", "/", "filter chats"},
	{"NORMAL", "Ctrl+f", "search messages, chats and people"},
	{"NORMAL", ": or ;", "command"},
	{"NORMAL", "?", "this panel"},
	{"NORMAL", "Esc", "back out of the assistant, the search, one pane of the right column, the filter, then the quote"},
	{"NORMAL", "q", "quit"},

	{"VISUAL", "v", "starts in the messages or thread pane"},
	{"VISUAL", "j/k", "extend"},
	{"VISUAL", "Y or yy/yr/yc", "copy and leave"},
	{"VISUAL", "Esc", "cancels"},

	{"INSERT", "Enter", "send"},
	{"INSERT", "Shift+Enter", "newline"},
	{"INSERT", "Ctrl+r", "drop the quote"},
	{"INSERT", "Esc", "back"},
	{"INSERT", "", "markdown sends as a post"},
	{"INSERT", "![](path)", "sends an image"},
	{"INSERT", "[](path)", "sends a file"},
	{"INSERT", "", "the badge under the draft names the type and the files it will upload"},
	{"INSERT", "Ctrl+o", "previews a post or an image the way the message list will draw it"},
	{"INSERT", "Ctrl+g", "opens the draft in $VISUAL or $EDITOR as a markdown file"},
	{"INSERT", "Ctrl+v", "pastes an image, a file path or text from the clipboard"},
	{"INSERT", "@", "completes anyone this chat reaches, its bots and you included"},
	{"INSERT", ": or [", "completes an emoji, once two letters stand after it"},

	{"OPEN", "o", "opens it when a message carries more than one target"},
	{"OPEN", "j/k", "move"},
	{"OPEN", "Enter", "open"},
	{"OPEN", "1-9", "the line it is drawn on"},
	{"OPEN", "Esc", "cancel"},
	{"OPEN", "", "each line names the target and where it leads, a link by its host"},
	{"OPEN", "", "the last line opens the message in Feishu, whatever the list left out"},

	{"FORWARD", "f", "opens it on the selected message"},
	{"FORWARD", "", "type to filter (Chinese, pinyin or initials)"},
	{"FORWARD", "↑↓", "move"},
	{"FORWARD", "Enter", "sends"},
	{"FORWARD", "Esc", "cancels"},
	{"FORWARD", "", "the message being sent on is named below the chooser"},

	{"COMPLETE", "@, : or [", "opens the popup while writing, and typing on narrows it"},
	{"COMPLETE", "Tab or Enter", "accept"},
	{"COMPLETE", "↑↓ or Ctrl+n/Ctrl+p", "move"},
	{"COMPLETE", "Esc", "dismiss, leaving what was typed as text"},
	{"COMPLETE", "", "an emoji goes in as its character, or as the bracketed name where no character carries it"},
	{"COMPLETE", "", "the draft keeps the plain @name, and the tag Feishu notifies on is built from it when the message is sent"},

	{"EMOJI", "e", "opens it on the selected message"},
	{"EMOJI", "", "type to filter (Chinese, pinyin or initials)"},
	{"EMOJI", "↑↓←→", "move"},
	{"EMOJI", "Enter", "react"},
	{"EMOJI", "Esc", "cancel"},
	{"EMOJI", "", "the filter takes the readline keys: Ctrl+w a word, Ctrl+u to the start, Ctrl+a/Ctrl+e ends"},
	{"EMOJI", "", "an emoji already yours is marked ✓, and choosing it takes the reaction back"},
	{"EMOJI", "", "one marked 图 is no longer a reaction: choosing it replies with the picture instead"},

	{"COMMAND", ":copy <200|7d|all>", "put that much of the chat on the clipboard as agent context"},
	{"COMMAND", ":goto <chat>", "open a chat by name or id"},
	{"COMMAND", ":react <emoji>", "react to the selected message"},
	{"COMMAND", ":send <chat|ou_> <text>", "send without opening the chat"},
	{"COMMAND", ":search <text>", "the panel Ctrl+f opens, with the text already in it"},
	{"COMMAND", ":mentions", "every message that named you"},
	{"COMMAND", ":preview", "toggle the composer's preview"},
	{"COMMAND", ":sync", "sync now"},
	{"COMMAND", ":q", "quit"},

	{"ASSISTANT", "a", "ask about this chat, the answer streams in the right pane"},
	{"ASSISTANT", ":ai summary", "what was discussed, decided, and left for you"},
	{"ASSISTANT", ":ai draft <how>", "write a reply the way you describe"},
	{"ASSISTANT", ":ai todo", "every action item directed at you"},
	{"ASSISTANT", ":ai <question>", "anything else about this chat"},
	{"ASSISTANT", "Esc", "closes"},

	{"MOUSE", "click", "focuses and selects"},
	{"MOUSE", "double-click", "opens"},
	{"MOUSE", "", "click a link, a picture, a file card, a card button or Join to open it"},
	{"MOUSE", "", "click a quote to land on the message it names"},
	{"MOUSE", "", "click a forwarded bundle's line to open it in the right pane"},
	{"MOUSE", "", "one opened from inside that pane stacks over it; Esc peels one off"},
	{"MOUSE", "", "click a reaction to add yours or take it back"},
	{"MOUSE", "wheel", "scrolls"},
	{"MOUSE", "", "over the preview it scrolls the band; over the writing area the caret follows"},

	{"HELP", "/", "filter these keys"},
	{"HELP", "j/k", "scroll"},
	{"HELP", "wheel", "scroll"},
	{"HELP", "Esc", "close"},
}

var (
	stHelpSection = lipgloss.NewStyle().Bold(true)
	// stHelpKey is the only colour in the table, so a key is what the eye
	// lands on when the panel is opened to look one up.
	stHelpKey  = lipgloss.NewStyle().Bold(true).Foreground(colAccent)
	stHelpDesc = lipgloss.NewStyle()
)

// helpHit is one entry the filter kept, with the runes of it the query landed
// on. Keys and prose are marked apart because they are drawn in separate
// columns.
type helpHit struct {
	entry    helpEntry
	keyMark  []int
	descMark []int
}

// helpPanel is the ? overlay. A zero value is closed.
type helpPanel struct {
	open bool
	// input is the filter, armed by /. It is the text input every other
	// chooser is built on, so the query takes the same readline keys.
	input     textinput.Model
	filtering bool
	hits      []helpHit
	top       int
}

func (m Model) openHelp() Model {
	in := textinput.New()
	in.Prompt = ""
	in.SetStyles(textinput.DefaultStyles(m.dark))
	in.SetVirtualCursor(false)
	m.help = helpPanel{open: true, input: in, hits: helpSearch("")}
	return m
}

func (m Model) closeHelp() Model {
	m.help = helpPanel{}
	return m
}

// helpSearch narrows the table. Keys, prose and mode name are matched apart so
// a query lands on whichever of them the reader had in mind — "insert", "ctrl"
// and "clipboard" all reach the paste binding — and only the column it matched
// is underlined.
func helpSearch(query string) []helpHit {
	ix := fuzzy.NewIndex()
	q := strings.ToLower(strings.TrimSpace(query))
	var near, far []helpHit
	for i, e := range helpEntries {
		id := strconv.Itoa(i)
		km, keyOK := ix.Match(id+"k", e.keys, query)
		dm, descOK := ix.Match(id+"d", e.desc, query)
		_, modeOK := ix.Match(id+"m", e.mode, query)
		if !keyOK && !descOK && !modeOK {
			continue
		}
		h := helpHit{entry: e, keyMark: km, descMark: dm}
		// A row the query is literally in is the one the reader meant. fzf
		// spells "recall" out of "the call it invites to" as well, and that
		// answer belongs under the real one rather than over it.
		if q != "" && !strings.Contains(strings.ToLower(e.mode+" "+e.keys+" "+e.desc), q) {
			far = append(far, h)
			continue
		}
		near = append(near, h)
	}
	return append(near, far...)
}

// helpWidth is the table's own width: the screen less the overlay's margin,
// border and padding.
func (m Model) helpWidth() int { return max(20, m.width-8) }

// helpRows is how many rows of the table fit: the screen less the border, the
// query line and the blank under it.
func (m Model) helpRows() int { return max(1, m.height-4) }

func (m Model) helpBottom() int { return max(0, len(m.helpLines())-m.helpRows()) }

func (m *Model) helpScroll(d int) { m.help.top = clamp(m.help.top+d, 0, m.helpBottom()) }

// onHelpKey drives the panel. Once / has armed the filter it owns every key it
// can edit with, so ^w cuts a word out of the query rather than scrolling the
// table under it.
func (m Model) onHelpKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	s := k.String()
	if m.help.filtering {
		switch s {
		case "esc":
			// Esc backs out one step at a time, the way it does out of the
			// chat filter: the query first, the panel only once it is empty.
			if m.help.input.Value() == "" {
				return m.closeHelp(), nil
			}
			m.help.input.SetValue("")
			m.help.filtering = false
			m.help.input.Blur()
			m.help.hits, m.help.top = helpSearch(""), 0
			return m, nil
		case "enter":
			m.help.filtering = false
			m.help.input.Blur()
			return m, nil
		case "up", "ctrl+p":
			m.helpScroll(-1)
			return m, nil
		case "down", "ctrl+n":
			m.helpScroll(1)
			return m, nil
		}
		before := m.help.input.Value()
		var cmd tea.Cmd
		m.help.input, cmd = m.help.input.Update(k)
		if q := m.help.input.Value(); q != before {
			m.help.hits, m.help.top = helpSearch(q), 0
		}
		return m, cmd
	}
	switch s {
	case "/":
		m.help.filtering = true
		return m, m.help.input.Focus()
	case "j", "down":
		m.helpScroll(1)
	case "k", "up":
		m.helpScroll(-1)
	case "ctrl+d", "pgdown":
		m.helpScroll(m.helpRows() / 2)
	case "ctrl+u", "pgup":
		m.helpScroll(-m.helpRows() / 2)
	case "g", "home":
		m.help.top = 0
	case "G", "end":
		m.help.top = m.helpBottom()
	default:
		// Anything else is a reader done reading, which is how the panel has
		// always closed.
		return m.closeHelp(), nil
	}
	return m, nil
}

// helpKeyWidth is the column the keys are laid out in, measured over the rows
// drawn together rather than the whole table: a section of long command names
// should not push every other section's prose across the screen.
func helpKeyWidth(hits []helpHit) int {
	w := 0
	for _, h := range hits {
		w = max(w, lipgloss.Width(h.entry.keys))
	}
	return w
}

// helpWrap folds a description at the column, keeping each line's rune offsets
// into the original so the filter's underlines land on the same characters
// after the fold. Wrapping the styled string will not do: the style closes at
// the end of it and every line after the first comes out bare.
func helpWrap(s string, mark []int, style lipgloss.Style, limit int) []string {
	src := []rune(s)
	var out []string
	at := 0
	for _, line := range strings.Split(ansi.Wordwrap(s, limit, ""), "\n") {
		for at < len(src) && src[at] == ' ' {
			at++
		}
		end := min(len(src), at+len([]rune(line)))
		var pos []int
		for _, p := range mark {
			if p >= at && p < end {
				pos = append(pos, p-at)
			}
		}
		out = append(out, markName(string(src[at:end]), pos, style))
		at = end
	}
	return out
}

// helpRow draws one entry: its keys in their column, its prose wrapped into
// what is left and indented under itself, so a long line folds rather than
// running out of the box.
func helpRow(lead string, h helpHit, keyw, w int) []string {
	const gap = "  "
	style := stHelpDesc
	keys := markName(h.entry.keys, h.keyMark, stHelpKey)
	if h.entry.keys == "" {
		// A note belongs to the binding above it, so it is dimmed and left to
		// stand in the prose column with nothing in the key one.
		style, keys = stDim, ""
	}
	head := lead + fit(keys, keyw) + gap
	indent := strings.Repeat(" ", lipgloss.Width(lead)+keyw+len(gap))
	var out []string
	for i, line := range helpWrap(h.entry.desc, h.descMark, style, max(10, w-lipgloss.Width(indent))) {
		if i == 0 {
			out = append(out, fit(head+line, w))
			continue
		}
		out = append(out, fit(indent+line, w))
	}
	return out
}

// helpLines is the whole table, laid out for the current width. Unfiltered it
// is grouped under its mode names; filtered the groups are dropped and each
// row names its own mode, because one surviving row under a heading reads as a
// section rather than as a hit.
func (m Model) helpLines() []string {
	w := m.helpWidth()
	var out []string
	if strings.TrimSpace(m.help.input.Value()) == "" {
		for i := 0; i < len(m.help.hits); {
			mode := m.help.hits[i].entry.mode
			j := i
			for j < len(m.help.hits) && m.help.hits[j].entry.mode == mode {
				j++
			}
			group := m.help.hits[i:j]
			if i > 0 {
				out = append(out, fit("", w))
			}
			out = append(out, fit(stHelpSection.Render(mode), w))
			keyw := helpKeyWidth(group)
			for _, h := range group {
				out = append(out, helpRow("  ", h, keyw, w)...)
			}
			i = j
		}
		return out
	}
	modew := 0
	for _, h := range m.help.hits {
		modew = max(modew, lipgloss.Width(h.entry.mode))
	}
	keyw := helpKeyWidth(m.help.hits)
	for _, h := range m.help.hits {
		out = append(out, helpRow(fit(stDim.Render(h.entry.mode), modew)+" ", h, keyw, w)...)
	}
	return out
}

// helpPrompt labels the query box. The cursor is placed past it, so its width
// is not measured in two places.
func helpPrompt() string { return stBold.Render("help") + stAccent.Render(" › ") }

// helpLeft is the column the table starts in: the overlay is centred in a
// screen four columns wider than its box, and then costs a border and a pad.
const helpLeft = 4

func (m Model) renderHelp() string {
	w, rows := m.helpWidth(), m.helpRows()
	head := padBetween(stHelpSection.Render("help"),
		stDim.Render("/ filter · j/k scroll · esc close"), w)
	if m.help.filtering || strings.TrimSpace(m.help.input.Value()) != "" {
		head = padBetween(helpPrompt()+m.help.input.View(),
			stDim.Render(strconv.Itoa(len(m.help.hits))+"/"+strconv.Itoa(len(helpEntries))), w)
	}
	lines := m.helpLines()
	if len(lines) == 0 {
		lines = []string{fit(stDim.Render("nothing matches "+m.help.input.Value()), w)}
	}
	top := clamp(m.help.top, 0, max(0, len(lines)-rows))
	body := lines[top:min(len(lines), top+rows)]
	for len(body) < rows {
		body = append(body, fit("", w))
	}
	return paneStyle(true, w).Padding(0, 1).
		Render(strings.Join(append([]string{head, fit("", w)}, body...), "\n"))
}

// helpCursor puts the caret in the query box. The overlay is drawn as one
// block rather than into the pane grid, so its coordinates are counted from
// the screen rather than taken from the layout.
func (m Model) helpCursor() *tea.Cursor {
	if !m.help.filtering {
		return nil
	}
	c := textinputCursor(m.help.input)
	if c == nil {
		return nil
	}
	c.X += helpLeft + lipgloss.Width(helpPrompt())
	c.Y++
	c.Shape, c.Blink = tea.CursorBar, true
	return c
}

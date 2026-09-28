package tui

import (
	"cmp"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/emoji"
)

// cmdCompLimit is how many offers a command's argument is narrowed to before
// the reader is better off typing more of what they want. It matches the
// forward chooser's own ceiling, which lists the same chats and people.
const cmdCompLimit = 50

// cmdHit is one offer the : line makes.
type cmdHit struct {
	// insert is what walking onto this row writes in the field's place.
	insert string
	// label is the row's words, with the runes the query landed on marked.
	label string
	// emoji is set only for a :react offer, for the icon column the row opens
	// with.
	emoji emoji.Emoji
}

// cmdComp is the completion list standing over the : line. Unlike the
// composer's popup it writes into the line as the reader walks it, which is
// what vim's wildmenu does and what keeps Enter honest: the line always says
// what Enter will run. A zero value is closed.
type cmdComp struct {
	// start is where the field being completed begins, in runes, and tail is
	// the line past the cursor. The two bracket what a walk rewrites, so the
	// list can be walked repeatedly without reading back its own writing.
	start int
	tail  string
	// stem is what the reader typed in the field. The hits stay keyed to it,
	// so walking off the end of the list and back lands on it again.
	stem string
	hits []cmdHit
	// idx is -1 until the reader walks onto a row, which is when the line
	// stops being what they typed.
	idx int
	top int
	// dismissed is the line Esc closed the list on. Typing further along that
	// same line leaves it closed.
	dismissed string
}

func (c cmdComp) open() bool { return len(c.hits) > 0 }

// cmdField reads the field the cursor stands in: which token of the line it
// is, where the token starts in runes, and what has been typed of it. A run of
// spaces parts one token from the next, so an extra space between a command
// and its argument does not invent a third field.
func cmdField(line string, pos int) (token, start int, stem string) {
	rs := []rune(line)[:min(pos, len([]rune(line)))]
	for i, r := range rs {
		if r != ' ' {
			continue
		}
		if i > 0 && rs[i-1] != ' ' {
			token++
		}
		start = i + 1
	}
	return token, start, string(rs[start:])
}

// takeCmdComp re-reads the field under the cursor and opens, refilters or
// closes the list. It runs after every key the : line took, so the offers
// follow the line however it changed.
func (m *Model) takeCmdComp() {
	line, pos := m.cmdline.Value(), m.cmdline.Position()
	if m.cmdcomp.dismissed != "" && strings.HasPrefix(line, m.cmdcomp.dismissed) {
		m.cmdcomp = cmdComp{idx: -1, dismissed: m.cmdcomp.dismissed}
		return
	}
	token, start, stem := cmdField(line, pos)
	rs := []rune(line)
	tail := string(rs[min(pos, len(rs)):])
	// An unchanged field keeps its cursor: a key that moved nothing should not
	// throw the reader back off the row they walked onto.
	if m.cmdcomp.open() && m.cmdcomp.start == start && m.cmdcomp.stem == stem && m.cmdcomp.tail == tail {
		return
	}
	m.cmdcomp = cmdComp{start: start, tail: tail, stem: stem, idx: -1, hits: m.cmdHits(line, token, stem)}
}

// closeCmdComp dismisses the list for the rest of the line the reader is on.
func (m *Model) closeCmdComp() {
	m.cmdcomp = cmdComp{idx: -1, dismissed: m.cmdline.Value()}
}

// cmdHits is what a field offers, best first.
//
// The command's own field opens on the first rune typed: a bare : is a bare :,
// not the whole table shoving the panes up before the reader has said
// anything. An argument opens on the empty query instead — having named the
// command is exactly the point at which its chats or its words are what the
// reader wants to see.
func (m Model) cmdHits(line string, token int, stem string) []cmdHit {
	switch token {
	case 0:
		if stem == "" {
			return nil
		}
		var out []cmdHit
		for _, c := range commandsWithPrefix(stem) {
			label := markName(c.name, prefixMark(stem), stBold)
			if c.usage != "" {
				label += " " + stDim.Render(c.usage)
			}
			out = append(out, cmdHit{insert: c.name, label: label})
		}
		return out
	case 1:
		head, _, _ := strings.Cut(strings.TrimLeft(line, " "), " ")
		cmd, ok := resolveCommand(head)
		if !ok {
			return nil
		}
		return m.argHits(cmd, stem)
	}
	// Past the first argument the line is prose — the text of a :send, the
	// question of an :ai — and there is nothing to narrow it against.
	return nil
}

// argHits narrows a command's first argument against what the rest of the TUI
// already knows: the chooser the f key opens over chats and people, the
// reaction set the e key opens, and the fixed words a command spells itself.
func (m Model) argHits(cmd command, stem string) []cmdHit {
	var out []cmdHit
	switch cmd.arg {
	case argEnum:
		for _, w := range cmd.enum {
			if strings.HasPrefix(w, stem) {
				out = append(out, cmdHit{insert: w, label: markName(w, prefixMark(stem), stBold)})
			}
		}
	case argChat, argTarget:
		for _, t := range m.fwdSearch(stem) {
			if cmd.arg == argChat && t.chatID == "" {
				continue
			}
			label := markName(t.name, t.mark, stBold)
			// :send splits its target from its text on the first space, so a
			// name is not a target it can take; the open id is. :goto reads
			// its whole rest and folds it, so it gets the readable name.
			insert := t.name
			if cmd.arg == argTarget {
				insert = cmp.Or(t.chatID, t.userID)
				label += stDim.Render("  " + insert)
			}
			out = append(out, cmdHit{insert: insert, label: label})
		}
	case argEmoji:
		for _, h := range m.emoji.Search(stem) {
			if len(out) == cmdCompLimit {
				break
			}
			// The key rather than the display name, because it is what the
			// reaction is made of and the one spelling no other emoji shares.
			out = append(out, cmdHit{
				insert: h.Emoji.Key,
				label:  emojiWords(h.Emoji, stBold.Render(h.Emoji.Name()), h.Term, h.Positions),
				emoji:  h.Emoji,
			})
		}
	}
	return out
}

// prefixMark is the runes of an offer a prefix query landed on, which for a
// prefix is its whole length.
func prefixMark(stem string) []int {
	mark := make([]int, len([]rune(stem)))
	for i := range mark {
		mark[i] = i
	}
	return mark
}

// onCmdCompKey drives the list. It is reached only while one is open, ahead of
// the : line, so every key here is one the line would otherwise take.
func (m Model) onCmdCompKey(k tea.KeyPressMsg) (Model, bool) {
	switch k.String() {
	case "tab", "down", "ctrl+n":
		return m.walkCmdComp(1), true
	case "shift+tab", "up", "ctrl+p":
		return m.walkCmdComp(-1), true
	case "esc":
		// The list goes, command mode stays: a second Esc leaves it, the way
		// dismissing a menu and leaving the buffer are two presses in vim.
		before := m.composerRows()
		m.closeCmdComp()
		m.tookCmdComp(before)
		return m, true
	}
	return m, false
}

// walkCmdComp moves onto the next offer and writes it into the line. Walking
// past the top lands back on what the reader typed, which is the only way back
// to a stem the list has already overwritten.
func (m Model) walkCmdComp(d int) Model {
	c := &m.cmdcomp
	c.idx = clamp(c.idx+d, -1, len(c.hits)-1)
	if rows := m.cmdCompRows(); c.idx >= 0 {
		c.top = clamp(c.top, max(0, c.idx-rows+1), c.idx)
	}
	text := c.stem
	if c.idx >= 0 {
		text = c.hits[c.idx].insert
	}
	head := string([]rune(m.cmdline.Value())[:c.start])
	m.cmdline.SetValue(head + text + c.tail)
	m.cmdline.SetCursor(c.start + len([]rune(text)))
	return m
}

// tookCmdComp relays out a change in how many rows the list claims, the way
// tookDraft does for the composer.
func (m *Model) tookCmdComp(before composerRows) {
	if before != m.composerRows() {
		m.layout()
	}
}

// cmdCompRows is how many offers the list has room for.
func (m Model) cmdCompRows() int { return m.composerRows().pum }

// cmdCompVisible is the offers the list has room for. The renderer draws
// exactly these and the picture pass claims exactly their pictures.
func (m Model) cmdCompVisible() []cmdHit {
	return window(m.cmdcomp.hits, m.cmdcomp.top, m.cmdCompRows())
}

// cmdCompLines draws the list, top row first, to sit above the : line — so the
// line the reader is typing on stands still as the list grows under their
// hands. Only command mode has one; the filter and the search share the box
// and complete against nothing.
func (m Model) cmdCompLines(w int) []string {
	if m.mode != modeCommand {
		return nil
	}
	var out []string
	for i, h := range m.cmdCompVisible() {
		out = append(out, m.offerLine(h.emoji, h.label, m.cmdcomp.top+i == m.cmdcomp.idx, w))
	}
	return out
}

// cmdCompHint names the keys the list owns while it is open, since it takes
// three the : line otherwise has.
const cmdCompHint = "Tab/^n/^p match · Esc dismiss"

// renderCmdCompHint draws the row the badge has in every other mode: where in
// the list the reader stands, and the keys that move them. It is blank while
// nothing is open, which is every row the filter and the search ever draw.
func (m Model) renderCmdCompHint(w int) string {
	if m.mode != modeCommand || !m.cmdcomp.open() {
		return ""
	}
	where := strconv.Itoa(len(m.cmdcomp.hits))
	if m.cmdcomp.idx >= 0 {
		where = strconv.Itoa(m.cmdcomp.idx+1) + "/" + where
	}
	return padBetween("", stDim.Render(where+" · "+cmdCompHint), w)
}

package tui

import (
	"cmp"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/emoji"
)

// cmdCompLimit is how many offers a command's argument is narrowed to before
// the reader is better off typing more of what they want. It matches the
// forward chooser's own ceiling, which lists the same chats and people.
const cmdCompLimit = 50

// cmdHit is one offer the : line makes. Which of its sources is set says
// what the row is; each projects its own row and box.
type cmdHit struct {
	// insert is what walking onto this row writes in the field's place.
	insert string
	// name is the word the row shows, and mark the runes of it the query
	// landed on.
	name string
	mark []int
	// cmd is set for a command, setting for a key of the configuration, and
	// emoji for a :react offer.
	cmd     command
	setting setting
	emoji   emoji.Hit
	// id is the chat or open id a target names, which the box beside the list
	// shows: :send writes it into the line, and :goto opens it. face is the
	// target's avatar.
	id   string
	face offerIcon
	// info is what the box says about a setting, read from the configuration
	// when the list was filled.
	info []string
}

// cmdSpec draws an offer by what it is: an emoji as its picture and name,
// anything else by its word, with the rest of what it is in the box beside
// the list.
var cmdSpec = menuSpec[cmdHit]{
	row: func(h cmdHit) offer {
		if h.emoji.Emoji.Key != "" {
			return offer{icon: emojiIcon(h.emoji.Emoji), name: stBold.Render(h.emoji.Emoji.Name())}
		}
		return offer{icon: h.face, name: markName(h.name, h.mark, stBold)}
	},
	info: func(h cmdHit) []string {
		switch {
		case h.emoji.Emoji.Key != "":
			return emojiInfo(h.emoji)
		case h.cmd.name != "":
			// The usage is the signature, so it leads; a command that takes
			// nothing has only its help to say.
			if h.cmd.usage == "" {
				return infoLines(h.cmd.help)
			}
			return infoLines(h.cmd.display(), h.cmd.help)
		case h.info != nil:
			return h.info
		}
		return infoLines(h.id)
	},
	noselect: true,
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
	// menu focuses nothing until the reader walks onto a row, which is when
	// the line stops being what they typed.
	menu menu[cmdHit]
	// dismissed is the line Esc closed the list on. Typing further along that
	// same line leaves it closed.
	dismissed string
}

func (c cmdComp) open() bool { return c.menu.open() }

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
		m.cmdcomp = cmdComp{dismissed: m.cmdcomp.dismissed}
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
	m.cmdcomp = cmdComp{start: start, tail: tail, stem: stem, menu: fillMenu(m.cmdHits(line, token, stem), cmdSpec)}
}

// closeCmdComp dismisses the list for the rest of the line the reader is on.
func (m *Model) closeCmdComp() {
	m.cmdcomp = cmdComp{dismissed: m.cmdline.Value()}
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
			out = append(out, cmdHit{insert: c.name, name: c.name, mark: prefixMark(stem), cmd: c})
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
				out = append(out, cmdHit{insert: w, name: w, mark: prefixMark(stem)})
			}
		}
	case argSetting, argConfigKey:
		// :set takes the = along, because an option only ever reads as a pair
		// there and the reader's next keystroke is the value. :config takes
		// the bare key: it opens the panel on that row and the value is
		// typed into the row, not into the line.
		eq := ""
		if cmd.arg == argSetting {
			eq = "="
		}
		// Past the =, a key with a fixed set of values offers them.
		if key, val, typed := strings.Cut(stem, "="); typed && cmd.arg == argSetting {
			for _, v := range config.Values(key) {
				if strings.HasPrefix(v, val) {
					out = append(out, cmdHit{insert: key + "=" + v, name: v, mark: prefixMark(val)})
				}
			}
			break
		}
		for _, st := range settings {
			if cmd.arg == argSetting && !st.live {
				continue
			}
			if strings.HasPrefix(st.key, stem) {
				out = append(out, cmdHit{insert: st.key + eq, name: st.key, mark: prefixMark(stem),
					setting: st, info: settingInfo(m.cfg, st)})
			}
		}
	case argChat, argTarget:
		for _, t := range m.fwdSearch(stem) {
			if cmd.arg == argChat && t.chatID == "" {
				continue
			}
			// :send splits its target from its text on the first space, so a
			// name is not a target it can take; the open id is. :goto reads
			// its whole rest and folds it, so it gets the readable name.
			id := cmp.Or(t.chatID, t.userID)
			insert := t.name
			if cmd.arg == argTarget {
				insert = id
			}
			out = append(out, cmdHit{insert: insert, name: t.name, mark: t.mark, id: id, face: faceIcon(t.seed, t.name, t.file)})
		}
	case argEmoji:
		for _, h := range m.emoji.Search(stem) {
			if len(out) == cmdCompLimit {
				break
			}
			// The key rather than the display name, because it is what the
			// reaction is made of and the one spelling no other emoji shares.
			out = append(out, cmdHit{insert: h.Emoji.Key, emoji: h})
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
		m.closeCmdComp()
		return m, true
	}
	return m, false
}

// walkCmdComp moves onto the next offer and writes it into the line. Walking
// past the top lands back on what the reader typed, which is the only way back
// to a stem the list has already overwritten.
func (m Model) walkCmdComp(d int) Model {
	c := &m.cmdcomp
	c.menu.move(d, m.cmdCompRows())
	text := c.stem
	if h, ok := c.menu.focused(); ok {
		text = h.insert
	}
	head := string([]rune(m.cmdline.Value())[:c.start])
	m.cmdline.SetValue(head + text + c.tail)
	m.cmdline.SetCursor(c.start + len([]rune(text)))
	return m
}

// cmdCompRows is how many offers the list has room for. Only command mode has
// one; the filter and the search share the box and complete against nothing.
func (m Model) cmdCompRows() int {
	if m.mode != modeCommand {
		return 0
	}
	return m.floatRoom(len(m.cmdcomp.menu.items), m.cmdcomp.menu.maxRows())
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
	where := strconv.Itoa(len(m.cmdcomp.menu.items))
	if m.cmdcomp.menu.idx >= 0 {
		where = strconv.Itoa(m.cmdcomp.menu.idx+1) + "/" + where
	}
	return padBetween("", stDim.Render(where+" · "+cmdCompHint), w)
}

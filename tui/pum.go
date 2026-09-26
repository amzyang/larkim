package tui

import (
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/fuzzy"
)

// pumKind is what a completion run completes.
type pumKind int

const (
	pumMention pumKind = iota // `@` — the people in this chat
	pumEmoji                  // `:` or `[` — an emoji, Feishu's own or a Unicode one
)

const (
	// pumMaxRows is as tall as the popup gets. Past a handful of offers a
	// reader stops reading the list and goes on typing the name instead.
	pumMaxRows = 8
	// pumQueryMax bounds how far back a run may reach. A word longer than this
	// is prose that happens to follow a colon, not something being completed.
	pumQueryMax = 32
	// pumEmojiQueryMin is how much of a name must stand before an emoji run
	// opens. A colon and a bracket both carry prose far more often than they
	// open an emoji, and one letter after either is still ambiguous; two is
	// where the reader is plainly spelling a name. A mention has no such floor:
	// `@` alone listing who is here is the point of the trigger.
	pumEmojiQueryMin = 2
)

// pumRun is the completion run standing immediately before the cursor.
type pumRun struct {
	kind pumKind
	// runes is how many runes the run spans, the trigger included, so accepting
	// can erase exactly what was typed.
	runes int
	query string
}

// pumHit is one offer the popup makes.
type pumHit struct {
	// insert is what accepting writes in the run's place, the trailing space
	// apart.
	insert string
	// id is the open id a mention names, and name is what picked is keyed by.
	// Both are empty for an emoji.
	id, name string
	// emoji is set only for an emoji offer, for the icon column the row opens
	// with. A mention has no picture.
	emoji emoji.Emoji
	// label is the row's words, with the runes the query landed on already
	// marked.
	label string
}

// pum is the completion popup standing over the writing area. It is not a mode:
// the trigger and the query stay in the draft and the reader goes on typing
// into the composer, which is the whole difference between this and a chooser
// that takes the box. A zero value is closed.
type pum struct {
	run  pumRun
	hits []pumHit
	idx  int
	top  int
	// dismissed is the line Esc closed the popup on. Typing further along that
	// same line leaves it closed: a reader who dismissed the popup meant the
	// colon literally, and one that came back on the next keystroke would be
	// one they cannot get rid of.
	dismissed string
}

func (p pum) open() bool { return len(p.hits) > 0 }

// showing reports whether the popup has room to be drawn. A popup nobody can
// see must not be taking Enter: on a terminal too short to spare it a row, the
// composer keeps every key it has.
func (m Model) pumShowing() bool { return m.pumRows() > 0 }

// move walks the list and scrolls to keep the cursor on screen.
func (p *pum) move(d, rows int) { moveCursor(&p.idx, &p.top, d, len(p.hits), rows) }

// pumKindOf reports which completion a rune opens, if any.
func pumKindOf(r rune) (pumKind, bool) {
	switch r {
	case '@':
		return pumMention, true
	case ':':
		return pumEmoji, true
	case '[':
		// The client has no bracket trigger; this one is ours. `[鼓掌]` is the
		// spelling a Feishu text message carries, so it is what the reader has
		// been reading all day and reaches for when they mean that emoji.
		return pumEmoji, true
	}
	return 0, false
}

// pumEmojiRune reports whether a rune may stand inside an emoji run. Anything
// else means the colon was punctuation: the `)` of a `:)`, the `/` of a URL.
func pumEmojiRune(r rune) bool {
	return unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '+' || r == '-'
}

// pumRunAt reads the completion run the cursor sits at the end of. line is the
// text before the cursor on the cursor's own line — a run never spans a
// newline, so that is the whole of what can be completed.
//
// A run opens only where atBoundary holds, the rule mentions already resolve
// by. That one rule is what keeps `http://`, `12:30`, `note:` and an email
// address from opening a popup over what the reader is actually writing, and
// pumEmojiQueryMin keeps the rest: a lone `:` or `[` is punctuation until two
// runes of a name stand behind it.
func pumRunAt(line string) (pumRun, bool) {
	rs := []rune(line)
	for i := len(rs) - 1; i >= 0; i-- {
		if len(rs)-1-i > pumQueryMax {
			return pumRun{}, false
		}
		kind, trigger := pumKindOf(rs[i])
		if !trigger {
			if unicode.IsSpace(rs[i]) {
				return pumRun{}, false
			}
			continue
		}
		query := string(rs[i+1:])
		if kind == pumEmoji {
			bad := strings.ContainsFunc(query, func(r rune) bool { return !pumEmojiRune(r) })
			if bad || len(rs)-i-1 < pumEmojiQueryMin {
				return pumRun{}, false
			}
		}
		head := string(rs[:i])
		if !atBoundary(head, len(head)) {
			return pumRun{}, false
		}
		return pumRun{kind: kind, runes: len(rs) - i, query: query}, true
	}
	return pumRun{}, false
}

// lineBeforeCursor is the text the cursor sits at the end of, on its own line.
// textarea counts its column in runes over a []rune row, so a draft written in
// Chinese lands on the same offsets as one written in English.
func lineBeforeCursor(ta textarea.Model) string {
	lines := strings.Split(ta.Value(), "\n")
	row := clamp(ta.Line(), 0, len(lines)-1)
	rs := []rune(lines[row])
	return string(rs[:clamp(ta.Column(), 0, len(rs))])
}

// takePum re-reads the run under the cursor and opens, refilters or closes the
// popup. It runs after every key the writing area took, so the popup follows a
// paste and a cursor move as readily as typing, and there is no second place
// that has to know what a trigger looks like.
func (m *Model) takePum() {
	line := lineBeforeCursor(m.input)
	if m.pum.dismissed != "" && strings.HasPrefix(line, m.pum.dismissed) {
		m.pum = pum{dismissed: m.pum.dismissed}
		return
	}
	run, ok := pumRunAt(line)
	if !ok {
		m.pum = pum{}
		return
	}
	// An unchanged run keeps its cursor: a key that moved nothing in the draft
	// should not throw the reader back to the first offer.
	if m.pum.open() && run == m.pum.run {
		return
	}
	m.pum = pum{run: run, hits: m.pumHits(run)}
}

// closePum dismisses the popup for the rest of the run the reader is on.
func (m *Model) closePum() {
	m.pum = pum{dismissed: lineBeforeCursor(m.input)}
}

// pumHits is what a run offers, best first.
func (m Model) pumHits(run pumRun) []pumHit {
	if run.kind == pumMention {
		return m.mentionHits(run.query)
	}
	return m.emojiHits(run.query)
}

// mentionHits narrows the chat's roster. The whole roster answers an empty
// query, which is what makes @ on its own a list of who is here.
//
// A group offers @All ahead of everyone: it is the one name that is not in the
// roster, and the one most often meant. A chat of two has no room to shout in,
// so it is left out there, matching the client.
func (m Model) mentionHits(query string) []pumHit {
	var out []pumHit
	ix := fuzzy.NewIndex()
	keep := func(id, name string, bot bool) {
		mark, hit := ix.Match(id, name, query)
		if !hit {
			return
		}
		label := markName(name, mark, stBold)
		if bot {
			label += botBadge
		}
		out = append(out, pumHit{insert: "@" + name, id: id, name: name, label: label})
	}
	if c, ok := m.currentChat(); ok && c.ChatMode != "p2p" {
		keep(allKey, strings.TrimPrefix(allName, "@"), false)
	}
	for _, c := range m.roster {
		if c.Name == "" {
			continue
		}
		keep(c.OpenID, c.Name, c.IsBot)
	}
	return out
}

// emojiHits narrows the emoji a message can carry.
func (m Model) emojiHits(query string) []pumHit {
	var out []pumHit
	for _, h := range m.emojiWrite.Search(query) {
		name := h.Emoji.Name()
		label := emojiWords(h.Emoji.Key, name, stBold.Render(name), h.Term, h.Positions)
		out = append(out, pumHit{insert: emojiInsert(h.Emoji), emoji: h.Emoji, label: label})
	}
	return out
}

// emojiInsert is what accepting an emoji writes: the character where one
// carries the same feeling, and the bracketed name where none does.
//
// The name is the English one this client displays, which is the spelling it
// puts on the wire. A bracketed name is resolved against the reading client's
// own table, which holds both languages' names for every emoji, so it draws
// there whichever language that client is set to.
func emojiInsert(e emoji.Emoji) string {
	if e.Glyph != "" {
		return e.Glyph
	}
	return "[" + e.Name() + "]"
}

// onPumKey drives the popup. It is reached only while one is open, ahead of the
// writing area, so every key here is one the composer would otherwise have.
func (m Model) onPumKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch k.String() {
	case "tab", "enter":
		return m.acceptPum(), nil, true
	case "down", "ctrl+n":
		m.pum.move(1, m.pumRows())
		return m, nil, true
	case "up", "ctrl+p":
		m.pum.move(-1, m.pumRows())
		return m, nil, true
	case "esc":
		// The popup goes, insert mode stays: a second Esc leaves it, the way
		// dismissing a menu and leaving the buffer are two presses in vim.
		m.closePum()
		m.layout()
		return m, nil, true
	}
	return m, nil, false
}

// acceptPum writes the highlighted offer in the run's place. The run is erased
// by backspacing over it — the typing run in reverse — so the draft is left
// exactly as if the reader had written the whole thing out.
func (m Model) acceptPum() Model {
	hit := m.pum.hits[m.pum.idx]
	for range m.pum.run.runes {
		m.input, _ = m.input.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	// A trailing space, because what is named is followed by what is being said
	// about it and the client leaves one too.
	m.input.InsertString(hit.insert + " ")
	if hit.id != "" && hit.id != allKey {
		// Which person a name stood for, so two colleagues sharing a display
		// name resolve to the one chosen. resolveMentions reads it on the way
		// out; the draft itself stays ordinary text.
		if m.picked == nil {
			m.picked = map[string]string{}
		}
		m.picked[hit.name] = hit.id
	}
	if hit.emoji.Key != "" {
		m.emojiWrite.Use(hit.emoji.Key)
		if err := m.emojiWrite.SaveUsed(m.deps.DataDir); err != nil {
			// The list is derived data; losing it costs the ordering of an
			// empty query, which is not worth interrupting the draft for.
			m = m.notify("could not remember "+hit.insert+": "+err.Error(), false)
		}
	}
	m.pum = pum{}
	m.replan()
	m.layout()
	return m
}

// pumRows is how many offers the popup has room for.
func (m Model) pumRows() int { return m.composerRows().pum }

// pumVisible is the offers the popup has room for. The renderer draws exactly
// these and the picture pass claims exactly their pictures, so the two cannot
// drift into preparing one emoji and drawing another.
func (m Model) pumVisible() []pumHit { return window(m.pum.hits, m.pum.top, m.pumRows()) }

// pumLines draws the popup, top row first, to sit directly over the writing
// area — which at the bottom of the screen is where a popup menu opens.
func (m Model) pumLines(w int) []string {
	var out []string
	for i, h := range m.pumVisible() {
		out = append(out, m.pumLine(h, m.pum.top+i == m.pum.idx, w))
	}
	return out
}

// pumLine draws one offer: the mark, the emoji where there is one, and the
// words emojiHits already assembled. It comes back as a string rather than
// pieces because only an emoji carries a picture, and that one case is joined
// here.
func (m Model) pumLine(h pumHit, selected bool, w int) string {
	mark := "  "
	if selected {
		mark = stAccent.Render("▸ ")
	}
	if h.emoji.Key == "" {
		return fit(mark+h.label, w)
	}
	icon, pic := m.pickerIcon(h.emoji)
	tail := " " + h.label
	if pic.cols > 0 {
		// A line carrying a picture is padded rather than fitted: fit measures
		// a placeholder as the characters it is and would cut one out of its
		// cluster.
		return m.joinSegs([]rowSeg{{text: mark}, {pic: pic}, {text: icon + tail}}, w)
	}
	return fit(mark+icon+tail, w)
}

// pumHint names the keys the popup owns while it is open, since it takes two
// the composer otherwise has.
const pumHint = "Tab/Enter accept · ^n/^p move · Esc dismiss"

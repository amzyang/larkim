package tui

import (
	"cmp"
	"slices"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/textarea"
	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/fuzzy"
	"github.com/amzyang/larkim/larkmd"
	"github.com/amzyang/larkim/store"
)

// pumKind is what a completion run completes.
type pumKind int

const (
	pumMention pumKind = iota // `@` — the people in this chat
	pumEmoji                  // `:` or `[` — an emoji, Feishu's own or a Unicode one
	pumSnippet                // `/` — the assistant panel's snippet offers
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
	// mark is the runes of name the query landed on.
	mark []int
	// person is who a mention names; zero for @All and for an emoji.
	person store.Contact
	// face is a mention's avatar: the person's, or the chat's for @All.
	face offerIcon
	// emoji is set only for an emoji offer.
	emoji emoji.Hit
	// text is a snippet's question, shown beside the list so the name does
	// not have to carry what the snippet asks.
	text string
}

// pum is the completion popup standing over the writing area. It is not a mode:
// the trigger and the query stay in the draft and the reader goes on typing
// into the composer, which is the whole difference between this and a chooser
// that takes the box. A zero value is closed.
type pum struct {
	run  pumRun
	menu menu[pumHit]
	// dismissed is the line Esc closed the popup on. Typing further along that
	// same line leaves it closed: a reader who dismissed the popup meant the
	// colon literally, and one that came back on the next keystroke would be
	// one they cannot get rid of.
	dismissed string
}

func (p pum) open() bool { return p.menu.open() }

// showing reports whether the popup has room to be drawn. A popup nobody can
// see must not be taking Enter: on a terminal too short to spare it a row, the
// composer keeps every key it has.
func (m Model) pumShowing() bool { return m.pumRows() > 0 }

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
// A run opens only where larkmd.AtBoundary holds, the rule mentions already
// resolve by. That one rule is what keeps `http://`, `12:30`, `note:` and an email
// address from opening a popup over what the reader is actually writing, and
// pumEmojiQueryMin keeps the rest: a lone `:` or `[` is punctuation until two
// runes of a name stand behind it.
func pumRunAt(line string) (pumRun, bool) {
	rs := []rune(line)
	for i, r := range slices.Backward(rs) {
		if len(rs)-1-i > pumQueryMax {
			return pumRun{}, false
		}
		kind, trigger := pumKindOf(r)
		if !trigger {
			if unicode.IsSpace(r) {
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
		if !larkmd.AtBoundary(head, len(head)) {
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
// that has to know what a trigger looks like. The assistant's box is a
// question, not a message: nothing in it completes the way a draft does, and
// its snippet popup — a / at the very start of the question — is its own.
func (m *Model) takePum() {
	// The side names the run's shape and where its dismissal is anchored: the
	// assistant's / stands at the very start of the whole question, the
	// composer's triggers wherever the cursor is.
	var run pumRun
	var ok bool
	var hits []pumHit
	var dismissedAt string
	if m.side == sideAI {
		dismissedAt = m.aiP.input.Value()
		run, ok = snippetRunAt(m.aiP.input)
		if ok {
			hits = m.snippetHits(run.query)
		}
	} else {
		dismissedAt = lineBeforeCursor(m.area())
		run, ok = pumRunAt(dismissedAt)
		if ok {
			hits = m.pumHits(run)
		}
	}
	if m.pum.dismissed != "" && strings.HasPrefix(dismissedAt, m.pum.dismissed) {
		m.pum = pum{dismissed: m.pum.dismissed}
		return
	}
	if !ok {
		m.pum = pum{}
		return
	}
	// An unchanged run keeps its cursor: a key that moved nothing in the draft
	// should not throw the reader back to the first offer.
	if m.pum.open() && run == m.pum.run {
		return
	}
	m.pum = pum{run: run, menu: fillMenu(hits, pumSpec)}
}

// snippetRunAt is the assistant box's completion run: a / standing at the very
// start of the question, with the query everything typed past it. A / anywhere
// else — a path, a date — opens nothing.
func snippetRunAt(ta textarea.Model) (pumRun, bool) {
	if ta.Line() != 0 {
		return pumRun{}, false
	}
	line := lineBeforeCursor(ta)
	query, ok := strings.CutPrefix(line, "/")
	if !ok {
		return pumRun{}, false
	}
	return pumRun{kind: pumSnippet, runes: len([]rune(line)), query: query}, true
}

// larkMark says an offer is one of Lark's own emoji, which reaches the other
// side as the picture this client draws, where a Unicode row is a character.
const larkMark = "🐦 Lark"

// pumSpec draws an offer: an emoji as its picture and name, a person by name,
// with what tells two of them apart in the box beside the list.
var pumSpec = menuSpec[pumHit]{
	row: func(h pumHit) offer {
		if h.emoji.Emoji.Key != "" {
			return offer{icon: emojiIcon(h.emoji.Emoji), name: stBold.Render(h.emoji.Emoji.Name())}
		}
		name := markName(h.name, h.mark, stBold)
		if h.person.IsBot {
			name += botBadge
		}
		return offer{icon: h.face, name: name}
	},
	info: func(h pumHit) []string {
		if h.emoji.Emoji.Key != "" {
			info := emojiInfo(h.emoji)
			// The client's composer offers only its own emoji, so it has no
			// mark for ours-vs-plain-character; the distinction exists only
			// where the two are listed together, which is here.
			if h.emoji.Emoji.Glyph == "" {
				info = append(info, larkMark)
			}
			return info
		}
		if h.text != "" {
			return infoLines(h.text)
		}
		return infoLines(h.person.Department, h.person.Email)
	},
}

// closePum dismisses the popup for the rest of the run the reader is on.
func (m *Model) closePum() {
	m.pum = pum{dismissed: lineBeforeCursor(m.area())}
}

// pumHits is what a run offers, best first.
//
// The query is folded before it is matched: the matcher reads a capital as
// smart case, and the emoji terms and the pinyin of a name are all lowercase,
// so `[Do` would answer nothing. A capital typed mid-sentence is the shift
// still held from the bracket, not a request to match that case.
func (m Model) pumHits(run pumRun) []pumHit {
	query := strings.ToLower(run.query)
	if run.kind == pumMention {
		return m.mentionHits(query)
	}
	return m.emojiHits(query)
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
	keep := func(id, name string, person store.Contact, face offerIcon) {
		if mark, hit := ix.Match(id, name, query); hit {
			out = append(out, pumHit{insert: "@" + name, id: id, name: name, mark: mark, person: person, face: face})
		}
	}
	if c, ok := m.currentChat(); ok && c.ChatMode != "p2p" {
		// @All reaches the whole group, so it wears the group's face.
		keep(allKey, strings.TrimPrefix(allName, "@"), store.Contact{}, faceIcon(c.AvatarSeed(), c.Name, c.AvatarFile()))
	}
	for _, c := range m.roster {
		if c.Name == "" {
			continue
		}
		keep(c.OpenID, c.Name, c, faceIcon(c.OpenID, c.Name, c.AvatarFile()))
	}
	return out
}

// emojiHits narrows the emoji a message can carry.
func (m Model) emojiHits(query string) []pumHit {
	var out []pumHit
	for _, h := range m.emojiWrite.Search(query) {
		out = append(out, pumHit{insert: emojiInsert(h.Emoji), emoji: h})
	}
	return out
}

// snippetHits narrows the panel's snippet offers with the matcher the chat
// list narrows by — a substring or initialism of the name, its pinyin
// included.
func (m Model) snippetHits(query string) []pumHit {
	q := strings.ToLower(query)
	ix := fuzzy.NewIndex()
	var out []pumHit
	for _, sn := range m.snippets() {
		if mark, hit := ix.Match(sn.Name, sn.Name, q); hit {
			out = append(out, pumHit{insert: sn.Text, name: sn.Name, mark: mark, text: sn.Text})
		}
	}
	return out
}

// emojiInsert is what accepting an emoji writes: the bracketed name for one
// of Feishu's own, and the character for a Unicode row.
//
// The name is the English one this client displays, which is the spelling it
// puts on the wire. A bracketed name is resolved against the reading client's
// own table, which holds both languages' names for every emoji, so it draws
// there whichever language that client is set to — as the picture, the way
// this client drew it in the menu.
func emojiInsert(e emoji.Emoji) string { return cmp.Or(e.Glyph, "["+e.Name()+"]") }

// onPumKey drives the popup. It is reached only while one is open, ahead of the
// writing area, so every key here is one the composer would otherwise have.
func (m Model) onPumKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd, bool) {
	switch k.String() {
	case "enter", "ctrl+y":
		return m.acceptPum(), nil, true
	case "tab", "down", "ctrl+n":
		m.pum.menu.move(1, m.pumRows())
		return m, nil, true
	case "shift+tab", "up", "ctrl+p":
		m.pum.menu.move(-1, m.pumRows())
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
	hit, _ := m.pum.menu.focused()
	for range m.pum.run.runes {
		ta := m.areap()
		*ta, _ = ta.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	}
	// A trailing space, because what is named is followed by what is being said
	// about it and the client leaves one too.
	m.areap().InsertString(hit.insert + " ")
	if hit.id != "" && hit.id != allKey {
		// Which person a name stood for, so two colleagues sharing a display
		// name resolve to the one chosen. resolveMentions reads it on the way
		// out; the draft itself stays ordinary text.
		if m.picked == nil {
			m.picked = map[string]string{}
		}
		m.picked[hit.name] = hit.id
	}
	if key := hit.emoji.Emoji.Key; key != "" {
		m.emojiWrite.Use(key)
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

// pumRows is how many offers the popup has room for. The popup is not cleared
// on the way out of insert mode, so the mode is what keeps a run left over from
// it off the pane.
func (m Model) pumRows() int {
	if m.mode != modeInsert {
		return 0
	}
	return m.floatRoom(len(m.pum.menu.items), m.pum.menu.maxRows())
}

// pumHint names the keys the popup owns while it is open, since it takes two
// the composer otherwise has.
const pumHint = "Tab/Enter accept · ^n/^p move · Esc dismiss"

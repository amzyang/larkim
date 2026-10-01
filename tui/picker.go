package tui

import (
	"cmp"
	"os"
	"strconv"
	"strings"
	"time"
	"uuid"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/jev"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
)

// picker is the emoji chooser open over the composer. A zero value is closed.
type picker struct {
	target store.Message
	// input is the filter. It is the same text input the command line is built
	// on, so the query is edited under the readline keys a reader already has
	// in their fingers rather than a hand-rolled subset of them.
	input textinput.Model
	// menu is the offers, drawn as the one list every completion is: over the
	// panes, one offer a row, with the focused emoji's key and matched term in
	// the box beside the list.
	menu menu[reactHit]
	// mine is the emoji the reader has already put on the target, so choosing
	// one of them takes it back instead of adding it twice.
	mine map[string]bool
	// suggest is what the head of the list is drawing, and keys
	// the emoji an answer named, best first. See suggest.go.
	suggest suggestState
	// picks is what the answer chose, best first.
	picks []jev.Option
	// found is how many emoji the query itself answered with, which laying the
	// list neither adds to nor takes from.
	found int
	// moved says the reader has walked the cursor off the offer the chooser
	// opened on, which is what decides where the cursor stands once an answer
	// rearranges the head of the list.
	moved bool
}

// reactHit is one emoji the chooser offers, with what its row says of it baked
// in when the list is filled: the menu's projections read the item alone.
type reactHit struct {
	hit emoji.Hit
	// mine says choosing it takes the reaction back rather than adding it.
	mine bool
	// byAsk says the conversation's answer chose it, rather than the row being
	// one filled from the reader's own order to finish the head of the list.
	byAsk bool
}

// reactSpec draws an offer the way the composer's own emoji completions do:
// the emoji as the icon, its name beside it, and the key with the term a query
// landed on said in the box beside the list.
var reactSpec = menuSpec[reactHit]{
	row: func(h reactHit) offer {
		name := stBold.Render(h.hit.Emoji.Name())
		switch {
		case h.mine:
			// Already the reader's: choosing it takes it back, and the row has
			// to say so before they press enter.
			name += stAccent.Render(" ✓")
		case h.byAsk:
			// The conversation chose this one, and the rows below it were only
			// filled out of the reader's own order, so without the mark the two
			// kinds of row read alike.
			name += stAccent.Render(" ✦")
		case !h.hit.Emoji.Reactable():
			// Feishu will not take this one as a reaction, so choosing it sends
			// a picture instead. That is a message in the chat rather than a
			// mark on one, which the reader has to know before they press enter.
			name += stDim.Render(" pic")
		}
		return offer{icon: emojiIcon(h.hit.Emoji), name: name}
	},
	info: func(h reactHit) []string { return emojiInfo(h.hit) },
	// A reaction is one press, so a digit reaches the row it names: most
	// reactions are the first row or two, which is where a hand rests. The
	// digit is query text first, because the emoji spelled in digits — 666,
	// 100, +1 — are typed exactly the way they read.
	digits: digitQuery,
}

// openPicker arms the chooser against the selected message.
func (m Model) openPicker() (tea.Model, tea.Cmd) {
	if m.onForwardedChild() {
		return m.notify("a forwarded message belongs to its own chat", true), nil
	}
	x, ok := m.selected()
	if !ok {
		return m.notify("select a message to react to", true), nil
	}
	if x.Deleted {
		return m.notify("that message was recalled", true), nil
	}
	// A send still on its way carries a local id Feishu has never seen. Its
	// body may not be rendered yet either, but that is no reason to refuse:
	// the message exists in Feishu and a reaction reaches it by id alone.
	if m.outboxAt(x.MessageID) != nil {
		return m.notify("that message has not reached Feishu yet", true), nil
	}
	// The strip the reader is looking at, presses and all: pressing e straight
	// after clicking a chip has to mark what that click put there.
	mine := map[string]bool{}
	for _, c := range m.drawnChips(x) {
		if c.Mine {
			mine[emoji.Fold(c.Key)] = true
		}
	}
	in := m.newQueryInput()
	m.mode = modeEmoji
	m.picker = picker{target: x, mine: mine, input: in}
	ask := m.armSuggest(x)
	m.pickerGrid()
	m.layout()
	focus := m.picker.input.Focus()
	return m, tea.Batch(focus, ask)
}

// reactRows is how many offers the chooser's list has room for. It stands over
// the panes the way every completion list does, so it costs the composer's box
// nothing.
func (m Model) reactRows() int {
	if m.mode != modeEmoji {
		return 0
	}
	return m.floatRoom(len(m.picker.menu.items), m.picker.menu.maxRows())
}

// onEmojiKey drives the chooser. The filter owns every key it can edit with,
// so movement through the offers is on the arrows and the readline pair —
// with the tab pair, the key every other list here walks by — rather than
// hjkl, and a digit is settled by the rule the menu's spec declares.
func (m Model) onEmojiKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	rows := m.reactRows()
	switch k.String() {
	case "esc":
		m.closePicker()
		return m, nil
	case "enter", "ctrl+y":
		return m.chooseAt(m.picker.menu.idx)
	case "shift+tab", "up", "ctrl+p":
		m.picker.moved = true
		m.picker.menu.move(-1, rows)
		return m, nil
	case "tab", "down", "ctrl+n":
		m.picker.moved = true
		m.picker.menu.move(1, rows)
		return m, nil
	}
	if s := k.String(); m.picker.menu.spec.digits == digitQuery && bareDigit(s) {
		return m.digitKey(s)
	}
	return m.typeIntoFilter(k)
}

// bareDigit says a key is one digit on its own, no modifier riding it.
func bareDigit(s string) bool {
	return len(s) == 1 && s[0] >= '0' && s[0] <= '9'
}

// digitKey takes a digit the digitQuery rule hands it: it goes into the query
// first, and a query that still answers keeps it. One that answers nothing is
// the reader pointing at a numbered row, so the digit comes back off the query
// and picks it against the list as it stood.
func (m Model) digitKey(s string) (tea.Model, tea.Cmd) {
	idx, top := m.picker.menu.idx, m.picker.menu.top
	next, _ := m.typeIntoFilter(tea.KeyPressMsg{Code: rune(s[0]), Text: s})
	m = next.(Model)
	if len(m.picker.menu.items) > 0 {
		return m, nil
	}
	m.picker.input, _ = m.picker.input.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m.pickerGrid()
	m.picker.menu.idx, m.picker.menu.top = idx, top
	if i, ok := m.picker.menu.pick(s, m.reactRows()); ok {
		return m.chooseAt(i)
	}
	return m, nil
}

// typeIntoFilter hands a message to the filter and re-runs the search when the
// query came back changed, which is the only thing the chooser reads from it.
func (m Model) typeIntoFilter(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m.picker.input.Value()
	var cmd tea.Cmd
	m.picker.input, cmd = m.picker.input.Update(msg)
	if m.picker.input.Value() != before {
		// The cursor goes back to the best hit, which is where a reader who
		// just typed is looking — and which is also what spares the row
		// appearing and disappearing from having to move the cursor with it.
		m.pickerGrid()
		m.picker.menu.idx, m.picker.menu.top = 0, 0
	}
	return m, cmd
}

// chooseAt puts the offered emoji on the message, or takes it back when the
// reader already chose it. i is the cursor's row, or the one a digit names.
func (m Model) chooseAt(i int) (tea.Model, tea.Cmd) {
	if i < 0 || i >= len(m.picker.menu.items) {
		return m, nil
	}
	target := m.picker.target
	key := m.picker.menu.items[i].hit.Emoji.Key
	m.closePicker()
	return m.toggleReaction(target, key)
}

// closePicker shuts the chooser and retires the contextual answer on its way,
// so one that lands late cannot fill the head of the chooser opened after it.
func (m *Model) closePicker() {
	m.mode = modeNormal
	m.picker = picker{}
	m.suggestGen++
	m.layout()
}

// toggleReaction puts the emoji on the message, or takes it back when it is
// already the reader's. Every way of reacting — the chooser, :react, a press
// on the chip itself — arrives here.
//
// The press is drawn before it is sent, and taken back off the strip only if
// Feishu refuses it. A chip that moved only once the round trip came back
// reads as a press that did not land, and the reader presses again.
func (m Model) toggleReaction(x store.Message, key string) (tea.Model, tea.Cmd) {
	if m.onForwardedChild() {
		return m.notify("a forwarded message belongs to its own chat", true), nil
	}
	if x.Deleted {
		return m.notify("select a message to react to", true), nil
	}
	if m.outboxAt(x.MessageID) != nil {
		return m.notify("that message has not reached Feishu yet", true), nil
	}
	// An emoji Feishu refuses as a reaction goes on the conversation as the
	// picture the client draws it with — the only way it reaches the other
	// side at all. Taking one back is still a reaction: it is already on the
	// message, put there before the client withdrew it.
	e, known := emoji.ByKey(key)
	asPicture := known && !e.Reactable() && !mineOn(m.drawnChips(x), key)
	// A chip carries whatever key Feishu sent, which may be one this build has
	// no entry for; remembering that would head an empty query with a name the
	// chooser cannot draw.
	if known {
		m.emoji.Use(key)
		if err := m.emoji.SaveUsed(m.deps.DataDir); err != nil {
			// The list is derived data; losing it costs the ordering of an
			// empty query, which is not worth interrupting the reaction for.
			m = m.notify("could not remember "+e.Name()+": "+err.Error(), false)
		}
	}
	if asPicture {
		return m.sendEmojiPicture(x, e)
	}
	p := m.pressReaction(x, key)
	// layout rather than a bare rebuild: a press can open or close a strip,
	// and the viewport is held by the message on its top row across the row
	// the strip takes or gives back.
	m.layout()
	return m, react(m.deps, p)
}

// sendEmojiPicture answers a choice of an emoji Feishu will not take as a
// reaction: it replies to the message with the picture the client draws that
// emoji as, which is what the client itself leaves a reader — the emoji is
// gone from the reaction panel but still goes inside a message.
//
// It goes through the outbox like any other send, so the bubble stands under
// the message it answers while it is on its way and says so if it fails.
func (m Model) sendEmojiPicture(x store.Message, e emoji.Emoji) (tea.Model, tea.Cmd) {
	path := emoji.Picture(m.deps.DataDir, e.Key)
	if _, err := os.Stat(path); err != nil {
		m.deps.Log.Error("emoji picture", "key", e.Key, "path", path, "err", err)
		return m.notify(e.Name()+" has no picture cut out to send", true), nil
	}
	img := draftImage{ref: path, local: path, key: "img_local_1"}
	it := outboxItem{localID: uuid.New().String(), chatID: x.ChatID, replyTo: x.MessageID,
		msgType: "image", send: larkcli.Image(img.key), body: "[Image: " + img.key + "]",
		images: []draftImage{img}, createMs: time.Now().UnixMilli()}
	// A reply to a message inside a thread stays inside it, the way the
	// composer's own reply does. A thread reply carries no position of its
	// own, which is what tells it from a message in the chat.
	if x.ThreadID != "" && x.MessagePosition < 0 {
		it.inThread, it.threadID = true, x.ThreadID
	}
	cmd := m.sendItem(it)
	m.enqueue(it)
	m.refreshPanes()
	return m.notify("sending "+e.Name()+" as a picture", false), cmd
}

// reactedMsg says how a press ended. The strip is drawn ahead of Feishu's
// answer, so one that failed has to be taken back off it by name.
type reactedMsg struct {
	p   reactPending
	err error
}

// react puts the emoji on the message and brings its summary back. The write
// bumps the data revision, so the panes reload on their own.
func react(d Deps, p reactPending) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := waited(reactTimeout)
		defer cancel()
		return reactedMsg{p: p, err: d.Syncer.React(ctx, p.messageID, p.emojiType, p.on)}
	}
}

// pickerPrompt labels the query box the chooser opens with. The cursor is
// placed past it, so its width cannot be measured in two places.
func pickerPrompt() string { return stBold.Render("react") + stAccent.Render(" › ") }

// reactHint names the keys the chooser owns while it is open, on the row the
// badge has in every mode.
const reactHint = "Enter react · 1-9 pick · Esc cancel"

// renderReactHint draws that row: where in the list the cursor stands, and the
// keys that move it.
func (m Model) renderReactHint(w int) string {
	where := strconv.Itoa(len(m.picker.menu.items))
	if m.picker.menu.idx >= 0 {
		where = strconv.Itoa(m.picker.menu.idx+1) + "/" + where
	}
	return padBetween("", stDim.Render(where+" · "+reactHint), w)
}

// renderPicker draws the chooser's own box: the query its offers are narrowed
// by, padded to the composer's height so nothing above the box moves. The
// offers themselves are the list over the panes, drawn by the one menu every
// completion is.
func (m Model) renderPicker() string {
	w := m.bandWidth(m.side) - 2
	h := m.composerHeight()
	// An unnarrowed query answers with the emoji this reader reaches for, the
	// way the client's own panel opens on its frequently used band, so the
	// line says that rather than counting the whole table against itself.
	label := strconv.Itoa(m.picker.found) + "/" + strconv.Itoa(m.emoji.Len())
	if strings.TrimSpace(m.picker.input.Value()) == "" && m.emoji.UsedLen() > 0 {
		label = "frequently used"
	}
	// The head of the list speaks for itself once it is filled; the note is
	// for the states that need a word, and it takes the line because it is the
	// one thing on it that changed.
	count := stDim.Render(cmp.Or(m.picker.suggestNote(), label))
	rows := []string{padBetween(pickerPrompt()+m.picker.input.View(), count, w)}
	if len(m.picker.menu.items) == 0 {
		rows = append(rows, fit(stDim.Render("  no emoji matches "+m.picker.input.Value()), w))
	}
	for len(rows) < h-1 {
		rows = append(rows, "")
	}
	rows = append(rows, m.renderReactHint(w))
	return paneStyle(true).Height(h).Render(fitBlock(strings.Join(rows, "\n"), w, h))
}

// markMatch underlines the runes of a term the query landed on, so a hit
// reached through pinyin says which pinyin.
func markMatch(term string, pos []int) string {
	at := map[int]bool{}
	for _, p := range pos {
		at[p] = true
	}
	var b strings.Builder
	for i, r := range []rune(term) {
		if at[i] {
			b.WriteString(stUnder.Render(string(r)))
			continue
		}
		b.WriteRune(r)
	}
	return b.String()
}

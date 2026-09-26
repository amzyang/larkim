package tui

import (
	"os"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/textinput"
	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/store"
	"github.com/google/uuid"
)

// pickerCols is how many emoji stand side by side. The picker is only as tall
// as the composer box it stands in, so the hits are laid across the width
// instead of down: three columns still name the key and the word at the
// narrowest terminal the client draws.
const pickerCols = 3

// picker is the emoji chooser open over the composer. A zero value is closed.
type picker struct {
	target store.Message
	// input is the filter. It is the same text input the command line is built
	// on, so the query is edited under the readline keys a reader already has
	// in their fingers rather than a hand-rolled subset of them.
	input textinput.Model
	hits  []emoji.Hit
	idx   int
	// top is the first grid row on screen, not the first hit: the grid scrolls
	// by whole rows, so a hit never changes column under the reader.
	top int
	// mine is the emoji the reader has already put on the target, so choosing
	// one of them takes it back instead of adding it twice.
	mine map[string]bool
}

// openPicker arms the chooser against the selected message.
func (m Model) openPicker() (tea.Model, tea.Cmd) {
	if m.onForwardedChild() {
		return m.notify("a forwarded message belongs to its own chat", true), nil
	}
	x, ok := m.selected()
	if !ok || x.Deleted {
		return m.notify("select a message to react to", true), nil
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
	in := textinput.New()
	in.Prompt = ""
	in.SetStyles(textinput.DefaultStyles(m.dark))
	in.SetVirtualCursor(false)
	m.mode = modeEmoji
	m.picker = picker{target: x, mine: mine, input: in, hits: m.emoji.Search("")}
	m.layout()
	return m, m.picker.input.Focus()
}

// pickerRows is how many rows of emoji the grid has: the composer's box less
// the query line it opens with. The picker takes the box the composer would
// have drawn, so pressing e moves nothing above it.
func (m Model) pickerRows() int { return m.composerHeight() - 1 }

// pickerVisible is the hits the chooser has room for. The renderer draws
// exactly these and the picture pass claims exactly their pictures, so the two
// cannot drift into preparing one emoji and drawing another.
func (m Model) pickerVisible() []emoji.Hit {
	lo := min(m.picker.top*pickerCols, len(m.picker.hits))
	return m.picker.hits[lo:min(len(m.picker.hits), lo+m.pickerRows()*pickerCols)]
}

// minListRows is the floor the composer leaves the message panes when a draft
// grows: enough to still see the message being written about, and its
// neighbours.
const minListRows = 6

// onEmojiKey drives the chooser. The filter owns every key it can edit with,
// so movement through the hits is on the arrows and the readline pair rather
// than hjkl.
func (m Model) onEmojiKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		m.mode = modeNormal
		m.picker = picker{}
		m.layout()
		return m, nil
	case "enter":
		return m.choose()
	case "up", "ctrl+p":
		m.picker.move(-pickerCols, m.pickerRows())
		return m, nil
	case "down", "ctrl+n":
		m.picker.move(pickerCols, m.pickerRows())
		return m, nil
	case "left":
		m.picker.move(-1, m.pickerRows())
		return m, nil
	case "right":
		m.picker.move(1, m.pickerRows())
		return m, nil
	}
	return m.typeIntoFilter(k)
}

// typeIntoFilter hands a message to the filter and re-runs the search when the
// query came back changed, which is the only thing the picker reads from it.
func (m Model) typeIntoFilter(msg tea.Msg) (tea.Model, tea.Cmd) {
	before := m.picker.input.Value()
	var cmd tea.Cmd
	m.picker.input, cmd = m.picker.input.Update(msg)
	if q := m.picker.input.Value(); q != before {
		// The cursor goes back to the best hit, which is where a reader who
		// just typed is looking.
		m.picker.hits = m.emoji.Search(q)
		m.picker.idx, m.picker.top = 0, 0
	}
	return m, cmd
}

// move walks the grid by d hits, the arrows crossing a column and the rows
// keys a whole row, and scrolls by rows to keep the cursor on screen.
func (p *picker) move(d, rows int) {
	if len(p.hits) == 0 {
		return
	}
	p.idx = clamp(p.idx+d, 0, len(p.hits)-1)
	row := p.idx / pickerCols
	p.top = clamp(p.top, max(0, row-rows+1), row)
}

// choose puts the highlighted emoji on the message, or takes it back when the
// reader already chose it.
func (m Model) choose() (tea.Model, tea.Cmd) {
	if len(m.picker.hits) == 0 {
		return m, nil
	}
	target := m.picker.target
	key := m.picker.hits[m.picker.idx].Emoji.Key
	m.mode = modeNormal
	m.picker = picker{}
	m.layout()
	return m.toggleReaction(target, key)
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
	// picker cannot draw.
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
	it := outboxItem{localID: uuid.NewString(), chatID: x.ChatID, replyTo: x.MessageID,
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

// renderPicker draws the chooser in the composer's place, filling exactly the
// box the composer would have drawn: the query it is being narrowed by, then
// the hits laid across the width.
func (m Model) renderPicker() string {
	w := m.width - 2
	rows := m.pickerRows()
	// An unnarrowed query answers with the emoji this reader reaches for, the
	// way the client's own panel opens on its frequently used band, so the
	// line says that rather than counting the whole table against itself.
	count := stDim.Render(strconv.Itoa(len(m.picker.hits)) + "/" + strconv.Itoa(m.emoji.Len()))
	if strings.TrimSpace(m.picker.input.Value()) == "" && m.emoji.UsedLen() > 0 {
		count = stDim.Render("frequently used")
	}
	lines := []string{padBetween(pickerPrompt()+m.picker.input.View(), count, w)}
	if len(m.picker.hits) == 0 {
		lines = append(lines, fit(stDim.Render("  no emoji matches "+m.picker.input.Value()), w))
	}
	vis := m.pickerVisible()
	cell := w / pickerCols
	for i := 0; i < len(vis); i += pickerCols {
		var segs []rowSeg
		for j := i; j < min(i+pickerCols, len(vis)); j++ {
			segs = append(segs, m.pickerCell(vis[j], m.picker.top*pickerCols+j == m.picker.idx, cell)...)
		}
		lines = append(lines, m.joinSegs(segs, w))
	}
	for len(lines) < rows+1 {
		lines = append(lines, fit("", w))
	}
	return paneStyle(true, w).Render(strings.Join(lines[:rows+1], "\n"))
}

// pickerIconCols is the column every emoji is drawn in, character or picture
// alike. A fixed width is what keeps the key and name columns from stepping a
// cell sideways under a single-width character.
const pickerIconCols = 2

// pickerIcon draws an emoji the way the message strip will once it is chosen:
// the Unicode character where one carries the same feeling, the client's own
// picture where none does, and a bare dot where the pictures were never cut
// out. Exactly one of the two is set.
func (m Model) pickerIcon(e emoji.Emoji) (string, picture) {
	if e.Glyph != "" {
		return fit(e.Glyph, pickerIconCols), picture{}
	}
	if pic := m.chatPics().pic(e.Key, pickerIconCols); pic.cols > 0 {
		return strings.Repeat(" ", pickerIconCols-pic.cols), pic
	}
	return fit(stDim.Render("·"), pickerIconCols), picture{}
}

// pickerCell draws one emoji in its column of the grid: the emoji itself, then
// the words emojiWords leaves of the key Feishu speaks, the name, and the term
// the query landed on. It comes back in pieces because the emoji may be a
// picture, which only the renderer can place.
func (m Model) pickerCell(h emoji.Hit, selected bool, cell int) []rowSeg {
	mark := " "
	if selected {
		mark = stAccent.Render("▸")
	}
	name := h.Emoji.Name()
	drawn := name
	if selected {
		// The mark alone is one glyph at the far left of a three-column grid,
		// which is not where the reader is reading. The name takes the colour
		// because it is the only run in the cell drawn plain: the key and the
		// term that answered are dim, and the tick and pic say something else.
		drawn = stPickerOn.Render(name)
	}
	switch {
	case m.picker.mine[emoji.Fold(h.Emoji.Key)]:
		// Already the reader's: choosing it takes it back, and the cell has to
		// say so before they press enter.
		drawn += stAccent.Render(" ✓")
	case !h.Emoji.Reactable():
		// Feishu will not take this one as a reaction, so choosing it sends a
		// picture instead. That is a message in the chat rather than a mark on
		// one, which the reader has to know before they press enter.
		drawn += stDim.Render(" pic")
	}
	// The words are fitted to what is left of the cell, so the column the next
	// emoji opens in stands still whatever shape this one has.
	room := max(0, cell-pickerIconCols-2)
	tail := " " + fit(truncate(emojiWords(h.Emoji.Key, name, drawn, h.Term, h.Positions), room), room)
	icon, pic := m.pickerIcon(h.Emoji)
	if pic.cols > 0 {
		return []rowSeg{{text: mark}, {pic: pic}, {text: icon + tail}}
	}
	return []rowSeg{{text: mark + icon + tail}}
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

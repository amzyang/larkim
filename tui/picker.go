package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/store"
)

const (
	// pickerRows is how many emoji the picker offers at a comfortable height,
	// and pickerMinRows the fewest that still make it worth opening: below
	// that the reader is typing blind.
	pickerRows    = 6
	pickerMinRows = 3
	// pickerChrome is the rows the picker spends on itself: its border, the
	// query line and the key hints.
	pickerChrome = 5
)

// picker is the emoji chooser open over the composer. A zero value is closed.
type picker struct {
	target store.Message
	query  string
	hits   []emoji.Hit
	idx    int
	top    int
	// mine is the emoji the reader has already put on the target, so choosing
	// one of them takes it back instead of adding it twice.
	mine map[string]bool
}

// openPicker arms the chooser against the selected message.
func (m Model) openPicker() (tea.Model, tea.Cmd) {
	if m.deps.Syncer == nil {
		return m.notify("reacting needs the sync lock; the daemon holds it", true), nil
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
	if m.pickerRows() < pickerMinRows {
		return m.notify("terminal too short for the emoji picker", true), nil
	}
	mine := map[string]bool{}
	for _, c := range emoji.Summary(x.ReactionsJSON, m.deps.Self) {
		if c.Mine {
			mine[emoji.Fold(c.Key)] = true
		}
	}
	m.mode = modeEmoji
	m.picker = picker{target: x, mine: mine, hits: m.emoji.Search("")}
	m.layout()
	return m, nil
}

// pickerRows is how many emoji fit under the panes at this height. It floors at
// zero because a resize can shrink the terminal under an open picker: View
// refuses to draw below minHeight, but picturePrepare still runs on every
// message and would slice the hits on a negative count.
func (m Model) pickerRows() int {
	return max(0, min(pickerRows, m.height-statusHeight-pickerChrome-minListRows))
}

// pickerVisible is the hits the chooser has room for. The renderer draws
// exactly these and the picture pass claims exactly their pictures, so the two
// cannot drift into preparing one emoji and drawing another.
func (m Model) pickerVisible() []emoji.Hit {
	return m.picker.hits[m.picker.top:min(len(m.picker.hits), m.picker.top+m.pickerRows())]
}

// minListRows is what the message panes keep when the picker is open: enough
// to still see the message being reacted to, and its neighbours.
const minListRows = 6

// onEmojiKey drives the chooser. The query owns every printable key, so
// movement is on the arrows and the readline pair rather than hjkl.
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
		m.picker.move(-1, m.pickerRows())
		return m, nil
	case "down", "ctrl+n":
		m.picker.move(1, m.pickerRows())
		return m, nil
	case "backspace":
		if q := []rune(m.picker.query); len(q) > 0 {
			m.picker.retype(string(q[:len(q)-1]), m.emoji)
		}
		return m, nil
	}
	if s := k.String(); len(s) > 0 && len([]rune(s)) == 1 {
		m.picker.retype(m.picker.query+s, m.emoji)
	}
	return m, nil
}

// retype re-runs the search and pulls the cursor back to the best hit, which
// is where a reader who just typed is looking.
func (p *picker) retype(query string, ix *emoji.Index) {
	p.query, p.hits = query, ix.Search(query)
	p.idx, p.top = 0, 0
}

func (p *picker) move(d, rows int) {
	if len(p.hits) == 0 {
		return
	}
	p.idx = clamp(p.idx+d, 0, len(p.hits)-1)
	p.top = clamp(p.top, max(0, p.idx-rows+1), p.idx)
}

// choose puts the highlighted emoji on the message, or takes it back when the
// reader already chose it. The send is optimistic in neither direction: the
// picker closes, and the strip changes when Feishu has answered.
func (m Model) choose() (tea.Model, tea.Cmd) {
	if len(m.picker.hits) == 0 {
		return m, nil
	}
	e := m.picker.hits[m.picker.idx].Emoji
	on := !m.picker.mine[emoji.Fold(e.Key)]
	target := m.picker.target
	m.emoji.Use(e.Key)
	if err := m.emoji.SaveRecent(m.deps.DataDir); err != nil {
		// The list is derived data; losing it costs the ordering of an empty
		// query, which is not worth interrupting the reaction for.
		m = m.notify("could not remember "+e.ZH+": "+err.Error(), false)
	}
	m.mode = modeNormal
	m.picker = picker{}
	m.layout()
	return m, react(m.deps, target.MessageID, e.Key, on)
}

// react puts the emoji on the message and brings its summary back. The write
// bumps the data revision, so the panes reload on their own.
func react(d Deps, messageID, key string, on bool) tea.Cmd {
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		if err := d.Syncer.React(ctx, messageID, key, on); err != nil {
			return errMsg{err}
		}
		return nil
	}
}

// renderPicker draws the chooser in the composer's place, over exactly the
// box the composer would have drawn: the message being reacted to on the
// frame, the query and hit count, then the emoji.
func (m Model) renderPicker() string {
	w := m.width - 2
	rows := m.pickerRows()
	head := stBold.Render("react") + stDim.Render(" · "+truncate(flatten(pickerTarget(m.picker.target, m.deps.Self)), max(4, w-10)))
	count := stDim.Render(strconv.Itoa(len(m.picker.hits)) + "/" + strconv.Itoa(m.emoji.Len()))
	query := padBetween(stAccent.Render("› ")+m.picker.query+stAccent.Render("▏"), count, w)

	lines := []string{fit(head, w), query}
	for i, h := range m.pickerVisible() {
		lines = append(lines, m.joinSegs(m.pickerLine(h, m.picker.top+i == m.picker.idx, w), w))
	}
	if len(m.picker.hits) == 0 {
		lines = append(lines, fit(stDim.Render("  no emoji matches "+m.picker.query), w))
	}
	for len(lines) < rows+2 {
		lines = append(lines, fit("", w))
	}
	lines = append(lines, fit(stDim.Render("↑↓ move · enter react · esc cancel"), w))
	return paneStyle(true, w).Render(strings.Join(lines, "\n"))
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

// pickerLine draws one emoji: the emoji itself, the key Feishu speaks, and the
// name the query matched with the matched runes marked. It comes back in
// pieces because the emoji may be a picture, which only the renderer can place.
func (m Model) pickerLine(h emoji.Hit, selected bool, w int) []rowSeg {
	mark := "  "
	if selected {
		mark = stAccent.Render("▸ ")
	}
	name := h.Emoji.ZH
	if m.picker.mine[emoji.Fold(h.Emoji.Key)] {
		// Already the reader's: choosing it takes it back, and the line has to
		// say so before they press enter.
		name += stAccent.Render(" ✓")
	}
	if h.Term != "" && h.Term != h.Emoji.ZH {
		name += stDim.Render(" " + markMatch(h.Term, h.Positions))
	}
	keyCols := min(24, w/3)
	tail := " " + fit(stDim.Render(h.Emoji.Key), keyCols) + " " +
		truncate(name, max(0, w-lipgloss.Width(mark)-pickerIconCols-keyCols-2))
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

// pickerTarget names the message being reacted to, so the reader can see they
// aimed at the right one.
func pickerTarget(x store.Message, self string) string {
	who := x.SenderName
	if x.SenderID == self {
		who = "你"
	}
	if who == "" {
		who = x.SenderID
	}
	return who + ": " + x.Content
}

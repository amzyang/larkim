package tui

import (
	"context"
	"strconv"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
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

// pickerRows is how many emoji fit under the panes at this height.
func (m Model) pickerRows() int {
	return min(pickerRows, m.height-statusHeight-pickerChrome-minListRows)
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

// renderPicker draws the chooser in the composer's place: the message being
// reacted to on the frame, the query and hit count, then the emoji.
func (m Model) renderPicker() string {
	w := m.width - 4
	rows := m.pickerRows()
	head := stBold.Render("react") + stDim.Render(" · "+truncate(flatten(pickerTarget(m.picker.target, m.deps.Self)), max(4, w-10)))
	count := stDim.Render(strconv.Itoa(len(m.picker.hits)) + "/" + strconv.Itoa(m.emoji.Len()))
	query := padBetween(stAccent.Render("› ")+m.picker.query+stAccent.Render("▏"), count, w)

	lines := []string{head, query}
	for i := m.picker.top; i < len(m.picker.hits) && len(lines) < rows+2; i++ {
		lines = append(lines, fit(m.pickerLine(m.picker.hits[i], i == m.picker.idx, w), w))
	}
	if len(m.picker.hits) == 0 {
		lines = append(lines, fit(stDim.Render("  no emoji matches "+m.picker.query), w))
	}
	for len(lines) < rows+2 {
		lines = append(lines, fit("", w))
	}
	lines = append(lines, stDim.Render("↑↓ move · enter react · esc cancel"))
	return paneStyle(true, w).Render(strings.Join(lines, "\n"))
}

// pickerLine draws one emoji: the emoji itself, the key Feishu speaks, and the
// name the query matched with the matched runes marked.
func (m Model) pickerLine(h emoji.Hit, selected bool, w int) string {
	mark := "  "
	if selected {
		mark = stAccent.Render("▸ ")
	}
	glyph := h.Emoji.Glyph
	if glyph == "" {
		glyph = "·"
	}
	name := h.Emoji.ZH
	if m.picker.mine[emoji.Fold(h.Emoji.Key)] {
		// Already the reader's: choosing it takes it back, and the line has to
		// say so before they press enter.
		name += stAccent.Render(" ✓")
	}
	line := mark + glyph + " " + fit(stDim.Render(h.Emoji.Key), min(24, w/3)) + " " + name
	if h.Term != "" && h.Term != h.Emoji.ZH {
		line += stDim.Render(" " + markMatch(h.Term, h.Positions))
	}
	return line
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

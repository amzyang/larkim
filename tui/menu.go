package tui

import (
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
)

// A menu is a list of offers the reader walks with the keys: the composer's
// completion popup, the : line's, the drafts picker. The host owns the items
// and what accepting one does; the menu owns the cursor and the shape of a
// row, which is neovim's popup menu's: each column as wide as its widest
// entry, a column no entry has anything for left out, and whatever more there
// is to say about the focused entry said in a box beside the list rather than
// on every row — completeopt=popup, blink.cmp's documentation window.
//
// The Lark client's @ list draws a person's department on the row itself;
// here it is the focused row's box instead, the same facts in fewer columns.
// The : line and the drafts picker have no client counterpart.

// offer is what a row draws.
type offer struct {
	icon offerIcon
	// name is styled, with the runes the query landed on already marked.
	name string
}

// offerIcon is what the icon column draws: text — a Unicode emoji, a Nerd
// Font glyph, an ASCII mark — or a picture, with the text standing in where
// the terminal draws none. The zero value draws nothing.
type offerIcon struct {
	text string
	// image is a picture file, relative to the data dir or absolute.
	image string
	// avatar draws image as the circle the client draws a face in, and draws
	// one from id and name where there is no image to cut it from.
	avatar   bool
	id, name string
}

// faceIcon is a person or a chat as the icon column draws them: their avatar,
// or the colour block the chat list stands in with.
func faceIcon(id, name, file string) offerIcon {
	return offerIcon{text: avatarBlock(id, name, pickerIconCols), image: file, avatar: true, id: id, name: name}
}

func (i offerIcon) set() bool { return i.text != "" || i.image != "" }

// offerCols is the layout of a menu's rows. It is measured over every item
// rather than the window, so the box holds its width while the list scrolls
// and changes only when the list is filtered again.
type offerCols struct {
	digits bool
	icon   bool // some row has an icon; when none has, the column and its gap go
	name   int  // the widest name, capped at offerNameMax
}

// digitRule is what a bare digit keystroke means to a list that draws the
// number column: the row it names, or the query the host narrows by with the
// row as the fallback — the emoji whose own terms are digits, 666, 100, +1,
// are typed exactly the way they read.
type digitRule uint8

const (
	digitOff   digitRule = iota // no number column; a digit is the host's alone
	digitRow                    // a digit picks the visible row it names
	digitQuery                  // a digit is query text first, the row on a query that answers nothing
)

// offerNameMax bounds the name column. Past it a name is cut rather than
// pushing the box across a pane the reader is still reading.
const offerNameMax = 40

// quickRows is as tall as a menu that picks by digit gets: 1–9, then 0 for the
// tenth row, so every digit drawn reaches a row.
const quickRows = 10

// menuSpec is how a host projects its items. Both callbacks are pure
// functions of the item: anything else they read is captured by value when
// the menu is filled, never through a live Model, which a callback stored in
// the Model would see a stale copy of.
type menuSpec[T any] struct {
	// row is called for every item when the menu is filled, because the
	// columns are measured over all of them.
	row func(T) offer
	// info is called for the focused item only, since only its box is drawn.
	// Nil, or an empty answer, draws no box.
	info func(T) []string
	// noselect leaves nothing focused until the reader walks onto a row: on
	// the : line, which must go on saying what Enter will run.
	noselect bool
	// digits draws the number column and says what a bare digit keystroke
	// means to the list. See digitRule.
	digits digitRule
}

// menu is the list a host draws. A zero value is closed.
type menu[T any] struct {
	spec  menuSpec[T]
	items []T
	rows  []offer
	cols  offerCols
	// idx is -1 while a noselect menu has nothing focused.
	idx int
	top int
}

func fillMenu[T any](items []T, spec menuSpec[T]) menu[T] {
	u := menu[T]{spec: spec, items: items, rows: make([]offer, len(items))}
	u.cols.digits = spec.digits != digitOff
	for i, it := range items {
		o := spec.row(it)
		u.rows[i] = o
		u.cols.icon = u.cols.icon || o.icon.set()
		u.cols.name = max(u.cols.name, min(offerNameMax, lipgloss.Width(o.name)))
	}
	if spec.noselect {
		u.idx = -1
	}
	return u
}

func (u menu[T]) open() bool { return len(u.items) > 0 }

// maxRows is how many rows the menu ever draws.
func (u menu[T]) maxRows() int {
	if u.spec.digits != digitOff {
		return quickRows
	}
	return pumMaxRows
}

// move walks the cursor and scrolls to keep it on screen. A noselect menu
// walks back off its first row onto nothing, which is how the reader gets back
// to what they typed.
func (u *menu[T]) move(d, rows int) {
	if !u.spec.noselect {
		moveCursor(&u.idx, &u.top, d, len(u.items), rows)
		return
	}
	u.idx = clamp(u.idx+d, -1, len(u.items)-1)
	if u.idx >= 0 {
		u.top = clamp(u.top, max(0, u.idx-rows+1), u.idx)
	}
}

// pick reads a digit as the visible row it is drawn beside. It never reaches a
// row scrolled out of sight, and 0 is the tenth row, where it sits on the
// keyboard.
func (u menu[T]) pick(key string, rows int) (int, bool) {
	if u.spec.digits == digitOff || len(key) != 1 || key[0] < '0' || key[0] > '9' {
		return 0, false
	}
	line := (int(key[0]-'0') + 9) % 10
	if line >= rows || u.top+line >= len(u.items) {
		return 0, false
	}
	return u.top + line, true
}

func (u menu[T]) focused() (T, bool) {
	if u.idx < 0 || u.idx >= len(u.items) {
		var zero T
		return zero, false
	}
	return u.items[u.idx], true
}

// view is the window onto the menu the floater draws, the item type erased.
func (u menu[T]) view(rows int) menuView {
	v := menuView{rows: window(u.rows, u.top, rows), cols: u.cols, sel: u.idx - u.top}
	if it, ok := u.focused(); ok && u.spec.info != nil && u.idx-u.top < rows {
		v.info = u.spec.info(it)
	}
	return v
}

// menuView is what a menu shows: the rows in its window, the cursor's place
// among them, and what the focused one has to say.
type menuView struct {
	rows []offer
	cols offerCols
	sel  int
	info []string
}

// infoLines is a focused row's box from the facts a host has for it, the empty
// ones dropped: the first says what the row is and is drawn plain, the rest
// are detail and drawn dim.
func infoLines(facts ...string) []string {
	var out []string
	for _, f := range facts {
		if f == "" {
			continue
		}
		if len(out) > 0 {
			f = stDim.Render(f)
		}
		out = append(out, f)
	}
	return out
}

// pickerIconCols is the column every emoji is drawn in, character or picture
// alike. A fixed width is what keeps the name column from stepping a cell
// sideways under a single-width character.
const pickerIconCols = 2

// offerRow is one row in the pieces it is drawn from: the digit that picks
// it, the icon, the name. The cursor is not a column of its own — the client
// marks the row it is on with a tint, so the selected row's text wears the
// client's selection colour here and the floater paints the background the
// rest of the row width, under pictures and padding alike. It comes back in
// pieces because an icon may be a picture, which only the renderer can place,
// and the box is sized to its widest row measured as the cells a picture
// fills.
func (m Model) offerRow(o offer, c offerCols, i int, selected bool) []rowSeg {
	sel := func(s string) string {
		if !selected {
			return s
		}
		return paint(stChatSel, s)
	}
	head := ""
	if c.digits {
		num := " "
		if i < quickRows {
			num = strconv.Itoa((i + 1) % 10)
		}
		head += stDim.Render(num) + " "
	}
	name := sel(fit(truncate(o.name, c.name), c.name))
	if !c.icon {
		return []rowSeg{{text: sel(head) + name}}
	}
	if pic := m.iconPic(o.icon); pic.cols > 0 {
		pad := pickerIconCols - pic.cols
		if head == "" {
			return []rowSeg{{pic: pic}, {text: sel(strings.Repeat(" ", pad) + " " + name)}}
		}
		return []rowSeg{{text: sel(head)}, {pic: pic}, {text: sel(strings.Repeat(" ", pad) + " " + name)}}
	}
	return []rowSeg{{text: sel(head + fit(o.icon.text, pickerIconCols) + " " + name)}}
}

// iconPic is the picture an icon is drawn as, if the terminal draws one. A
// face goes the way a replier's does on a summary line, at the same narrow
// size.
func (m Model) iconPic(i offerIcon) picture {
	switch {
	case i.avatar:
		return m.pics.disc(i.image, i.id, i.name, pickerIconCols, 1)
	case i.image != "":
		return m.pics.place(i.image, pickerIconCols, 1)
	}
	return picture{}
}

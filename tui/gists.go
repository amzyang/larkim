package tui

import "github.com/amzyang/larkim/store"

// rowGist is one chat-list row's second line before it is laid out: the
// reactions it carries and the message behind them. It comes as segments only
// when a picture stands in the line — the same shape the leaf functions
// already hand back.
type rowGist struct {
	chips   []rowSeg
	text    string
	summary []rowSeg
}

// gistCache holds them between the calls that ask. picturePrepare walks the
// visible rows for the pictures on them and the pane then draws those same
// rows, so without this every row parses its card and its mentions twice a
// frame — once per keystroke, on a list a screen tall.
type gistCache struct {
	of   []listRow
	rows map[string]rowGist
}

func newGistCache() *gistCache { return &gistCache{rows: map[string]rowGist{}} }

// hold keeps what was summarised while the interleave behind it is the same
// one, and drops the lot when a reload builds a new one. rowsCache already
// rebuilds that slice exactly when either list arrives anew, so taking its
// answer leaves one rule to keep in step rather than two that have to agree.
// A key that only moves a cursor rebuilds nothing, and the lines it drew
// stand with it.
func (g *gistCache) hold(rows []listRow) {
	if sameSlice(g.of, rows) {
		return
	}
	g.of = rows
	clear(g.rows)
}

// at is the row's gist, computed once per frame. A thread is summarised by its
// own replies and carries no reactions; the chat's row is where those show.
func (g *gistCache) at(r listRow, self string, pics emojiPics) rowGist {
	key := r.key()
	if v, ok := g.rows[key]; ok {
		return v
	}
	var v rowGist
	if r.isThread() {
		v.text, v.summary = threadRowGist(r, self, pics)
	} else {
		v.chips = chatChips(r.chat, pics)
		v.text, v.summary = chatSummary(r.chat, self, pics)
	}
	g.rows[key] = v
	return v
}

// withDraft stands the reader's saved draft in for the message a row was going
// to summarise. It runs after the cache and never in it: a draft changes each
// time its box is saved, and a save rebuilds no list, so a cached line would
// hold the row against its own words. The pencil in the marker slot already
// says whose they are, so they carry no sender prefix; the chips stay, as the
// reactions on the message the draft stands in front of.
func (g rowGist) withDraft(d store.Draft) rowGist {
	if d.Empty() {
		return g
	}
	return rowGist{chips: g.chips, text: stDim.Render(flatten(d.Text))}
}

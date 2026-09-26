package tui

// rowGist is one chat-list row's second line before it is laid out: the
// reactions it carries and the message behind them. It comes as segments only
// when a picture stands in the line — the same shape the leaf functions
// already hand back.
type rowGist struct {
	chips   []rowSeg
	text    string
	summary []rowSeg
}

// gistCache holds one frame of them. picturePrepare walks the visible rows for
// the pictures on them and the pane then draws those same rows, so without
// this every row parses its card and its mentions twice a frame — once per
// keystroke, on a list a screen tall.
type gistCache struct{ rows map[string]rowGist }

func newGistCache() *gistCache { return &gistCache{rows: map[string]rowGist{}} }

// begin drops the frame before. These lines are derived from model state that
// Update is about to change, so they live exactly one pass through it.
func (g *gistCache) begin() { clear(g.rows) }

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

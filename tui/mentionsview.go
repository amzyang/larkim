package tui

import (
	"context"

	tea "charm.land/bubbletea/v2"
)

// mentionsLabel names the panel and the rule over its rows. It is the client's
// own word for this list, so the two are read as the same thing.
const mentionsLabel = "@我"

// mentionsLimit is how far back the list reaches. Being named is rare next to
// being sent to, so a hundred covers a long stretch of history without the
// panel becoming something to scroll rather than to act on.
const mentionsLimit = 100

// mentionsLoadedMsg carries the answer to a :mentions run.
type mentionsLoadedMsg struct {
	hits []searchHit
	meta msgMeta
}

// loadMentions gathers every message naming the reader. It answers from
// mentions_json alone, so there is no round trip and no rest timer: unlike a
// search there is nothing to type, and the query cannot change under it.
func loadMentions(d Deps) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		msgs, err := d.Store.MentionsOf(ctx, d.Self, mentionsLimit)
		if err != nil {
			return errMsg{err}
		}
		meta, err := loadMeta(ctx, d.Store, msgs)
		if err != nil {
			return errMsg{err}
		}
		hits := make([]searchHit, 0, len(msgs))
		for _, msg := range msgs {
			hits = append(hits, searchHit{kind: hitMessage, msg: msg})
		}
		return mentionsLoadedMsg{hits: hits, meta: meta}
	}
}

// openMentions puts the panel up on what named the reader. It borrows the
// search panel whole — the same pane, cursor, rows and Enter — because a
// mention is a message hit like any other; only where the list comes from and
// what the rule over it says differ.
func (m Model) openMentions() (tea.Model, tea.Cmd) {
	if m.deps.Self == "" {
		return m.notify("who you are is not known yet; the first sync settles it", true), nil
	}
	m.mode = modeSearch
	m.searching, m.mentions = true, true
	m.focus = paneMessages
	m.searchLocal, m.searchRemote, m.searchHits, m.msgIdx, m.msgTop = nil, nil, nil, 0, 0
	m.searchQuery = ""
	m.cmdline.Reset()
	m.rebuildMessages()
	return m, loadMentions(m.deps)
}

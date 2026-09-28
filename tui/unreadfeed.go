package tui

// The Feishu client has no counterpart to this panel. What it offers is a
// filter over the chat list — the chats narrow, but the reader still opens
// them one at a time. This lays every waiting chat out as one page instead,
// parted into a section each, because the round trip a terminal pays to open a
// chat is a whole page rebuild and the client pays nothing for it. The
// semantics stay the client's: Enter goes to the chat, r answers it, and
// nothing seen here is taken as read.

import (
	"cmp"
	"context"
	"slices"
	"strconv"

	tea "charm.land/bubbletea/v2"

	"github.com/amzyang/larkim/store"
)

const (
	// unreadLabel names the panel. It is the client's own word for what the
	// badge counts, so the two read as the same thing.
	unreadLabel = "Unread"
	// unreadSectionLimit caps one chat's stretch. A chat with more than this
	// waiting is a firehose rather than a backlog: its section ends in a line
	// saying so, and Enter goes there.
	unreadSectionLimit = 80
	// unreadFeedChats caps how many chats the page reaches. Together with the
	// section cap it bounds the page at anchoredPageSize, which is the size
	// rebuildMessages is already known to redraw without stuttering.
	unreadFeedChats = 20
)

// unreadFeed is one visit to the panel. A section's anchor is taken once and
// held for as long as the chat is still waiting: one that re-anchored on every
// reload would shrink under the page the reader is on. A chat whose backlog is
// settled leaves instead, taking its anchor with it. A chat that starts waiting
// later joins at the end, where there is nothing above it to renumber. Leaving
// drops the feed, which is what re-orders it.
type unreadFeed struct {
	sections []unreadSection
	// from is the chat that was open when the panel went up, so Esc puts the
	// reader back where they were rather than nowhere.
	from string
	// loaded is the chat whose draft the composer is showing. Until that
	// chat's side load lands the composer is nobody's, and writing it back
	// would file an empty draft under a chat whose own was never shown —
	// which store.SaveDraft reads as a delete.
	loaded string
}

// unreadSection is one chat's stretch of the page.
type unreadSection struct {
	chatID string
	name   string
	// anchorMs is the oldest message the badge still counts. The section runs
	// from there to the newest, messages already read included, so a chat
	// reads as the conversation it is rather than as a sieve.
	anchorMs int64
	// cut says the stretch hit unreadSectionLimit and there is more below it.
	cut bool
	// count, atMe and muted are what the chat's own row in the list says
	// about it, taken once when the section is anchored. The page runs across
	// chats with that list out of sight, so the rule has to answer what the
	// row would have: how much is waiting here, whether any of it names the
	// reader, and whether this chat asked not to be pulled at.
	count int64
	atMe  bool
	muted bool
}

// label is what the section's rule is named by, and what a line pointing at
// the section elsewhere calls it.
func (s unreadSection) label() string {
	return cmp.Or(flatten(s.name), s.chatID)
}

// rule is the whole of what the section's rule says. It is built here rather
// than where the rule is drawn because two places draw it — the row in the
// page and the copy pinned under the title — and a reader scrolling past the
// boundary sees both at once.
func (s unreadSection) rule() string {
	out := stBold.Render(s.label())
	if s.count > 0 {
		out += stDim.Render(" · " + strconv.FormatInt(s.count, 10))
	}
	if at := atMeMark(s.atMe); at != "" {
		out += " " + at
	}
	if mute := muteMark(s.muted); mute != "" {
		out += " " + mute
	}
	return out
}

// section is the stretch a chat holds on the page.
func (f *unreadFeed) section(chatID string) unreadSection {
	for _, s := range f.sections {
		if s.chatID == chatID {
			return s
		}
	}
	return unreadSection{chatID: chatID}
}

// unreadFeedLoadedMsg carries a page of the panel.
type unreadFeedLoadedMsg struct {
	sections []unreadSection
	msgs     []store.Message
	meta     msgMeta
}

// chatSideMsg is what a chat owns outside its messages: the draft written into
// it and who it reaches. The panel walks between chats without loading a page,
// so these arrive on their own.
type chatSideMsg struct {
	chatID string
	draft  store.Draft
	roster []store.Contact
}

// unreadAnchors is every chat the list badges, oldest backlog first, so the
// whole page reads as one timeline. The set comes back whole rather than cut to
// unreadFeedChats: joinUnread reads it as what is still waiting, and a cut one
// cannot tell a chat that has been read from one pushed past the cap by a
// newcomer with an older backlog.
//
// Silenced chats come in. What silencing refuses is being pulled at — the
// badge, the dot, the n key walking the reader onto the chat — and opening the
// panel is the reader doing the pulling, asking for the whole backlog on one
// page. Silenced messages are still out, the predicate being the badge's own.
func unreadAnchors(rows []store.UnreadAnchor, chats []store.Chat) []unreadSection {
	byID := make(map[string]store.Chat, len(chats))
	for _, c := range chats {
		byID[c.ChatID] = c
	}
	out := make([]unreadSection, 0, len(rows))
	for _, a := range rows {
		c, ok := byID[a.ChatID]
		if !ok {
			continue
		}
		out = append(out, unreadSection{chatID: a.ChatID, name: c.Name, anchorMs: a.FirstMs,
			count: c.UnreadCount, atMe: c.UnreadMention, muted: c.Muted})
	}
	slices.SortFunc(out, func(a, b unreadSection) int {
		return cmp.Or(cmp.Compare(a.anchorMs, b.anchorMs), cmp.Compare(a.chatID, b.chatID))
	})
	return out
}

// joinUnread is the page's sections after a reload: the ones still waiting, in
// the order they are already drawn in, and then whatever has started waiting
// since. A chat whose backlog has been settled leaves with it, so the anchor it
// held cannot re-open that stretch when the chat next says something. A
// newcomer joins at the end however old its backlog — the only part of the page
// free to grow is the part below the rows the reader has seen.
func joinUnread(held, fresh []unreadSection) []unreadSection {
	stillWaiting := make(map[string]bool, len(fresh))
	for _, s := range fresh {
		stillWaiting[s.chatID] = true
	}
	// Built fresh rather than appended to: held is the page the model is still
	// drawing, and its spare capacity is not this function's to write into.
	out := make([]unreadSection, 0, len(held)+len(fresh))
	for _, s := range held {
		if stillWaiting[s.chatID] {
			out = append(out, s)
		}
	}
	for _, s := range fresh {
		if slices.ContainsFunc(out, func(x unreadSection) bool { return x.chatID == s.chatID }) {
			continue
		}
		out = append(out, s)
	}
	return out[:min(len(out), unreadFeedChats)]
}

// feedWaiting reports whether a row is one the panel would gather, which is
// what the Unread row counts over the list. Threads are out although they
// wait too: their replies are not what the panel gathers, and the chat one
// happens in is counted through its own row. Silenced chats are in, on
// unreadAnchors' rule — which is what parts this from waitingFor, the rule
// the n key walks the list by.
func feedWaiting(r listRow, unread map[string]int64) bool {
	return !r.isFeed() && !r.isThread() && r.unread(unread) > 0
}

// gatherUnread reads one page of the panel. keep is the anchors already held,
// nil on the first page; it answers with the sections it could fill, each
// carrying whether it was cut short.
func gatherUnread(ctx context.Context, st *store.Store, self string, chats []store.Chat, keep []unreadSection) ([]unreadSection, []store.Message, msgMeta, error) {
	anchors, err := st.UnreadAnchors(ctx)
	if err != nil {
		return nil, nil, msgMeta{}, err
	}
	keep = joinUnread(keep, unreadAnchors(anchors, chats))
	sections := make([]unreadSection, 0, len(keep))
	var msgs []store.Message
	for _, sec := range keep {
		// One past the cap: a chat holding exactly the cap has nothing below
		// it, and a page asked for exactly the cap cannot tell the two apart.
		rows, err := st.ListMessages(ctx, messageQuery(sec.chatID, sec.anchorMs, unreadSectionLimit+1))
		if err != nil {
			return nil, nil, msgMeta{}, err
		}
		// An anchor can be a thread reply, which the page folds into a root
		// older than the anchor and so outside the window. Nothing is left to
		// draw then, and an empty rule is not a section.
		if len(rows) == 0 {
			continue
		}
		if sec.cut = len(rows) > unreadSectionLimit; sec.cut {
			rows = rows[:unreadSectionLimit]
		}
		sections = append(sections, sec)
		msgs = append(msgs, rows...)
	}
	// One pass over the whole page: every lookup loadMeta makes is by id, so
	// nothing in it is scoped to a chat.
	meta, err := loadMeta(ctx, st, self, msgs)
	if err != nil {
		return nil, nil, msgMeta{}, err
	}
	return sections, msgs, meta, nil
}

// feedPage is one read of the panel: the sections it filled and their
// messages, beside the chat listing the page is measured against — what says a
// chat exists, what its rule is named, and how many chats were left out.
type feedPage struct {
	chats    []store.Chat
	sections []unreadSection
	msgs     []store.Message
	meta     msgMeta
}

// readFeed reads one page. The chats listing is read here rather than taken
// from the model: it is what says a chat exists and what its rule is named,
// and the one the model holds is by definition the one from before whatever
// prompted the reload.
func readFeed(ctx context.Context, d Deps, keep []unreadSection) (feedPage, error) {
	chats, err := d.Store.ListChats(ctx, store.ChatQuery{Self: d.Self})
	if err != nil {
		return feedPage{}, err
	}
	p := feedPage{chats: chats}
	p.sections, p.msgs, p.meta, err = gatherUnread(ctx, d.Store, d.Self, chats, keep)
	return p, err
}

// loadUnreadFeed reads the page for the panel.
func loadUnreadFeed(d Deps, keep []unreadSection) tea.Cmd {
	return func() tea.Msg {
		p, err := readFeed(context.Background(), d, keep)
		if err != nil {
			return errMsg{err}
		}
		return unreadFeedLoadedMsg{sections: p.sections, msgs: p.msgs, meta: p.meta}
	}
}

// loadChatSide fetches what the composer needs to answer a chat the panel is
// only showing. A draft or a roster the store cannot answer for costs the
// composer its text or its candidates, not the reader their page.
func loadChatSide(d Deps, chatID string) tea.Cmd {
	return func() tea.Msg {
		ctx := context.Background()
		draft, err := d.Store.LoadDraft(ctx, chatID)
		if err != nil {
			d.log().Error("load draft", "chat_id", chatID, "err", err)
			draft = store.Draft{ChatID: chatID}
		}
		roster, err := d.Store.ChatRoster(ctx, chatID, d.Self)
		if err != nil {
			d.log().Error("load roster", "chat_id", chatID, "err", err)
			roster = nil
		}
		return chatSideMsg{chatID: chatID, draft: draft, roster: roster}
	}
}

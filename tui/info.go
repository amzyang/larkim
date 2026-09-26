package tui

import (
	"context"
	"strconv"
	"strings"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/store"
)

// infoLoadedMsg carries a chat's roster for the info pane.
type infoLoadedMsg struct {
	chatID  string
	members []store.Contact
}

// toggleInfo opens the info pane on the open chat, or closes it. It takes the
// right-hand pane, which the thread and the assistant already share: three
// things that are all about the chat being read, only one of which is wanted
// at a time.
func (m Model) toggleInfo() (tea.Model, tea.Cmd) {
	if m.infoOpen {
		m.infoOpen = false
		m.layout()
		return m, nil
	}
	if m.chatID == "" {
		return m.notify("open a chat first", true), nil
	}
	m.closeRight()
	m.infoOpen, m.infoTop = true, 0
	m.aiOpen = false
	m.layout()
	cmds := []tea.Cmd{loadInfo(m.deps, m.chatID)}
	// A chat of two answers with the person across from it, whom the contacts
	// table names because a pair keeps no roster. A group's card draws nobody
	// the roster has not already brought, so it pays for no such read.
	if c, ok := m.currentChat(); ok && c.ChatMode == "p2p" {
		cmds = append(cmds, loadContacts(m.deps))
	}
	return m, tea.Batch(cmds...)
}

// loadInfo fetches the roster. Everything else the pane draws is already on
// the chat row the list holds.
func loadInfo(d Deps, chatID string) tea.Cmd {
	return func() tea.Msg {
		members, err := d.Store.ChatMembers(context.Background(), chatID)
		if err != nil {
			d.log().Error("load chat members", "chat_id", chatID, "err", err)
			return infoLoadedMsg{chatID: chatID}
		}
		return infoLoadedMsg{chatID: chatID, members: members}
	}
}

// infoLines is what the pane draws, one line per row, already styled. It is
// built whole rather than streamed so the pane scrolls like any list.
//
// A group answers with what the chat is and who is in it; a chat of two
// answers with the person across from it, since "members" of a pair is a
// question nobody asks.
func (m Model) infoLines(w int) []string {
	c, ok := m.currentChat()
	if !ok {
		return nil
	}
	var out []string
	label := func(k, v string) {
		if v == "" {
			return
		}
		out = append(out, fit(stDim.Render(k+" ")+v, w))
	}
	name := flatten(c.Name)
	if name == "" {
		name = c.ChatID
	}
	out = append(out, fit(stBold.Render(truncate(name, w)), w))

	var tags []string
	if c.External {
		tags = append(tags, stErr.Render("external"))
	}
	if c.ChatStatus != "" && c.ChatStatus != "normal" {
		tags = append(tags, stErr.Render(c.ChatStatus))
	}
	if c.Muted {
		tags = append(tags, stDim.Render("muted"))
	}
	if len(tags) > 0 {
		out = append(out, fit(strings.Join(tags, " "), w))
	}
	out = append(out, fit(stDim.Render(chatKindWord(c.ChatMode)), w))
	if d := flatten(c.Description); d != "" {
		out = append(out, fit("", w))
		for _, line := range wrap(d, w) {
			out = append(out, fit(stDim.Render(line), w))
		}
	}
	if c.SyncError != "" {
		out = append(out, fit("", w), fit(stErr.Render("sync: ")+stDim.Render(truncate(c.SyncError, w-6)), w))
	}

	if c.ChatMode == "p2p" {
		peer := m.infoPeer(c)
		out = append(out, fit("", w))
		label("email", peer.EnterpriseEmail)
		if peer.EnterpriseEmail == "" {
			label("email", peer.Email)
		}
		label("dept", peer.Department)
		if peer.IsCrossTenant {
			out = append(out, fit(stDim.Render("outside this tenant"), w))
		}
		label("id", peer.OpenID)
		return out
	}

	out = append(out, fit("", w))
	// A capped roster is a part of the membership, and a bare count of it
	// reads as the size of the chat.
	n := strconv.Itoa(len(m.info))
	if c.MembersTruncated {
		out = append(out, fit(stBold.Render("Members ")+stErr.Render("partial"), w),
			fit(stDim.Render("the server caps this list; @ reaches only these "+n), w))
	} else {
		out = append(out, fit(stBold.Render("Members ")+stDim.Render(n), w))
	}
	for _, p := range m.info {
		who := flatten(p.Name)
		if who == "" {
			who = p.OpenID
		}
		if p.OpenID == c.OwnerID {
			who += stDim.Render(" owner")
		}
		if p.IsBot {
			who += botBadge
		}
		if p.IsCrossTenant {
			who += stDim.Render(" ext")
		}
		out = append(out, fit("  "+who, w))
	}
	return out
}

// infoPeer is the contact a p2p chat is with, which the chat row already
// carries enough of to draw without another query.
func (m Model) infoPeer(c store.Chat) store.Contact {
	for _, p := range m.info {
		if p.OpenID == c.P2PTargetID {
			return p
		}
	}
	for _, p := range m.contacts {
		if p.OpenID == c.P2PTargetID {
			return p
		}
	}
	return store.Contact{OpenID: c.P2PTargetID}
}

// chatKindWord names what a chat is, in the client's own terms.
func chatKindWord(mode string) string {
	switch mode {
	case "p2p":
		return "direct message"
	case "topic":
		return "topic group"
	default:
		return "group"
	}
}

// renderInfo draws the pane, scrolled to infoTop.
func (m Model) renderInfo(h int) string {
	w := m.rightWidth() - 2
	all := m.infoLines(w)
	lines := make([]string, 0, h)
	for i := m.infoTop; i < len(all) && len(lines) < h; i++ {
		lines = append(lines, all[i])
	}
	for len(lines) < h {
		lines = append(lines, fit("", w))
	}
	return paneStyle(m.focus == paneThread, w).Height(h).Render(strings.Join(lines, "\n"))
}

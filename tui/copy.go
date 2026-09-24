package tui

import (
	"context"
	"fmt"
	"math/rand/v2"
	"path/filepath"
	"slices"
	"strings"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/amzyang/larkim/agentctx"
	"github.com/amzyang/larkim/store"
)

const (
	// chatsCopyAge and chatsCopyLimit bound the chats pane's y: enough of
	// today's conversation to brief an agent, without opening the chat.
	chatsCopyAge   = 24 * time.Hour
	chatsCopyLimit = 10
	// copyAllLimit stands in for "no limit"; ListMessages falls back to 100
	// when none is given.
	copyAllLimit = 1_000_000
	// followUpLimit is the page size of the command appended to every copy.
	followUpLimit = 100
)

// contextMsg carries a finished agent context back into Update.
type contextMsg struct {
	text string
	n    int
	chat string
}

// copySpec says what a copy should cover. Either msgs is already in hand (a
// cursor or a VISUAL range) and is copied as picked, or rng describes a range
// to query, which covers the chat stream without its folded thread replies.
type copySpec struct {
	chatID string
	msgs   []store.Message
	rng    agentctx.Range
}

// copyQuery turns a range into a store query. A count walks back from the
// newest message, so it is fetched descending and flipped afterwards; the
// replies are excluded in SQL so a count of 200 yields 200 usable messages
// even in a chat that lives in its threads.
func copyQuery(chatID string, r agentctx.Range, now time.Time) store.MessageQuery {
	q := store.MessageQuery{ChatID: chatID, Limit: copyAllLimit, ExcludeThreadReplies: true}
	if r.Since > 0 {
		q.SinceMs = now.Add(-r.Since).UnixMilli()
	}
	if r.Limit > 0 {
		q.Limit, q.Desc = r.Limit, true
	}
	return q
}

// copyContext assembles the agent context off the Update loop: a copy needs
// the chat, its member count, the contacts behind every open id, the thread
// reply counts and the attachments, none of which the model holds.
func copyContext(d Deps, spec copySpec) tea.Cmd {
	return func() tea.Msg {
		ctx, now := context.Background(), time.Now()
		msgs := spec.msgs
		if msgs == nil {
			q := copyQuery(spec.chatID, spec.rng, now)
			rows, err := d.Store.ListMessages(ctx, q)
			if err != nil {
				return errMsg{err}
			}
			if q.Desc {
				slices.Reverse(rows)
			}
			msgs = rows
		}
		in, err := assemble(ctx, d, spec.chatID, msgs, now)
		if err != nil {
			return errMsg{err}
		}
		name := in.Chat.Name
		if name == "" {
			name = spec.chatID
		}
		return contextMsg{text: agentctx.Render(in), n: len(msgs), chat: name}
	}
}

// assemble resolves everything Render needs but cannot derive from messages.
func assemble(ctx context.Context, d Deps, chatID string, msgs []store.Message, now time.Time) (agentctx.Input, error) {
	chat, err := d.Store.GetChat(ctx, chatID)
	if err != nil {
		return agentctx.Input{}, err
	}
	ids := agentctx.Participants(d.Self, msgs)
	lookup := ids
	if chat.ChatMode == "p2p" && chat.P2PTargetID != "" {
		lookup = append(slices.Clone(ids), chat.P2PTargetID)
	}
	contacts, err := d.Store.ContactsByIDs(ctx, lookup)
	if err != nil {
		return agentctx.Input{}, err
	}
	names := map[string]string{}
	var msgIDs, threadIDs []string
	for _, m := range msgs {
		msgIDs = append(msgIDs, m.MessageID)
		if m.SenderName != "" {
			names[m.SenderID] = m.SenderName
		}
		if m.ThreadID != "" && m.MessagePosition >= 0 {
			threadIDs = append(threadIDs, m.ThreadID)
		}
	}
	threads, err := d.Store.ThreadReplyCounts(ctx, chatID, threadIDs)
	if err != nil {
		return agentctx.Input{}, err
	}
	res, err := d.Store.ResourcesForMessages(ctx, msgIDs)
	if err != nil {
		return agentctx.Input{}, err
	}
	for id, rs := range res {
		for i := range rs {
			if rs[i].LocalPath != "" && !filepath.IsAbs(rs[i].LocalPath) {
				rs[i].LocalPath = filepath.Join(d.DataDir, rs[i].LocalPath)
			}
		}
		res[id] = rs
	}
	members, err := d.Store.ChatMemberCount(ctx, chatID)
	if err != nil {
		return agentctx.Input{}, err
	}
	people := make([]agentctx.Person, 0, len(ids))
	for _, id := range ids {
		people = append(people, person(id, contacts[id], names[id]))
	}
	in := agentctx.Input{
		Now:      now,
		Boundary: agentctx.Boundary(rand.New(rand.NewPCG(rand.Uint64(), rand.Uint64())), msgs),
		Chat:     chat,
		Members:  int(members),
		People:   people,
		Messages: msgs,
		Threads:  threads,
		Res:      res,
		More:     followUp(d, chatID, msgs),
	}
	if d.Self != "" {
		in.Self = person(d.Self, contacts[d.Self], names[d.Self])
	}
	if chat.ChatMode == "p2p" && chat.P2PTargetID != "" {
		peer := person(chat.P2PTargetID, contacts[chat.P2PTargetID], chat.Name)
		in.Contact = &peer
	}
	return in, nil
}

// person prefers the contact cache and falls back to the name the message
// carried; an open id never seen as a contact still gets a line.
func person(id string, c store.Contact, fallback string) agentctx.Person {
	name := c.Name
	if name == "" {
		name = fallback
	}
	mail := c.EnterpriseEmail
	if mail == "" {
		mail = c.Email
	}
	return agentctx.Person{Name: name, OpenID: id, Email: mail, Bot: c.IsBot}
}

// followUp is the command that resumes digging where this copy stopped. The
// binary name is a literal: os.Args[0] may be "./larkim" or a test binary.
func followUp(d Deps, chatID string, msgs []store.Message) string {
	if len(msgs) == 0 {
		return ""
	}
	cmd := "larkim"
	if d.ConfigPath != "" {
		cmd += " --config " + shellQuote(d.ConfigPath)
	}
	return fmt.Sprintf("%s messages list \\\n  --chat %s --before %s --limit %d --json",
		cmd, chatID, msgs[0].MessageID, followUpLimit)
}

// shellQuote makes a path safe to paste into a shell; a data dir with a
// space in it would otherwise split the follow-up command in two.
func shellQuote(s string) string {
	if !strings.ContainsAny(s, " \t'\"\\$`") {
		return s
	}
	return "'" + strings.ReplaceAll(s, "'", `'\''`) + "'"
}

// humanBytes sizes a copy or an attachment the way a person judges it at a
// glance.
func humanBytes(n int64) string {
	if n < 1024 {
		return fmt.Sprintf("%d B", n)
	}
	switch kb := float64(n) / 1024; {
	case kb < 10:
		return fmt.Sprintf("%.1f KB", kb)
	case kb < 1024:
		return fmt.Sprintf("%.0f KB", kb)
	default:
		return fmt.Sprintf("%.1f MB", kb/1024)
	}
}

func plural(n int, one, many string) string {
	if n == 1 {
		return "1 " + one
	}
	return fmt.Sprintf("%d %s", n, many)
}

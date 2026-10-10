// Package triage decides which fresh arrivals need the reader now, raises a
// desktop banner for those, drafts replies into draft_candidates and keeps the
// reminders a message asked for.
//
// The Lark client has no counterpart: it notifies on every unmuted message and
// leaves the reader to sort them. What is copied from it is the vocabulary —
// p2p, @me, calls and muted chats mean what they mean there — and the rule
// that a banner never fires while the reader is looking at the conversation.
package triage

import (
	"encoding/json/v2"
	"regexp"
	"slices"
	"strings"

	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
)

// Level is how urgent a message is.
type Level string

const (
	// P0 raises a banner and is drafted for.
	P0 Level = "P0"
	// P1 is read in its turn.
	P1 Level = "P1"
	// Drop is nothing the reader needs to answer: their own, a bot's, empty.
	Drop Level = "drop"
)

// Verdict is a level and what decided it.
type Verdict struct {
	Level  Level
	Reason string
	// JevP is the judge's attention probability, nil when a rule decided.
	JevP *float64
}

// vcTypes are the messages that start or share a call. They are urgent by
// nature and often carry no text at all.
var vcTypes = []string{"video_chat", "vc_meeting"}

// Rules is the rule half of the judgement.
type Rules struct {
	// watch holds users and chats alike: ou_ and oc_ ids never collide.
	watch    map[string]bool
	keywords []*regexp.Regexp
}

// NewRules compiles the watch list and keywords of n, which
// config.Notifications.Validate has passed.
func NewRules(n config.Notifications) Rules {
	r := Rules{watch: map[string]bool{}}
	for _, w := range n.Watch {
		r.watch[w] = true
	}
	for _, k := range n.Keywords {
		r.keywords = append(r.keywords, regexp.MustCompile(k))
	}
	return r
}

// Watches reports whether id, a user or a chat, is on the watch list.
func (r Rules) Watches(id string) bool { return r.watch[id] }

// Classify judges m in chat c by rule, for the reader self. It reports false
// for a message no rule speaks to, which is the judge's to decide. The order of the checks is the order a reason is credited in: the
// first that matches names it.
func (r Rules) Classify(m store.Message, c store.Chat, self string) (Verdict, bool) {
	vc := slices.Contains(vcTypes, m.MsgType)
	body := Text(m)
	switch {
	case m.SenderID == self:
		return Verdict{Level: Drop, Reason: "self"}, true
	case m.SenderType != "user":
		return Verdict{Level: Drop, Reason: "non-user"}, true
	case !vc && strings.TrimSpace(body) == "":
		return Verdict{Level: Drop, Reason: "empty"}, true
	case vc:
		return Verdict{Level: P0, Reason: "vc"}, true
	case c.ChatMode == "p2p":
		return Verdict{Level: P0, Reason: "p2p"}, true
	case mentions(m, self):
		return Verdict{Level: P0, Reason: "at-me"}, true
	case r.watch[m.SenderID]:
		return Verdict{Level: P0, Reason: "watch-user"}, true
	case r.watch[m.ChatID]:
		return Verdict{Level: P0, Reason: "watch-chat"}, true
	}
	for _, re := range r.keywords {
		if re.MatchString(body) {
			return Verdict{Level: P0, Reason: "keyword:" + re.String()}, true
		}
	}
	return Verdict{}, false
}

// mentions reads the ids out of mentions_json rather than matching the text,
// so an @all, whose id is not the reader's, is not a mention of them.
func mentions(m store.Message, self string) bool {
	if m.MentionsJSON == "" {
		return false
	}
	var ms []mention
	if json.Unmarshal([]byte(m.MentionsJSON), &ms) != nil {
		return false
	}
	return slices.ContainsFunc(ms, func(x mention) bool { return x.ID == self })
}

type mention struct {
	ID string `json:"id"`
}

// Text is what a message says, as the banner and the keywords read it.
func Text(m store.Message) string {
	t, _ := card.MessageText(m.ContentRaw, m.Content, m.RenderedAt > 0)
	return t
}

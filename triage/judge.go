package triage

import (
	"cmp"
	"context"

	"github.com/amzyang/larkim/ai"
	"github.com/amzyang/larkim/jev"
)

// attention is the probability over which Jev's "does this need the reader
// soon" answer counts as yes. It is the midpoint, not yet measured against
// this reader's own gray-zone messages; `larkim triage list --level P1` shows
// jev_p beside each verdict, which is where a better cut is read off.
const attention = 0.5

// addressed is the probability over which Jev's "is this meant for the
// reader" answer counts as yes. On this reader's chats a message to someone
// else came out near 0.15 and one to them above 0.8, so the midpoint has room
// on both sides.
const addressed = 0.5

// toReader names the addressee question in the request and its answer.
const toReader = "to_reader"

// Ranker is the one call JevJudge makes.
type Ranker interface {
	Rank(ctx context.Context, a jev.Ask) (jev.Rank, error)
}

// JevJudge asks Jev what a gray-zone message asks of the reader, whether it
// can wait, and whether it is meant for them at all. One request carries all
// three questions.
type JevJudge struct {
	Ranker Ranker
}

// asks are the things a message can ask of its reader. Only the first two are
// worth interrupting for, and only when the message also cannot wait.
var asks = map[string]string{
	"reply":   "asks the reader a question or for a decision only they can give",
	"act":     "asks the reader to do something: fix, review, check, attend, hand something over",
	"fyi":     "tells the reader something without asking anything of them",
	"chatter": "small talk, banter, thanks, or a reaction to someone else",
}

// grayState is what Jev reads about the message.
type grayState struct {
	Chat    string   `json:"chat"`
	Reader  string   `json:"reader,omitempty"`
	Sender  string   `json:"sender"`
	Message string   `json:"message"`
	Before  []string `json:"before"`
}

// Judge answers a.
func (j JevJudge) Judge(ctx context.Context, a GrayAsk) (Verdict, error) {
	state := grayState{Chat: cmp.Or(a.Chat.Name, a.Chat.ChatID), Reader: a.Reader,
		Sender: cmp.Or(a.Message.SenderName, a.Message.SenderID), Message: Text(a.Message), Before: []string{}}
	for _, m := range a.Context {
		if l := ai.Line(m, a.Self, nil); l != "" {
			state.Before = append(state.Before, l)
		}
	}
	r, err := j.Ranker.Rank(ctx, jev.Ask{
		State:   state,
		Pick:    "The reader is a member of the group chat `chat`, and the colleague `sender` just posted `message` in it, after the lines in `before`; the reader's own lines there are marked (me). What does `message` ask of the reader?",
		Options: asks,
		Fits:    "Would the reader be worse off for seeing `message` an hour from now rather than right away?",
		True:    "yes: it is waiting on the reader, or it is something going wrong that the reader is expected to act on",
		False:   "no: it can sit in the unread list until the reader gets to it",
		// Asked as its own question rather than as an option of the pick: as
		// an option it loses to the content, and a log pasted for a colleague
		// who @-ed its sender comes out act.
		Nouls: map[string]jev.Noul{toReader: {
			Instructions: "The reader, named `reader`, is a member of the group chat `chat`; their own lines in `before` are marked (me). Is `message` meant for the reader?",
			True:         "yes: it names the reader, answers or follows up on a (me) line, or calls on everyone in the chat",
			False:        "no: it continues an exchange in `before` between other people, such as an answer to someone else's @ or something pasted for whoever asked for it",
		}},
	})
	if err != nil {
		return Verdict{}, err
	}
	top := ""
	if len(r.Options) > 0 {
		top = r.Options[0].Key
	}
	v := Verdict{Level: P1, Reason: "jev:" + top, JevP: new(r.Fits)}
	if r.Fits < attention || (top != "reply" && top != "act") {
		return v, nil
	}
	if r.Nouls[toReader] < addressed {
		v.Reason = "jev:others"
		return v, nil
	}
	v.Level = P0
	return v, nil
}

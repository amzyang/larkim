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

// Ranker is the one call JevJudge makes.
type Ranker interface {
	Rank(ctx context.Context, a jev.Ask) (jev.Rank, error)
}

// JevJudge asks Jev what a gray-zone message asks of the reader and whether
// it can wait. One request carries both questions.
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
	Sender  string   `json:"sender"`
	Message string   `json:"message"`
	Before  []string `json:"before"`
}

// Judge answers a.
func (j JevJudge) Judge(ctx context.Context, a GrayAsk) (Verdict, error) {
	state := grayState{Chat: cmp.Or(a.Chat.Name, a.Chat.ChatID), Sender: cmp.Or(a.Message.SenderName, a.Message.SenderID),
		Message: Text(a.Message), Before: []string{}}
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
	})
	if err != nil {
		return Verdict{}, err
	}
	top := ""
	if len(r.Options) > 0 {
		top = r.Options[0].Key
	}
	v := Verdict{Level: P1, Reason: "jev:" + top, JevP: new(r.Fits)}
	if r.Fits >= attention && (top == "reply" || top == "act") {
		v.Level = P0
	}
	return v, nil
}

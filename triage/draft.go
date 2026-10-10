package triage

import (
	"cmp"
	"context"
	_ "embed"
	"encoding/json/v2"
	"fmt"
	"strings"
	"time"
)

// instructions lead the drafter's one turn. They hold only rules a transcript
// alone can honour: the agent has no persona file and no tool to look anything
// up with.
//
//go:embed prompt.md
var instructions string

// remindLayout is how the drafter writes a reminder's time: local wall clock,
// the form a person reads off the message.
const remindLayout = "2006-01-02 15:04"

// maxDrafts is how many candidates a message keeps, the 1–3 the rules ask for.
const maxDrafts = 3

// Answerer is the agent call AgentDrafter makes.
type Answerer interface {
	Answer(ctx context.Context, transcript, prompt, instructions string) (string, error)
}

// AgentDrafter asks the ACP agent for reply candidates and a reminder.
type AgentDrafter struct {
	Answerer Answerer
}

type answer struct {
	Drafts []string `json:"drafts"`
	Format string   `json:"format"`
	Remind *struct {
		At    string `json:"at"`
		Title string `json:"title"`
	} `json:"remind"`
}

// Draft answers a.
func (d AgentDrafter) Draft(ctx context.Context, a DraftAsk) (Draft, error) {
	prompt := fmt.Sprintf("当前时间：%s\n\n目标消息：\n%s", a.Now.Local().Format(remindLayout), a.Target)
	out, err := d.Answerer.Answer(ctx, a.Transcript, prompt, instructions)
	if err != nil {
		return Draft{}, err
	}
	return parseDraft(out)
}

// parseDraft reads the one JSON object the answer is meant to be. An agent
// that fences it or says a word around it is forgiven; one that answers with
// something else entirely is a failed draft.
func parseDraft(out string) (Draft, error) {
	start, end := strings.Index(out, "{"), strings.LastIndex(out, "}")
	if start < 0 || end < start {
		return Draft{}, fmt.Errorf("draft: no JSON object in %q", out)
	}
	var a answer
	if err := json.Unmarshal([]byte(out[start:end+1]), &a); err != nil {
		return Draft{}, fmt.Errorf("draft: %w in %q", err, out)
	}
	d := Draft{Format: cmp.Or(a.Format, "text")}
	if d.Format != "text" && d.Format != "markdown" {
		return Draft{}, fmt.Errorf("draft: format %q", a.Format)
	}
	for _, s := range a.Drafts {
		if s = strings.TrimSpace(s); s != "" && len(d.Texts) < maxDrafts {
			d.Texts = append(d.Texts, s)
		}
	}
	if a.Remind != nil {
		at, err := time.ParseInLocation(remindLayout, strings.TrimSpace(a.Remind.At), time.Local)
		if err != nil {
			return Draft{}, fmt.Errorf("draft: remind.at: %w", err)
		}
		d.Remind = &Remind{At: at, Title: strings.TrimSpace(a.Remind.Title)}
	}
	return d, nil
}

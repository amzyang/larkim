// Package cuacallback implements the account-quota card callback through Cua Driver.
package cuacallback

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/automation"
)

// claimLabel marks a refill button, first ("申请…") or repeat ("再次申请…").
// Every one grants the same +50% to the current period, so any enabled button
// in the chat is equivalent and card text is never parsed; ineligible cards
// carry "未达加油条件" instead and a consumed card keeps no button.
const claimLabel = "申请加油包"

type Quota struct {
	URL string
}

// ParseQuota checks only that the event is a live, positioned card from the
// configured bot in the configured chat; what to press is read from the
// client's buttons, not from the card text.
func ParseQuota(e automation.Event, chatID, senderID string) (Quota, error) {
	m := e.Message
	if e.Version != 1 || e.JobID <= 0 || e.RuleID == "" || chatID == "" || senderID == "" || m.ChatID != chatID || m.SenderID != senderID || m.MsgType != "interactive" || m.Deleted || m.RenderedAt == 0 || !strings.HasPrefix(m.MessageID, "om_") || m.MessagePosition <= 0 {
		return Quota{}, errors.New("event is not a live positioned card in the configured chat and sender scope")
	}
	return Quota{URL: applink.ChatLink(m.ChatID, m.MessageID, m.MessagePosition)}, nil
}

type Element struct {
	Index   int      `json:"element_index"`
	Parent  int      `json:"parent_index"`
	Role    string   `json:"role"`
	Label   string   `json:"label"`
	Value   string   `json:"value"`
	Token   string   `json:"element_token"`
	Enabled bool     `json:"enabled"`
	Actions []string `json:"actions"`
}

type State struct {
	Elements       []Element `json:"elements"`
	Truncated      bool      `json:"truncated"`
	Degraded       bool      `json:"degraded"`
	DegradedReason string    `json:"degraded_reason"`
}

func (s State) Healthy() bool { return !s.Truncated && !s.Degraded && s.DegradedReason == "" }

// claimable returns the tokens of pressable refill buttons in the chat pane.
// Lingxi's AX tree has no per-message container, so buttons are taken from
// the whole loaded chat rather than from one card.
func claimable(s State) []string {
	if !s.Healthy() {
		return nil
	}
	parents := map[int]Element{}
	for _, e := range s.Elements {
		parents[e.Index] = e
	}
	inChat := func(e Element) bool {
		for range len(s.Elements) {
			p, ok := parents[e.Parent]
			if !ok || p.Index == e.Index {
				return false
			}
			if p.Role == "AXWebArea" {
				return p.Label == "messenger-chat"
			}
			e = p
		}
		return false
	}
	var tokens []string
	for _, e := range s.Elements {
		if e.Role == "AXButton" && strings.HasPrefix(strings.TrimPrefix(e.Label, "再次"), claimLabel) && e.Enabled && e.Token != "" && slices.Contains(e.Actions, "AXPress") && inChat(e) {
			tokens = append(tokens, e.Token)
		}
	}
	return tokens
}

type Window struct {
	PID int `json:"pid"`
	ID  int `json:"window_id"`
}

type Driver interface {
	Open(context.Context, string) (Window, error)
	State(context.Context, Window) (State, error)
	Click(context.Context, Window, string) error
}

type Claim struct {
	Driver Driver
}

func (c *Claim) Run(ctx context.Context, q Quota, check bool) automation.Outcome {
	failed := func(err error) automation.Outcome { return automation.Outcome{Status: "failed", Result: err.Error()} }
	w, err := c.Driver.Open(ctx, q.URL)
	if err != nil {
		return failed(err)
	}
	before, err := c.Driver.State(ctx, w)
	if err != nil {
		return failed(err)
	}
	if !before.Healthy() {
		return failed(fmt.Errorf("incomplete Lingxi AX snapshot: %s", before.DegradedReason))
	}
	tokens := claimable(before)
	if len(tokens) == 0 {
		return failed(errors.New("skipped: no claimable button in the chat"))
	}
	if check {
		return failed(fmt.Errorf("check passed: %d claimable button(s) found; no claim submitted", len(tokens)))
	}
	// Once the request leaves this process, transport failure cannot prove that
	// the button's handler did not run. Never submit a second click.
	if err := c.Driver.Click(ctx, w, tokens[0]); err != nil {
		return automation.Outcome{Status: "unknown", Result: err.Error()}
	}
	// A consumed card turns into a receipt without a button, so the claim is
	// verified by the claimable count dropping.
	verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		after, err := c.Driver.State(verifyCtx, w)
		if err != nil {
			return automation.Outcome{Status: "unknown", Result: err.Error()}
		}
		if n := len(claimable(after)); n < len(tokens) && after.Healthy() {
			return automation.Outcome{Status: "succeeded", Result: fmt.Sprintf("verified: claimable buttons %d -> %d", len(tokens), n)}
		}
		select {
		case <-verifyCtx.Done():
			return automation.Outcome{Status: "unknown", Result: "click submitted; claimable button did not disappear"}
		case <-time.After(250 * time.Millisecond):
		}
	}
}

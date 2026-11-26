package cuacallback

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"github.com/amzyang/larkim/automation"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

func quotaEvent() automation.Event {
	return automation.Event{Version: 1, JobID: 7, RuleID: "quota", Text: "any card text",
		Message: store.Message{MessageID: "om_quota", ChatID: "oc_quiet", SenderID: "ou_bot", MsgType: "interactive", CreateMs: 100, MessagePosition: 200, RenderedAt: 101}}
}

func TestParseQuota_ChecksScopeButNotCardText(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*automation.Event)
	}{
		{"wrong chat", func(e *automation.Event) { e.Message.ChatID = "oc_elsewhere" }},
		{"wrong sender", func(e *automation.Event) { e.Message.SenderID = "ou_elsewhere" }},
		{"not a card", func(e *automation.Event) { e.Message.MsgType = "text" }},
		{"missing position", func(e *automation.Event) { e.Message.MessagePosition = 0 }},
		{"recalled", func(e *automation.Event) { e.Message.Deleted = true }},
		{"unrendered", func(e *automation.Event) { e.Message.RenderedAt = 0 }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := quotaEvent()
			tc.change(&e)
			_, err := ParseQuota(e, "oc_quiet", "ou_bot")
			require.Error(t, err)
		})
	}
	q, err := ParseQuota(quotaEvent(), "oc_quiet", "ou_bot")
	require.NoError(t, err)
	require.Contains(t, q.URL, "position=200")
}

// chatState lays every row flat under the chat web area, as Lingxi's AX tree
// does: cards have no per-message container.
func chatState(buttons ...string) State {
	rows := []Element{{Index: 1, Role: "AXWebArea", Label: "messenger-chat"}, {Index: 2, Parent: 1, Role: "AXStaticText", Label: "🟠 AI Coding 总额度已用 87% · 可申请加油包"}}
	for i, label := range buttons {
		rows = append(rows, Element{Index: 10 + i, Parent: 1, Role: "AXButton", Label: label, Token: fmt.Sprintf("s1:%d", 10+i), Enabled: true, Actions: []string{"AXPress"}})
	}
	return State{Elements: rows}
}

func TestClaimable_DecidesByButtonLabelOnly(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name    string
		state   State
		tokens  []string
		comment string
	}{
		{"first claim", chatState("申请加油包（+50%）"), []string{"s1:10"}, ""},
		{"repeat claim", chatState("再次申请加油包（+50%）"), []string{"s1:10"}, ""},
		{"not eligible", chatState("未达加油条件（需合计达 80%）"), nil, ""},
		{"no button", chatState(), nil, ""},
		{"chat header", chatState("AI Coding 额度提醒，加油包申请"), nil, ""},
		{"any claimable wins", chatState("未达加油条件（需合计达 80%）", "申请加油包（+50%）", "再次申请加油包（+50%）"), []string{"s1:11", "s1:12"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			require.Equal(t, tc.tokens, claimable(tc.state))
		})
	}
	s := chatState("申请加油包（+50%）")
	s.Elements[2].Enabled = false
	require.Empty(t, claimable(s))
	s = chatState("申请加油包（+50%）")
	s.Elements[2].Actions = nil
	require.Empty(t, claimable(s))
	s = chatState("申请加油包（+50%）")
	s.Elements[2].Parent = 99
	require.Empty(t, claimable(s), "buttons outside the chat web area never count")
}

type fakeDriver struct {
	states   []State
	calls    []string
	clickErr error
}

func (d *fakeDriver) Open(context.Context, string) (Window, error) {
	d.calls = append(d.calls, "open")
	return Window{PID: 1, ID: 2}, nil
}
func (d *fakeDriver) State(context.Context, Window) (State, error) {
	d.calls = append(d.calls, "state")
	if len(d.states) == 0 {
		return State{}, errors.New("no snapshot")
	}
	s := d.states[0]
	d.states = d.states[1:]
	return s, nil
}
func (d *fakeDriver) Click(_ context.Context, _ Window, token string) error {
	d.calls = append(d.calls, "click:"+token)
	return d.clickErr
}

func TestClaim_ClicksOneClaimableButtonAndVerifiesItIsGone(t *testing.T) {
	t.Parallel()
	q, err := ParseQuota(quotaEvent(), "oc_quiet", "ou_bot")
	require.NoError(t, err)
	claim, done := chatState("申请加油包（+50%）"), chatState("未达加油条件（需合计达 80%）")
	degraded := chatState()
	degraded.Degraded = true
	for _, tc := range []struct {
		name, status, prefix string
		states               []State
		clickErr             error
		clicks               int
	}{
		{"success", "succeeded", "verified", []State{claim, done}, nil, 1},
		{"delayed success", "succeeded", "verified", []State{claim, claim, done}, nil, 1},
		{"two claimable, one consumed", "succeeded", "verified", []State{chatState("申请加油包（+50%）", "再次申请加油包（+50%）"), chatState("再次申请加油包（+50%）")}, nil, 1},
		{"only ineligible", "failed", "skipped", []State{done}, nil, 0},
		{"no button", "failed", "skipped", []State{chatState()}, nil, 0},
		{"unreadable", "failed", "", []State{degraded}, nil, 0},
		{"ambiguous click", "unknown", "", []State{claim}, fmt.Errorf("connection lost"), 1},
		{"button stays", "unknown", "", []State{claim, claim}, nil, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDriver{states: tc.states, clickErr: tc.clickErr}
			out := (&Claim{Driver: d}).Run(t.Context(), q, false)
			require.Equal(t, tc.status, out.Status, out.Result)
			if tc.prefix != "" {
				require.Contains(t, out.Result, tc.prefix)
			}
			clicks := 0
			for _, c := range d.calls {
				if c == "click:s1:10" {
					clicks++
				}
			}
			require.Equal(t, tc.clicks, clicks)
		})
	}
}

func TestClaim_CheckNeverClicks(t *testing.T) {
	t.Parallel()
	q, err := ParseQuota(quotaEvent(), "oc_quiet", "ou_bot")
	require.NoError(t, err)
	d := &fakeDriver{states: []State{chatState("申请加油包（+50%）")}}
	out := (&Claim{Driver: d}).Run(t.Context(), q, true)
	require.Equal(t, "failed", out.Status)
	require.Contains(t, out.Result, "check passed")
	require.Equal(t, []string{"open", "state"}, d.calls)
}

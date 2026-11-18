package cuacallback

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/amzyang/larkim/automation"
	"github.com/amzyang/larkim/store"
	"github.com/stretchr/testify/require"
)

const quotaText = `🟠 AI Coding 总额度已用 90% · 可申请加油包
账号：linlan
当前时段：12:00-17:00（17:00 自动重置）
主力额度合计（Claude/GPT）：$270.35 / $300.00（90%） · 本时段已加油 1 次
各策略明细：
Claude：$67.21 / $75.00（90%）
GPT：$203.14 / $225.00（90%）
[再次申请加油包（+50%）]
已满足加油条件（Claude/GPT 合计已用 ≥90%）`

func quotaEvent() automation.Event {
	at := time.Date(2026, 11, 18, 14, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
	return automation.Event{Version: 1, JobID: 7, RuleID: "quota", Text: quotaText,
		Message: store.Message{MessageID: "om_quota", ChatID: "oc_quiet", SenderID: "ou_bot", MsgType: "interactive", CreateMs: at.UnixMilli(), MessagePosition: 200, RenderedAt: at.UnixMilli()}}
}

func quotaNow() time.Time {
	return time.Date(2026, 11, 18, 15, 0, 0, 0, time.FixedZone("UTC+8", 8*60*60))
}

func TestParseQuota_RejectsWrongScopeExpiredOrAmbiguousInput(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		name   string
		change func(*automation.Event)
		now    time.Time
	}{
		{"expired", func(e *automation.Event) {}, quotaNow().Add(2 * time.Hour)},
		{"old date", func(e *automation.Event) { e.Message.CreateMs -= int64(24 * time.Hour / time.Millisecond) }, quotaNow()},
		{"wrong chat", func(e *automation.Event) { e.Message.ChatID = "oc_elsewhere" }, quotaNow()},
		{"wrong sender", func(e *automation.Event) { e.Message.SenderID = "ou_elsewhere" }, quotaNow()},
		{"missing position", func(e *automation.Event) { e.Message.MessagePosition = 0 }, quotaNow()},
		{"recalled", func(e *automation.Event) { e.Message.Deleted = true }, quotaNow()},
		{"two cards", func(e *automation.Event) { e.Text += "\n" + quotaText }, quotaNow()},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := quotaEvent()
			tc.change(&e)
			_, err := ParseQuota(e, "oc_quiet", "ou_bot", tc.now)
			require.Error(t, err)
		})
	}
	q, err := ParseQuota(quotaEvent(), "oc_quiet", "ou_bot", quotaNow())
	require.NoError(t, err)
	require.Equal(t, "linlan", q.Account)
	require.Equal(t, 2, q.NextCount)
}

func cardState(text, token string) State {
	rows := []Element{{Index: 1, Role: "AXWebArea", Label: "messenger-chat"}}
	for i, line := range splitLines(text) {
		rows = append(rows, Element{Index: i + 2, Parent: 1, Role: "AXStaticText", Label: line})
	}
	rows = append(rows, Element{Index: 100, Parent: 1, Role: "AXButton", Label: claimButton, Token: token, Enabled: true, Actions: []string{"AXPress"}})
	return State{Elements: rows}
}

func successState() State {
	return cardState(`✅ AI Coding 加油包已生效（本时段第 2 次）
账号：linlan
当前时段：12:00-17:00（17:00 自动重置）
主力额度合计（Claude/GPT）：$282.97 / $400.00（71%）
Claude：$67.21 / $100.00（67%）
GPT：$215.76 / $300.00（72%）`, "")
}

func TestSelectQuota_RequiresCompleteUniqueCardAndExactAmounts(t *testing.T) {
	t.Parallel()
	q, err := ParseQuota(quotaEvent(), "oc_quiet", "ou_bot", quotaNow())
	require.NoError(t, err)
	s := cardState(quotaText, "s00000001:100")
	token, err := q.Select(s)
	require.NoError(t, err)
	require.Equal(t, "s00000001:100", token)
	s.Truncated = true
	_, err = q.Select(s)
	require.Error(t, err)
	s.Truncated = false
	s.Degraded = true
	_, err = q.Select(s)
	require.Error(t, err)
	s = cardState(quotaText+"\n"+quotaText, "s00000001:100")
	_, err = q.Select(s)
	require.Error(t, err)
	s = cardState(quotaText, "s00000001:100")
	s.Elements[1].Label = "unrelated card"
	_, err = q.Select(s)
	require.Error(t, err)
	s = cardState(quotaText, "s00000001:100")
	s.Elements = append(s.Elements, Element{Index: 101, Parent: 1, Role: "AXButton", Label: claimButton, Token: "s00000001:101", Enabled: true, Actions: []string{"AXPress"}})
	_, err = q.Select(s)
	require.Error(t, err)
	s = successState()
	for i := range s.Elements {
		s.Elements[i].Label = strings.ReplaceAll(s.Elements[i].Label, "linlan", "linlan2")
	}
	require.False(t, q.Succeeded(s))
	require.True(t, q.Succeeded(successState()))
	s = successState()
	s.Truncated = true
	require.False(t, q.Succeeded(s))
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

func TestClaim_ClicksOnceAndRequiresChangedSuccess(t *testing.T) {
	t.Parallel()
	q, err := ParseQuota(quotaEvent(), "oc_quiet", "ou_bot", quotaNow())
	require.NoError(t, err)
	for _, tc := range []struct {
		name, status string
		states       []State
		clickErr     error
	}{
		{"success", "succeeded", []State{cardState(quotaText, "s00000001:100"), successState()}, nil},
		{"delayed success", "succeeded", []State{cardState(quotaText, "s00000001:100"), cardState(quotaText, "s00000002:100"), successState()}, nil},
		{"already done", "failed", []State{successState()}, nil},
		{"ambiguous click", "unknown", []State{cardState(quotaText, "s00000001:100")}, fmt.Errorf("connection lost")},
		{"no postcondition", "unknown", []State{cardState(quotaText, "s00000001:100"), cardState(quotaText, "s00000002:100")}, nil},
	} {
		t.Run(tc.name, func(t *testing.T) {
			d := &fakeDriver{states: tc.states, clickErr: tc.clickErr}
			r := Claim{Driver: d, Now: quotaNow}
			out := r.Run(t.Context(), q, false)
			require.Equal(t, tc.status, out.Status)
			count := 0
			for _, c := range d.calls {
				if c == "click:s00000001:100" {
					count++
				}
			}
			require.LessOrEqual(t, count, 1)
		})
	}
}

func TestClaim_RefusesPreexistingSuccessAlongsideClaim(t *testing.T) {
	t.Parallel()
	q, err := ParseQuota(quotaEvent(), "oc_quiet", "ou_bot", quotaNow())
	require.NoError(t, err)
	s := cardState(quotaText, "s00000001:100")
	for _, e := range successState().Elements[1:] {
		e.Index += 200
		s.Elements = append(s.Elements, e)
	}
	d := &fakeDriver{states: []State{s}}
	out := (&Claim{Driver: d, Now: quotaNow}).Run(t.Context(), q, false)
	require.Equal(t, "failed", out.Status)
	require.Equal(t, []string{"open", "state"}, d.calls)
}

func TestQuota_AcceptsAboveThresholdTitle(t *testing.T) {
	t.Parallel()
	e := quotaEvent()
	e.Text = strings.Replace(e.Text, "总额度已用 90%", "总额度已用 91%", 1)
	q, err := ParseQuota(e, "oc_quiet", "ou_bot", quotaNow())
	require.NoError(t, err)
	_, err = q.Select(cardState(e.Text, "s00000001:100"))
	require.NoError(t, err)
	e.Text = strings.Replace(e.Text, "总额度已用 91%", "总额度已用 89%", 1)
	_, err = ParseQuota(e, "oc_quiet", "ou_bot", quotaNow())
	require.Error(t, err)
}

func TestClaim_CheckNeverClicks(t *testing.T) {
	t.Parallel()
	q, err := ParseQuota(quotaEvent(), "oc_quiet", "ou_bot", quotaNow())
	require.NoError(t, err)
	d := &fakeDriver{states: []State{cardState(quotaText, "s00000001:100")}}
	out := (&Claim{Driver: d, Now: quotaNow}).Run(t.Context(), q, true)
	require.Equal(t, "failed", out.Status)
	require.Contains(t, out.Result, "check passed")
	require.Equal(t, []string{"open", "state"}, d.calls)
}

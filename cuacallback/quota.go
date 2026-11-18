// Package cuacallback implements the account-quota card callback through Cua Driver.
package cuacallback

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/automation"
)

const claimButton = "再次申请加油包（+50%）"

var eligibleTitleRE = regexp.MustCompile(`🟠AICoding总额度已用(\d+)%·可申请加油包`)

var (
	accountRE = regexp.MustCompile(`账号[：:]([^当前]+)当前时段`)
	periodRE  = regexp.MustCompile(`当前时段[：:](\d{2}):(\d{2})-(\d{2}):(\d{2})`)
	countRE   = regexp.MustCompile(`本时段已加油(\d+)次`)
	amountRE  = regexp.MustCompile(`\$(\d+)\.(\d{2})/\$(\d+)\.(\d{2})`)
	quotaZone = time.FixedZone("UTC+8", 8*60*60)
)

type Quota struct {
	Account               string
	NextCount             int
	URL                   string
	Expires               time.Time
	Period                string
	anchors               []string
	claudeLimit, gptLimit int64
}

func compact(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsSpace(r) || strings.ContainsRune("*`[]", r) {
			return -1
		}
		return r
	}, s)
}

func splitLines(s string) []string { return strings.Split(s, "\n") }

// ParseQuota validates the callback input before any desktop interaction. The
// card's clock belongs to the service's China time, not the host's current zone.
func ParseQuota(e automation.Event, chatID, senderID string, now time.Time) (Quota, error) {
	m := e.Message
	if e.Version != 1 || e.JobID <= 0 || e.RuleID == "" || chatID == "" || senderID == "" || m.ChatID != chatID || m.SenderID != senderID || m.MsgType != "interactive" || m.Deleted || m.RenderedAt == 0 || !strings.HasPrefix(m.MessageID, "om_") || m.MessagePosition <= 0 {
		return Quota{}, errors.New("event is not a live positioned quota card in the configured chat and sender scope")
	}
	s := compact(e.Text)
	titles := eligibleTitleRE.FindAllStringSubmatch(s, -1)
	if len(titles) != 1 {
		return Quota{}, errors.New("expected one quota title")
	}
	percent, err := strconv.Atoi(titles[0][1])
	if err != nil || percent < 90 {
		return Quota{}, errors.New("quota threshold not reached")
	}
	if strings.Count(s, claimButton) != 1 || !strings.Contains(s, "已满足加油条件") {
		return Quota{}, errors.New("event does not contain one eligible repeat-claim card")
	}
	a, p, n := accountRE.FindStringSubmatch(s), periodRE.FindStringSubmatch(s), countRE.FindStringSubmatch(s)
	amounts := amountRE.FindAllStringSubmatch(s, -1)
	if len(a) != 2 || len(p) != 5 || len(n) != 2 || len(amounts) != 3 {
		return Quota{}, errors.New("unsupported quota card fields")
	}
	count, err := strconv.Atoi(n[1])
	if err != nil || count < 1 || count > 1000 {
		return Quota{}, errors.New("invalid refill count")
	}
	clock := make([]int, 4)
	for i := range 4 {
		clock[i], _ = strconv.Atoi(p[i+1])
	}
	if clock[0] > 23 || clock[2] > 23 || clock[1] > 59 || clock[3] > 59 {
		return Quota{}, errors.New("invalid quota period")
	}
	created := time.UnixMilli(m.CreateMs).In(quotaZone)
	start := time.Date(created.Year(), created.Month(), created.Day(), clock[0], clock[1], 0, 0, quotaZone)
	end := time.Date(created.Year(), created.Month(), created.Day(), clock[2], clock[3], 0, 0, quotaZone)
	if !end.After(start) {
		return Quota{}, errors.New("unsupported overnight quota period")
	}
	if created.Before(start) || !created.Before(end) || now.Before(created) || !now.Before(end) {
		return Quota{}, errors.New("quota card is outside its current validity period")
	}
	limits := make([]int64, 3)
	for i, amount := range amounts {
		dollars, err := strconv.ParseInt(amount[3], 10, 64)
		if err != nil || dollars > 100000000 {
			return Quota{}, errors.New("invalid quota amount")
		}
		cents, _ := strconv.ParseInt(amount[4], 10, 64)
		limits[i] = dollars*100 + cents
	}
	if limits[0] != limits[1]+limits[2] || limits[1] <= 0 || limits[2] <= 0 {
		return Quota{}, errors.New("inconsistent quota limits")
	}
	// Each refill adds half of the base allowance, not half of the current limit.
	divisor := int64(count + 2)
	if limits[1]%divisor != 0 || limits[2]%divisor != 0 {
		return Quota{}, errors.New("non-integral base quota increment")
	}
	q := Quota{Account: a[1], NextCount: count + 1, URL: applink.ChatLink(m.ChatID, m.MessageID, m.MessagePosition), Expires: end, Period: p[0],
		claudeLimit: limits[1] + limits[1]/divisor, gptLimit: limits[2] + limits[2]/divisor}
	q.anchors = []string{"账号：" + a[1] + "当前时段", p[0], "主力额度合计（Claude/GPT）：" + amounts[0][0], n[0], "Claude：" + amounts[1][0], "GPT：" + amounts[2][0]}
	return q, nil
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

type cardBlock struct {
	text    string
	buttons []Element
}

func blocks(s State) []cardBlock {
	if s.Truncated || s.Degraded || s.DegradedReason != "" {
		return nil
	}
	parents := map[int]Element{}
	for _, e := range s.Elements {
		parents[e.Index] = e
	}
	var out []cardBlock
	areaID := -1
	for _, e := range s.Elements {
		inside := false
		parent := e.Parent
		for range len(s.Elements) {
			p, ok := parents[parent]
			if !ok {
				break
			}
			if p.Role == "AXWebArea" {
				inside = p.Label == "messenger-chat"
				if inside && areaID != p.Index {
					areaID = p.Index
					out = append(out, cardBlock{})
				}
				break
			}
			if p.Parent == parent {
				break
			}
			parent = p.Parent
		}
		if !inside {
			continue
		}
		label := e.Label
		if label == "" {
			label = e.Value
		}
		if e.Role == "AXStaticText" && (strings.Contains(label, "AI Coding") || strings.Contains(label, "额度预警") || strings.Contains(label, "额度已用完")) && (strings.HasPrefix(label, "🟠") || strings.HasPrefix(label, "✅") || strings.HasPrefix(label, "🟡")) {
			out = append(out, cardBlock{})
		}
		if len(out) == 0 {
			continue
		}
		b := &out[len(out)-1]
		if e.Role == "AXStaticText" {
			b.text += compact(label)
		}
		if e.Role == "AXButton" {
			b.buttons = append(b.buttons, e)
		}
	}
	return out
}

func (q Quota) Select(s State) (string, error) {
	var tokens []string
	cards := 0
	for _, b := range blocks(s) {
		if !eligibleTitleRE.MatchString(b.text) {
			continue
		}
		matches := true
		for _, a := range q.anchors {
			if !strings.Contains(b.text, a) {
				matches = false
			}
		}
		if !matches {
			continue
		}
		cards++
		for _, button := range b.buttons {
			if button.Label == claimButton && button.Enabled && button.Token != "" && slices.Contains(button.Actions, "AXPress") {
				tokens = append(tokens, button.Token)
			}
		}
	}
	if cards != 1 || len(tokens) != 1 {
		return "", fmt.Errorf("expected one matching quota card button, found %d", len(tokens))
	}
	return tokens[0], nil
}

func money(cents int64) string { return fmt.Sprintf("%d.%02d", cents/100, cents%100) }

func (q Quota) Succeeded(s State) bool {
	title := fmt.Sprintf("✅AICoding加油包已生效（本时段第%d次）", q.NextCount)
	matches := 0
	for _, b := range blocks(s) {
		if !strings.Contains(b.text, title) || !strings.Contains(b.text, "账号："+q.Account+"当前时段") || !strings.Contains(b.text, q.Period) {
			continue
		}
		amounts := amountRE.FindAllStringSubmatch(b.text, -1)
		if len(amounts) != 3 {
			continue
		}
		limits := []int64{q.claudeLimit + q.gptLimit, q.claudeLimit, q.gptLimit}
		valid := true
		for i, a := range amounts {
			if a[3]+"."+a[4] != money(limits[i]) {
				valid = false
			}
		}
		if valid {
			matches++
		}
	}
	return matches == 1
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
	Now    func() time.Time
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
	if q.Succeeded(before) {
		return failed(errors.New("matching success card already exists; no claim submitted"))
	}
	token, err := q.Select(before)
	if err != nil {
		return failed(err)
	}
	if !c.Now().Before(q.Expires) {
		return failed(errors.New("quota period expired before click"))
	}
	if check {
		return failed(errors.New("check passed: unique eligible card found; no claim submitted"))
	}
	// Once the request leaves this process, transport failure cannot prove that
	// the button's handler did not run. Never submit a second click.
	if err := c.Driver.Click(ctx, w, token); err != nil {
		return automation.Outcome{Status: "unknown", Result: err.Error()}
	}
	verifyCtx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	for {
		after, err := c.Driver.State(verifyCtx, w)
		if err != nil {
			return automation.Outcome{Status: "unknown", Result: err.Error()}
		}
		if q.Succeeded(after) {
			return automation.Outcome{Status: "succeeded", Result: fmt.Sprintf("verified refill %d: total=$%s Claude=$%s GPT=$%s", q.NextCount, money(q.claudeLimit+q.gptLimit), money(q.claudeLimit), money(q.gptLimit))}
		}
		select {
		case <-verifyCtx.Done():
			return automation.Outcome{Status: "unknown", Result: "click submitted; matching success card with increased limits not verified"}
		case <-time.After(250 * time.Millisecond):
		}
	}
}

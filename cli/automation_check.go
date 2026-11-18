package cli

import (
	"context"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/amzyang/larkim/automation"
	"github.com/amzyang/larkim/cuacallback"
	"github.com/amzyang/larkim/store"
)

// quotaChecker never runs the rule's executable: inspection has one built-in,
// non-submitting path even when a configured callback is an arbitrary program.
type quotaChecker struct {
	Store      *store.Store
	Executable string
	Now        func() time.Time
	Inspect    func(context.Context, cuacallback.Quota) automation.Outcome
}

func (c quotaChecker) Check(ctx context.Context, r automation.Rule) (string, error) {
	chat, sender, ok := automation.QuotaScope(r)
	if !ok || r.Callback.Executable != c.Executable {
		return "", fmt.Errorf("only the built-in quota preset supports Check")
	}
	now := c.Now()
	messages, err := c.Store.ListMessages(ctx, store.MessageQuery{ChatID: chat, SenderID: sender, MsgType: "interactive", SinceMs: now.Add(-24 * time.Hour).UnixMilli(), Desc: true, Limit: 100})
	if err != nil {
		return "", err
	}
	for _, m := range messages {
		text, match := r.Criteria.Match(m)
		if !match {
			continue
		}
		q, err := cuacallback.ParseQuota(automation.Event{Version: 1, JobID: 1, RuleID: r.ID, Message: m, Text: text}, chat, sender, now)
		if err != nil {
			continue
		}
		result := c.Inspect(ctx, q)
		if result.Status != "failed" || !strings.HasPrefix(result.Result, "check passed:") {
			return "", fmt.Errorf("quota check: %s", result.Result)
		}
		return result.Result, nil
	}
	return "", fmt.Errorf("no current eligible quota card in the latest 100 synced messages; sync the chat and try again")
}

func (a *App) checkQuota(st *store.Store) func(context.Context, automation.Rule) (string, error) {
	return func(ctx context.Context, r automation.Rule) (string, error) {
		executable, err := os.Executable()
		if err != nil {
			return "", err
		}
		checker := quotaChecker{Store: st, Executable: executable, Now: time.Now, Inspect: func(ctx context.Context, q cuacallback.Quota) automation.Outcome {
			return a.claimQuota(ctx, q, true, "larkim-quota-check", a.logger().With("operation", "quota-check"))
		}}
		return checker.Check(ctx, r)
	}
}

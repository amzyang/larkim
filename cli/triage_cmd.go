package cli

import (
	"context"
	"fmt"
	"slices"
	"strconv"
	"time"

	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/store"
	"github.com/spf13/cobra"
)

// triageLevels are the verdicts triage writes, the values --level completes.
var triageLevels = []string{"P0", "P1", "drop"}

// triageCmd shows what the banner side judged and why. The Lark client has no
// counterpart — it notifies on every unmuted message — so this names its own
// command rather than borrowing one of the client's words.
func (a *App) triageCmd() *cobra.Command {
	triage := &cobra.Command{
		Use:   "triage",
		Short: "Verdicts on fresh arrivals: which raised a banner, and why",
	}
	var chat, level string
	var limit int
	list := &cobra.Command{
		Use:   "list [--chat <oc_ | name>] [--level P0|P1|drop]",
		Short: "List verdicts, newest first",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
			if level != "" && !slices.Contains(triageLevels, level) {
				return &usageError{fmt.Errorf("--level must be one of P0, P1, drop")}
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			if chat != "" {
				if chat, err = resolveChatLocal(ctx, st, chat); err != nil {
					return err
				}
			}
			rows, err := st.ListTriage(ctx, store.TriageQuery{ChatID: chat, Level: level, Limit: limit})
			if err != nil {
				return err
			}
			for i := range rows {
				rows[i].Content, _ = card.MessageText(rows[i].ContentRaw, rows[i].Content, rows[i].RenderedAt > 0)
			}
			if a.json() {
				return a.printJSON(rows)
			}
			out := make([][]string, 0, len(rows))
			for _, r := range rows {
				p := ""
				if r.JevP != nil {
					p = strconv.FormatFloat(*r.JevP, 'f', 2, 64)
				}
				out = append(out, []string{time.UnixMilli(r.JudgedMs).Local().Format("01-02 15:04:05"), r.Level, r.Reason, p,
					r.ChatName, r.SenderName, firstLine(r.Content)})
			}
			table(a.Out, []string{"judged", "level", "reason", "jev", "chat", "sender", "message"}, out)
			return nil
		},
	}
	list.Flags().StringVar(&chat, "chat", "", "only this chat: id (oc_…) or exact name")
	list.Flags().StringVar(&level, "level", "", "only this verdict: P0, P1 or drop")
	list.Flags().IntVar(&limit, "limit", 100, "at most this many rows")
	mustWire(list.RegisterFlagCompletionFunc("chat", a.completeChatRef))
	mustWire(list.RegisterFlagCompletionFunc("level", cobra.FixedCompletions(triageLevels, cobra.ShellCompDirectiveNoFileComp)))
	triage.AddCommand(list)
	completeNoFileDefault(triage)
	return triage
}

package cli

import (
	"context"
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

// candidatesCmd lists the drafts triage wrote for urgent messages, replies and
// reactions both, the ones the TUI offers.
func (a *App) candidatesCmd() *cobra.Command {
	candidates := &cobra.Command{
		Use:   "candidates",
		Short: "Reply and reaction drafts written for urgent messages, offered in the TUI",
	}

	var chat string
	list := &cobra.Command{
		Use:   "list [--chat <oc_ | name>]",
		Short: "List the pending drafts",
		Args:  cobra.NoArgs,
		RunE: func(_ *cobra.Command, _ []string) error {
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
			// Answered is a TUI distinction; the listing reads without a self
			// id and leaves it unset.
			rows, err := st.ChatCandidates(ctx, chat, "")
			if err != nil {
				return err
			}
			if a.json() {
				return a.printJSON(rows)
			}
			out := make([][]string, 0, len(rows))
			for i, c := range rows {
				draft := firstLine(c.Text)
				if c.Reaction != "" {
					draft = "[" + reactName(c.Reaction) + "]"
				}
				out = append(out, []string{circled(i + 1), c.Mid, c.ChatID, c.Format, draft})
			}
			table(a.Out, []string{"#", "message_id", "chat_id", "format", "draft"}, out)
			return nil
		},
	}
	list.Flags().StringVar(&chat, "chat", "", "only this chat: id (oc_…) or exact name")
	mustWire(list.RegisterFlagCompletionFunc("chat", a.completeChatRef))

	candidates.AddCommand(list)
	completeNoFileDefault(candidates)
	return candidates
}

// circled is the draft's place among its siblings, numbered the way the TUI
// numbers them.
func circled(n int) string {
	if n >= 1 && n <= 20 {
		return string(rune('①' + n - 1))
	}
	return fmt.Sprint(n)
}

// firstLine stands a draft up in one table row: everything before its first
// newline, elided when there is more.
func firstLine(s string) string {
	if before, _, found := strings.Cut(s, "\n"); found {
		return before + " …"
	}
	return s
}

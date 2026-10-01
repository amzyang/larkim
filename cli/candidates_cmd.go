package cli

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"time"

	"github.com/spf13/cobra"
)

// candidatesCmd mirrors lark-watch's pending reply drafts into draft_candidates.
// lark-watch shells put when it sends a confirmation card and clear when the
// pending resolves (card click, banner send, quick reply, TTL sweep); the TUI
// reads the same rows to offer the drafts at the composer.
func (a *App) candidatesCmd() *cobra.Command {
	candidates := &cobra.Command{
		Use:   "candidates",
		Short: "Reply drafts lark-watch is holding, mirrored for the TUI composer",
	}

	var drafts []string
	var format string
	put := &cobra.Command{
		Use:   "put <message_id> --draft <text> [--draft <text> ...]",
		Short: "Mirror the reply drafts held for a message",
		Long: "Writes one draft_candidates row for the message, so a TUI with that chat open can\n" +
			"offer the drafts at the composer. The message's chat is looked up here; a message the\n" +
			"store has never seen has no chat to scope the row to and is refused.",
		Args:              cobra.ExactArgs(1),
		ValidArgsFunction: a.completeMessageID,
		RunE: func(_ *cobra.Command, args []string) error {
			drafts = trimAll(drafts)
			if len(drafts) == 0 {
				return fmt.Errorf("pass at least one --draft")
			}
			if format != "text" && format != "markdown" {
				return fmt.Errorf("--format must be text or markdown")
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			m, err := st.GetMessage(ctx, args[0])
			if err != nil {
				return err
			}
			if err := st.PutCandidates(ctx, m.MessageID, m.ChatID, drafts, format, time.Now().UnixMilli()); err != nil {
				return err
			}
			if a.json() {
				return a.printJSON(map[string]any{
					"message_id": m.MessageID, "chat_id": m.ChatID, "drafts": len(drafts), "format": format,
				})
			}
			fmt.Fprintf(a.Out, "mirrored %d draft(s) for %s in chat %s\n", len(drafts), m.MessageID, m.ChatID)
			return nil
		},
	}
	put.Flags().StringArrayVar(&drafts, "draft", nil, "one reply draft, repeatable (1–3)")
	put.Flags().StringVar(&format, "format", "text", "how the drafts read on send: text or markdown")

	clear := &cobra.Command{
		Use:               "clear <message_id> ...",
		Short:             "Drop the mirrored drafts of messages whose pending resolved",
		Args:              cobra.MinimumNArgs(1),
		ValidArgsFunction: a.completeMessageID,
		RunE: func(_ *cobra.Command, args []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			for _, mid := range args {
				if err := st.ClearCandidate(ctx, mid); err != nil {
					return err
				}
			}
			if a.json() {
				return a.printJSON(map[string]any{"cleared": len(args)})
			}
			fmt.Fprintf(a.Out, "cleared %d message(s)\n", len(args))
			return nil
		},
	}

	var chat string
	list := &cobra.Command{
		Use:   "list [--chat <oc_ | name>]",
		Short: "List the mirrored drafts",
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
			// Replied is a TUI distinction; the listing reads without a self
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
				out = append(out, []string{circled(i + 1), c.Mid, c.ChatID, c.Format, firstLine(c.Text)})
			}
			table(a.Out, []string{"#", "message_id", "chat_id", "format", "draft"}, out)
			return nil
		},
	}
	list.Flags().StringVar(&chat, "chat", "", "only this chat: id (oc_…) or exact name")
	mustWire(list.RegisterFlagCompletionFunc("chat", a.completeChatRef))

	candidates.AddCommand(put, clear, list)
	completeNoFileDefault(candidates)
	return candidates
}

// trimAll drops empty drafts: a blank candidate is nothing to pick, and the
// mirror has no column to keep a hole in.
func trimAll(in []string) []string {
	return slices.DeleteFunc(in, func(s string) bool { return strings.TrimSpace(s) == "" })
}

// circled is the draft's place among its siblings, spelled the way the Feishu
// card spells it. lark-watch caps drafts at three; past ten the card keeps
// counting and so does this, in plain digits.
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

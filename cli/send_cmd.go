package cli

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/amzyang/larkim/larkcli"
	"github.com/amzyang/larkim/resolve"
	"github.com/amzyang/larkim/store"
	"github.com/google/uuid"
	"github.com/spf13/cobra"
)

func (a *App) sendCmd() *cobra.Command {
	var to, chat, text string
	cmd := &cobra.Command{
		Use:   "send --to <ou_|email> | --chat <oc_|name> --text <message>",
		Short: "Send a text message to a user or a chat",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if (to == "") == (chat == "") {
				return fmt.Errorf("pass exactly one of --to or --chat")
			}
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("--text is required")
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			client := a.client()
			r := &resolve.Resolver{Store: st, Client: client, Now: func() int64 { return time.Now().UnixMilli() }}
			var target larkcli.Target
			var label string
			if chat != "" {
				c, err := r.Chat(ctx, chat)
				if err != nil {
					return err
				}
				target.ChatID = c.ChatID
				label = c.Name
			} else {
				u, err := r.User(ctx, to)
				if err != nil {
					return err
				}
				target.UserID = u.OpenID
				label = u.Name
			}
			sent, err := client.SendText(ctx, target, text, uuid.NewString())
			if err != nil {
				return err
			}
			a.ingestSent(ctx, st, sent)
			return a.printSent(sent, label)
		},
	}
	cmd.Flags().StringVar(&to, "to", "", "recipient: open_id (ou_…), email or exact name")
	cmd.Flags().StringVar(&chat, "chat", "", "chat: id (oc_…) or exact name")
	cmd.Flags().StringVar(&text, "text", "", "plain text to send")
	return cmd
}

func (a *App) replyCmd() *cobra.Command {
	var text string
	var inThread bool
	cmd := &cobra.Command{
		Use:   "reply <message_id> --text <message>",
		Short: "Reply to a message, optionally inside its thread",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if strings.TrimSpace(text) == "" {
				return fmt.Errorf("--text is required")
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			client := a.client()
			sent, err := client.ReplyText(ctx, args[0], text, inThread, uuid.NewString())
			if err != nil {
				return err
			}
			a.ingestSent(ctx, st, sent)
			return a.printSent(sent, "")
		},
	}
	cmd.Flags().StringVar(&text, "text", "", "plain text to send")
	cmd.Flags().BoolVar(&inThread, "in-thread", false, "reply in the message's thread instead of the main chat")
	return cmd
}

// ingestSent stores the message we just sent so it is visible before the next
// daemon tick; failures are reported but do not fail the send.
func (a *App) ingestSent(ctx context.Context, st *store.Store, sent larkcli.SentMessage) {
	if err := a.syncer(st).IngestIDs(ctx, []string{sent.MessageID}); err != nil {
		fmt.Fprintln(a.Err, "larkim: sent, but could not store the message locally:", err)
	}
}

func (a *App) printSent(sent larkcli.SentMessage, label string) error {
	if a.json() {
		return a.printJSON(sent)
	}
	if label != "" {
		fmt.Fprintf(a.Out, "sent %s to %s (%s)\n", sent.MessageID, label, sent.ChatID)
	} else {
		fmt.Fprintf(a.Out, "sent %s in %s\n", sent.MessageID, sent.ChatID)
	}
	return nil
}

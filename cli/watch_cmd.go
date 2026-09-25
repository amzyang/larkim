package cli

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/amzyang/larkim/card"
	"github.com/amzyang/larkim/store"
	"github.com/spf13/cobra"
)

func (a *App) watchCmd() *cobra.Command {
	var chat string
	var every time.Duration
	cmd := &cobra.Command{
		Use:   "watch",
		Short: "Stream newly synced messages as they land in the database",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
			defer cancel()
			if chat != "" {
				chat, err = resolveChatLocal(ctx, st, chat)
				if err != nil {
					return err
				}
			}
			enc := json.NewEncoder(a.Out)
			enc.SetEscapeHTML(false)
			for msgs := range st.Watch(ctx, every, chat) {
				for _, m := range msgs {
					if a.json() {
						if err := enc.Encode(m); err != nil {
							return err
						}
						continue
					}
					fmt.Fprintf(a.Out, "%s %s %s %s: %s\n", fmtMs(m.CreateMs), m.ChatID, m.MessageID, senderLabel(m), oneLine(contentLabel(m), 120))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&chat, "chat", "", "only this chat (id or exact name)")
	cmd.Flags().DurationVar(&every, "every", 500*time.Millisecond, "poll interval")
	return cmd
}

func senderLabel(m store.Message) string {
	if m.SenderName != "" {
		return m.SenderName
	}
	return m.SenderID
}

func contentLabel(m store.Message) string {
	if c, ok := card.Parse(m.ContentRaw); ok {
		return c.Markdown()
	}
	if m.RenderedAt == 0 {
		return m.ContentRaw
	}
	return m.Content
}

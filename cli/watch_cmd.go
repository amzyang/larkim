package cli

import (
	"cmp"
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
					fmt.Fprintln(a.Out, watchLine(m))
				}
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&chat, "chat", "", "only this chat (id or exact name)")
	cmd.Flags().DurationVar(&every, "every", 500*time.Millisecond, "poll interval")
	mustWire(cmd.RegisterFlagCompletionFunc("chat", a.completeChatRef))
	return cmd
}

// watchLine is one streamed message, a single line however the fields it names
// arrived: the whole line goes through inline, so neither a sender's name nor
// their text can break the stream apart or reach the terminal as an escape.
func watchLine(m store.Message) string {
	return inline(fmt.Sprintf("%s %s %s %s: %s",
		fmtMs(m.CreateMs), m.ChatID, m.MessageID, senderLabel(m), oneLine(contentLabel(m), 120)))
}

func senderLabel(m store.Message) string { return cmp.Or(m.SenderName, m.SenderID) }

func contentLabel(m store.Message) string {
	text, _ := card.MessageText(m.ContentRaw, m.Content, m.RenderedAt > 0)
	return text
}

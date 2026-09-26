package cli

import (
	"context"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/spf13/cobra"
)

// readAllResult is what one pass settled, on both sides of the line.
type readAllResult struct {
	// Messages is what was taken as read locally, thread replies included.
	Messages int64 `json:"messages"`
	// Chats is how many the desktop client was walked onto: the chats whose
	// main message flow still had something Feishu reports unseen.
	Chats int `json:"chats"`
	// Failed is how many of those macOS refused to open, which leaves the
	// client's own dot up while larkim's badge is already down.
	Failed int `json:"failed"`
}

func (a *App) readAllCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "read-all",
		Short: "Take every chat as read and clear the Feishu client's red dots",
		Long: "Feishu has no mark-read call. The local half is a write; the client's own dots come down by\n" +
			"walking it onto each chat over a lark:// applink, in the background, one chat at a time.\n" +
			"Thread replies are taken as read locally but leave no dot to clear: the chat the applink\n" +
			"opens does not render them, so the client never answers for one.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			// Read before the write: the write is what erases the evidence of
			// which chats the client still has a dot for.
			chats, err := st.ChatsWithUnread(ctx)
			if err != nil {
				return err
			}
			if dryRun {
				return a.reportReadAll(cmd, readAllResult{Chats: len(chats)}, true)
			}
			n, err := st.MarkAllRead(ctx, time.Now().UnixMilli())
			if err != nil {
				return err
			}
			res := readAllResult{Messages: n, Chats: len(chats)}
			for i, c := range chats {
				if i > 0 {
					// The client renders the chat it was walked onto before
					// it sends a receipt, so a walk faster than it draws
					// loses the chats it was hurried through.
					time.Sleep(applink.Pace)
				}
				if err := a.open([]string{applink.ChatLink(c.ChatID, c.Position)}, true); err != nil {
					// Best effort behind a durable write: a refusal costs one
					// red dot, not the pass.
					a.logger().Warn("clear feishu badge", "chat_id", c.ChatID, "err", err)
					res.Failed++
				}
			}
			return a.reportReadAll(cmd, res, false)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "count the chats that would be walked, and write nothing")
	return cmd
}

// open is the one hand-over to the desktop this process makes.
func (a *App) open(targets []string, background bool) error {
	if a.openURL != nil {
		return a.openURL(targets, background)
	}
	return applink.Open(a.logger(), targets, background)
}

func (a *App) reportReadAll(cmd *cobra.Command, res readAllResult, dry bool) error {
	if a.json() {
		return a.printJSON(res)
	}
	if dry {
		cmd.Printf("%d chats waiting\n", res.Chats)
		return nil
	}
	cmd.Printf("%d messages read, %d chats cleared in Feishu\n", res.Messages, res.Chats-res.Failed)
	if res.Failed > 0 {
		cmd.Printf("%d chats kept their red dot: open refused them\n", res.Failed)
	}
	return nil
}

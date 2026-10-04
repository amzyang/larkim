package cli

import (
	"context"
	"time"

	"github.com/amzyang/larkim/larkweb"
	"github.com/amzyang/larkim/markread"
	"github.com/spf13/cobra"
)

// readAllResult is what one pass settled, on both sides of the line.
type readAllResult struct {
	// Messages is what was taken as read locally, thread replies included.
	Messages int64 `json:"messages"`
	// Chats is the chats whose main message flow Feishu still reports unseen.
	Chats int `json:"chats"`
	// Failed is how many of those the web client refused or could not match,
	// which leaves the dot up while larkim's badge is down. They stay in the
	// set so the next pass retries them.
	Failed int `json:"failed"`
}

func (a *App) readAllCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "read-all",
		Short: "Take every chat as read and clear the Feishu client's red dots",
		Long: "The local half is a write. The client's own dots come down by posting each chat's read watermark to the web client with the Feishu login of mark_read.browser.\n" +
			"A chat leaves the set when Feishu reports it read, not when the attempt is made, so a chat\n" +
			"left unread is tried again by the next pass.\n" +
			"Thread replies are taken as read locally but leave no dot this clears.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
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
			clear := a.clearBadge
			if clear == nil {
				clear = markread.New(a.cfg.MarkRead, a.logger(), st)
			}
			for i, c := range chats {
				if i > 0 {
					time.Sleep(larkweb.Pace)
				}
				if err := clear(ctx, c); err != nil {
					// Best effort behind a durable write: a refusal costs one
					// red dot, not the pass.
					a.logger().Warn("clear feishu badge", "chat_id", c.ChatID, "err", err)
					res.Failed++
				}
			}
			return a.reportReadAll(cmd, res, false)
		},
	}
	cmd.Flags().BoolVar(&dryRun, "dry-run", false, "count the chats whose red dot would be cleared, and write nothing")
	return cmd
}

func (a *App) reportReadAll(cmd *cobra.Command, res readAllResult, dry bool) error {
	if a.json() {
		return a.printJSON(res)
	}
	if dry {
		cmd.Printf("%d chats waiting in Feishu\n", res.Chats)
		return nil
	}
	cmd.Printf("%d messages read, %d chats cleared in Feishu\n", res.Messages, res.Chats-res.Failed)
	if res.Failed > 0 {
		cmd.Printf("%d chats kept their red dot: not matched to the web client, or refused (see %s)\n", res.Failed, a.cfg.LogPath())
	}
	return nil
}

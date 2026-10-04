package cli

import (
	"context"
	"time"

	"github.com/amzyang/larkim/larkweb"
	"github.com/amzyang/larkim/markread"
	"github.com/spf13/cobra"
)

// readAllResult is what one pass settled.
type readAllResult struct {
	// Messages is how many main-flow rows AcceptRemoteRead flipped on successful POSTs.
	Messages int64 `json:"messages"`
	// Chats is the chats whose main message flow Feishu still reports unseen.
	Chats int `json:"chats"`
	// Failed is how many clears or accepts failed; those chats stay unread.
	Failed int `json:"failed"`
}

func (a *App) readAllCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "read-all",
		Short: "Clear every Feishu client red dot for chats larkim knows are unread",
		Long: "Posts each chat's read watermark to the web client with the Feishu login of mark_read.browser.\n" +
			"On a successful POST, larkim sets is_read_remote for main-flow messages up to that watermark.\n" +
			"A failed POST leaves the badge up; the next pass retries.\n" +
			"Thread replies are not settled by the watermark and stay unread until Feishu reports them read.",
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
			res := readAllResult{Chats: len(chats)}
			clear := a.clearBadge
			if clear == nil {
				clear = markread.New(a.cfg.MarkRead, a.logger(), st)
			}
			for i, c := range chats {
				if i > 0 {
					time.Sleep(larkweb.Pace)
				}
				if err := clear(ctx, c); err != nil {
					a.logger().Warn("clear feishu badge", "chat_id", c.ChatID, "err", err)
					res.Failed++
					continue
				}
				n, err := st.AcceptRemoteRead(ctx, c.ChatID, c.Position)
				if err != nil {
					a.logger().Warn("accept remote read", "chat_id", c.ChatID, "err", err)
					res.Failed++
					continue
				}
				res.Messages += n
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

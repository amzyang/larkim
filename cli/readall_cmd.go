package cli

import (
	"context"
	"time"

	"github.com/amzyang/larkim/applink"
	"github.com/amzyang/larkim/markread"
	"github.com/spf13/cobra"
)

// readAllResult is what one pass settled, on both sides of the line.
type readAllResult struct {
	// Messages is what was taken as read locally, thread replies included.
	Messages int64 `json:"messages"`
	// Chats is how many the desktop client was walked onto: the chats whose
	// main message flow still had something Feishu reports unseen.
	Chats int `json:"chats"`
	// Failed is how many of those were refused — by macOS in applink mode, by
	// the gateway in web mode — which leaves the client's own dot up while
	// larkim's badge is already down. Those chats stay in the set until Feishu
	// reports them read, so the next pass retries them.
	Failed int `json:"failed"`
	// Mode is the lever the pass used, which decides what a success means.
	Mode string `json:"mode"`
}

func (a *App) readAllCmd() *cobra.Command {
	var dryRun bool
	cmd := &cobra.Command{
		Use:   "read-all",
		Short: "Take every chat as read and clear the Feishu client's red dots",
		Long: "The local half is a write. The client's own dots come down by mark_read.mode: applink walks\n" +
			"the desktop client onto each chat over a lark:// applink, in the background, one chat at a time;\n" +
			"web posts each chat's read watermark to the web client with the browser's Feishu login.\n" +
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
				return a.reportReadAll(cmd, readAllResult{Chats: len(chats), Mode: a.cfg.MarkRead.Mode}, true)
			}
			n, err := st.MarkAllRead(ctx, time.Now().UnixMilli())
			if err != nil {
				return err
			}
			res := readAllResult{Messages: n, Chats: len(chats), Mode: a.cfg.MarkRead.Mode}
			clear := markread.New(a.cfg.MarkRead, a.logger(), st, a.open)
			for i, c := range chats {
				if i > 0 {
					time.Sleep(markread.Pace(a.cfg))
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
		cmd.Printf("%d chats waiting in Feishu\n", res.Chats)
		return nil
	}
	// "walked", not "cleared", unless Feishu answered: open returning nil is
	// not the client saying it drew the chat.
	verb, why := "walked", "open refused them"
	if markread.Confirms(a.cfg.MarkRead) {
		verb, why = "cleared", "not matched to the web client, or refused (see "+a.cfg.LogPath()+")"
	}
	cmd.Printf("%d messages read, %d chats %s in Feishu\n", res.Messages, res.Chats-res.Failed, verb)
	if res.Failed > 0 {
		cmd.Printf("%d chats kept their red dot: %s\n", res.Failed, why)
	}
	return nil
}

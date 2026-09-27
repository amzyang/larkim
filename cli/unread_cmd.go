package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/amzyang/larkim/tui"
	"github.com/spf13/cobra"
	"golang.org/x/term"
)

// unreadDefaultWidth is what the page is drawn to when whatever it is being
// written to will not say how wide it is. It is the messages pane at a
// comfortable terminal.
const unreadDefaultWidth = 100

func (a *App) unreadCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "unread",
		Short: "Print every chat that still owes you an answer, parted by chat",
		Long: "The TUI's Unread panel without the cursor: each waiting chat under a rule naming it,\n" +
			"its messages running from the oldest one the badge still counts through to the newest.\n" +
			"Nothing is taken as read — this only reads the store.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			deps := tui.Deps{Store: st, DataDir: a.cfg.DataDir, Log: a.logger(), Self: selfOpenID(ctx, st)}
			if a.json() {
				rows, err := tui.UnreadRows(ctx, deps)
				if err != nil {
					return err
				}
				return a.printJSON(rows)
			}
			page, err := tui.UnreadPage(ctx, deps, a.unreadWidth())
			if err != nil {
				return err
			}
			if page == "" {
				fmt.Fprintln(a.Out, "nothing waiting")
				return nil
			}
			fmt.Fprint(a.Out, page)
			return nil
		},
	}
}

// unreadWidth is how wide the page is drawn: the terminal's own width.
func (a *App) unreadWidth() int {
	if f, ok := a.Out.(*os.File); ok {
		if w, _, err := term.GetSize(int(f.Fd())); err == nil && w > 0 {
			return w
		}
	}
	return unreadDefaultWidth
}

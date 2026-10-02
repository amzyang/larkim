package cli

import (
	"context"
	"fmt"
	"os"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/amzyang/larkim/tui"
)

// unreadDefaultWidth and unreadDefaultHeight are what the page is drawn to when
// whatever it is being written to will not say how large it is. They are the
// messages pane at a comfortable terminal.
const (
	unreadDefaultWidth  = 100
	unreadDefaultHeight = 40
)

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
			page, err := tui.UnreadPage(ctx, deps, a.unreadScreen())
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

// unreadScreen is the terminal the page is drawn for. Anything that is not the
// terminal itself — a redirect, a test's buffer — is drawn to the default size
// and takes no pictures, because an escape sequence in a file is noise.
func (a *App) unreadScreen() tui.UnreadScreen {
	sc := tui.UnreadScreen{Width: unreadDefaultWidth, Height: unreadDefaultHeight}
	f, ok := a.Out.(*os.File)
	if !ok || !term.IsTerminal(int(f.Fd())) {
		return sc
	}
	sc.TTY = true
	h := tui.Probe(os.Stdin, f)
	if h.Width > 0 {
		sc.Width, sc.Height = h.Width, h.Height
	}
	sc.CellW, sc.CellH = h.CellW, h.CellH
	sc.Display = h.Display
	sc.Dark = h.Dark
	sc.Graphics = h.Graphics
	return sc
}

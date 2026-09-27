package cli

import (
	"context"
	"fmt"
	"os"

	"charm.land/lipgloss/v2"
	"github.com/amzyang/larkim/tui"
	"github.com/spf13/cobra"
	"golang.org/x/sys/unix"
	"golang.org/x/term"
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
	sc.Dark = lipgloss.HasDarkBackground(os.Stdin, f)
	// One ioctl answers both questions. kitty fills the pixel fields, which is
	// the cell size without a query the terminal has to answer; a terminal that
	// leaves them zero falls back to the placer's own ratio.
	ws, err := unix.IoctlGetWinsize(int(f.Fd()), unix.TIOCGWINSZ)
	if err != nil || ws.Col == 0 || ws.Row == 0 {
		return sc
	}
	sc.Width, sc.Height = int(ws.Col), int(ws.Row)
	sc.CellW, sc.CellH = int(ws.Xpixel)/int(ws.Col), int(ws.Ypixel)/int(ws.Row)
	return sc
}

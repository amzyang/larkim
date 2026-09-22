package cli

import (
	"context"
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

func (a *App) markCmd() *cobra.Command {
	mark := &cobra.Command{Use: "mark", Short: "Local consumption state (never written to Feishu)"}
	consumed := &cobra.Command{
		Use:   "consumed <message_id>...",
		Short: "Record that these messages were processed locally (see `messages list --unconsumed`)",
		Args:  cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			if err := st.MarkConsumed(context.Background(), args, time.Now().UnixMilli()); err != nil {
				return err
			}
			if a.json() {
				return a.printJSON(map[string]any{"consumed": args})
			}
			fmt.Fprintf(a.Out, "marked %d message(s) consumed\n", len(args))
			return nil
		},
	}
	mark.AddCommand(consumed)
	return mark
}

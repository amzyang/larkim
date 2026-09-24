package cli

import (
	"fmt"

	"github.com/amzyang/larkim/emoji"
	"github.com/spf13/cobra"
)

func (a *App) emojiCmd() *cobra.Command {
	root := &cobra.Command{Use: "emoji", Short: "Feishu's emoji pictures, cut out of the sprite sheet larkim carries"}
	sync := &cobra.Command{
		Use:   "sync",
		Short: "Cut the emoji sprite into one picture per emoji",
		Long: "Writes <data_dir>/emoji/<KEY>.png for every emoji larkim knows, which is what the message\n" +
			"list draws where no Unicode character carries the same feeling. The TUI cuts them itself on\n" +
			"the first start after the sheet changes, so this command is only for cutting them again.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := emoji.Sync(a.cfg.DataDir)
			if err != nil {
				return err
			}
			if a.json() {
				return a.printJSON(map[string]any{"emoji": n, "dir": emoji.Dir(a.cfg.DataDir)})
			}
			fmt.Fprintf(a.Out, "cut %d emoji into %s\n", n, emoji.Dir(a.cfg.DataDir))
			return nil
		},
	}
	root.AddCommand(sync)
	return root
}

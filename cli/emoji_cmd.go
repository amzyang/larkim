package cli

import (
	"fmt"

	"github.com/amzyang/larkim/emoji"
	"github.com/spf13/cobra"
)

func (a *App) emojiCmd() *cobra.Command {
	root := &cobra.Command{Use: "emoji", Short: "Feishu's emoji pictures, cut out of the Lark client"}
	var assets string
	sync := &cobra.Command{
		Use:   "sync",
		Short: "Cut the Lark client's emoji sprite into one picture per emoji",
		Long: "Writes <data_dir>/emoji/<KEY>.png for every emoji the client ships, which is what the\n" +
			"message list draws where no Unicode character carries the same feeling. The pictures are\n" +
			"derived data: delete the directory and run this again.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			n, err := emoji.Sync(assets, a.cfg.DataDir)
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
	sync.Flags().StringVar(&assets, "assets", emoji.DefaultAssetsDir, "the Lark client's assets/emoji directory")
	root.AddCommand(sync)
	return root
}

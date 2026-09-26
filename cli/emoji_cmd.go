package cli

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"

	"github.com/amzyang/larkim/emoji"
	"github.com/amzyang/larkim/tui"
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
	root.AddCommand(sync, a.emojiListCmd(), a.emojiAddCmd(), a.emojiRemoveCmd())
	return root
}

func (a *App) emojiAddCmd() *cobra.Command {
	var source string
	cmd := &cobra.Command{
		Use:   "add <名称> [别名…]",
		Short: "Keep the picture on the clipboard as an emoji of your own",
		Long: "The picture is held to " + strconv.Itoa(emoji.MaxCustomSide) + " pixels on its longest side, because Feishu draws a\n" +
			"pasted picture at its own size and a screenshot would land in the chat as a screenshot.\n" +
			"The name and every alias are reachable by their pinyin and its initials. Adding the same\n" +
			"name again replaces what was there.",
		Args: cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			picture := source
			if picture == "" {
				staged, err := tui.StageClipboardImage(filepath.Join(a.cfg.DataDir, "resources", "pasted"))
				if err != nil {
					return fmt.Errorf("no picture on the clipboard: %w", err)
				}
				defer os.Remove(staged)
				picture = staged
			}
			c, err := emoji.AddCustom(a.cfg.DataDir, args[0], args[1:], picture)
			if err != nil {
				return err
			}
			if a.json() {
				return a.printJSON(customRow(c, a.cfg.DataDir))
			}
			fmt.Fprintf(a.Out, "kept %s as %s\n", c.Name, c.Path(a.cfg.DataDir))
			return nil
		},
	}
	cmd.Flags().StringVar(&source, "image", "", "picture to keep; the clipboard's when left out")
	return cmd
}

func (a *App) emojiRemoveCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "rm <名称>",
		Aliases: []string{"remove"},
		Short:   "Forget one of your own emoji",
		Args:    cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := emoji.RemoveCustom(a.cfg.DataDir, args[0]); err != nil {
				return err
			}
			if a.json() {
				return a.printJSON(map[string]any{"removed": args[0]})
			}
			fmt.Fprintf(a.Out, "forgot %s\n", args[0])
			return nil
		},
	}
	cmd.ValidArgsFunction = func(_ *cobra.Command, args []string, prefix string) ([]string, cobra.ShellCompDirective) {
		if len(args) > 0 {
			return nil, cobra.ShellCompDirectiveNoFileComp
		}
		all, err := emoji.LoadCustom(a.cfg.DataDir)
		if err != nil {
			return nil, cobra.ShellCompDirectiveError
		}
		var out []string
		for _, c := range all {
			if strings.HasPrefix(c.Name, prefix) {
				out = append(out, c.Name)
			}
		}
		return out, cobra.ShellCompDirectiveNoFileComp
	}
	return cmd
}

// emojiRow is one emoji as a consumer outside larkim reads it: the names it
// answers to, the picture it was cut to, and what Feishu will let it do.
type emojiRow struct {
	Key   string `json:"key"`
	ZH    string `json:"zh"`
	EN    string `json:"en"`
	Glyph string `json:"glyph,omitempty"`
	// Terms is what a query is matched against: the names, their pinyin and
	// pinyin initials, the aliases, and the key.
	Terms []string `json:"terms"`
	// Order is the emoji's place in the client's own panel, which is the
	// order a picker falls back to when nothing has been typed.
	Order     int    `json:"order"`
	Reactable bool   `json:"reactable"`
	Delisted  bool   `json:"delisted"`
	Picture   string `json:"picture"`
}

// customRow puts an emoji of one's own in the shape the table's own rows take.
// It is never reactable: Feishu has no key for a picture it has never seen, so
// it travels inside a message or not at all. Order 0 leaves the panel order to
// the emoji that have one.
func customRow(c emoji.Custom, dataDir string) emojiRow {
	return emojiRow{Key: c.Key(), ZH: c.Name, Terms: c.Terms, Picture: c.Path(dataDir)}
}

func (a *App) emojiListCmd() *cobra.Command {
	var custom bool
	cmd := &cobra.Command{
		Use:   "list",
		Short: "Every emoji larkim knows: names, search terms, panel order and picture",
		Long: "The table larkim carries, for a picker built on top of it. `reactable` is false for the ones\n" +
			"Feishu refuses as a reaction, and `delisted` marks those a message cannot carry either — one\n" +
			"of those reaches the other side as its picture or not at all.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
			if custom {
				return a.listCustom()
			}
			// The pictures are cut from the sheet this binary carries, so the
			// paths named here are paths that exist.
			if _, err := emoji.Ensure(a.cfg.DataDir); err != nil {
				return err
			}
			rows := emojiRows(a.cfg.DataDir)
			if a.json() {
				return a.printJSON(rows)
			}
			out := make([][]string, 0, len(rows))
			for _, e := range rows {
				out = append(out, []string{e.Key, e.ZH, e.EN, strconv.Itoa(e.Order), emojiFlags(e)})
			}
			table(a.Out, []string{"KEY", "ZH", "EN", "ORDER", "FLAGS"}, out)
			return nil
		},
	}
	cmd.Flags().BoolVar(&custom, "custom", false, "list the emoji you added yourself instead of Feishu's")
	return cmd
}

// listCustom answers --custom. An entry whose picture has gone is already left
// out by the load, which is what deleting one by hand does.
func (a *App) listCustom() error {
	all, err := emoji.LoadCustom(a.cfg.DataDir)
	if err != nil {
		return err
	}
	rows := make([]emojiRow, 0, len(all))
	for _, c := range all {
		rows = append(rows, customRow(c, a.cfg.DataDir))
	}
	if a.json() {
		return a.printJSON(rows)
	}
	out := make([][]string, 0, len(rows))
	for _, e := range rows {
		out = append(out, []string{e.ZH, strings.Join(e.Terms, " "), e.Picture})
	}
	table(a.Out, []string{"NAME", "TERMS", "PICTURE"}, out)
	return nil
}

// emojiRows is every emoji a picker may offer, in panel order. The bare
// spellings from glyphs.go are left out: they have no name to search by and no
// rectangle to cut a picture from.
func emojiRows(dataDir string) []emojiRow {
	var rows []emojiRow
	for _, e := range emoji.All() {
		if !e.Offerable() {
			continue
		}
		rows = append(rows, emojiRow{Key: e.Key, ZH: e.ZH, EN: e.EN, Glyph: e.Glyph, Terms: e.Terms,
			Order: e.Order, Reactable: e.Reactable(), Delisted: e.Delisted,
			Picture: emoji.Path(dataDir, e.Key)})
	}
	slices.SortFunc(rows, func(a, b emojiRow) int { return cmp.Or(a.Order-b.Order, strings.Compare(a.Key, b.Key)) })
	return rows
}

// emojiFlags is the column a terminal reads instead of two booleans.
func emojiFlags(e emojiRow) string {
	switch {
	case e.Delisted:
		return "delisted"
	case !e.Reactable:
		return "no-reaction"
	}
	return ""
}

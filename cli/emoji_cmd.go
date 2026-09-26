package cli

import (
	"cmp"
	"fmt"
	"slices"
	"strconv"
	"strings"

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
	root.AddCommand(sync, a.emojiListCmd())
	return root
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

func (a *App) emojiListCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "list",
		Short: "Every emoji larkim knows: names, search terms, panel order and picture",
		Long: "The table larkim carries, for a picker built on top of it. `reactable` is false for the ones\n" +
			"Feishu refuses as a reaction, and `delisted` marks those a message cannot carry either — one\n" +
			"of those reaches the other side as its picture or not at all.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, args []string) error {
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

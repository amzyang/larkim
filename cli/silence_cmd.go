package cli

import (
	"context"
	"strconv"

	"github.com/amzyang/larkim/store"
	"github.com/spf13/cobra"
)

// silenceMatch is one configured rule with what it currently silences.
type silenceMatch struct {
	store.SilenceRule
	Matched   int64 `json:"matched"`
	LastMatch int64 `json:"last_match_ms"`
}

func (a *App) silenceCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "silence",
		Short: "Configured silence rules and the messages each one matches",
		Long: "Silence rules keep matching messages out of the unread badge and out of the chat list's ordering.\n" +
			"A rule naming an id that does not exist fails silently, so this is where a typo shows up as a zero.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			rows := make([]silenceMatch, 0, len(a.cfg.Silence))
			for _, r := range a.cfg.Silence {
				n, lastMs, err := st.SilenceMatches(ctx, r)
				if err != nil {
					return err
				}
				rows = append(rows, silenceMatch{SilenceRule: r, Matched: n, LastMatch: lastMs})
			}
			if a.json() {
				return a.printJSON(rows)
			}
			if len(rows) == 0 {
				cmd.Printf("no silence rules configured (config key: silence)\n")
				return nil
			}
			out := make([][]string, 0, len(rows))
			for _, r := range rows {
				out = append(out, []string{r.Chat, r.Sender, oneLine(r.Contains, 30), strconv.FormatInt(r.Matched, 10), fmtMs(r.LastMatch)})
			}
			table(a.Out, []string{"chat", "sender", "contains", "matched", "last match"}, out)
			return nil
		},
	}
}

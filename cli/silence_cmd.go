package cli

import (
	"context"
	"strconv"
	"strings"

	"github.com/amzyang/larkim/config"
	"github.com/amzyang/larkim/store"
	"github.com/spf13/cobra"
)

// silenceMatch is one configured rule with what it currently silences.
type silenceMatch struct {
	store.SilenceRule
	Matched   int64 `json:"matched"`
	LastMatch int64 `json:"last_match_ms"`
}

var silenceHeader = []string{"chat", "sender", "contains", "matched", "last match"}

func (r silenceMatch) cells() []string {
	return []string{r.Chat, r.Sender, oneLine(r.Contains, 30), strconv.FormatInt(r.Matched, 10), fmtMs(r.LastMatch)}
}

func (a *App) silenceCmd() *cobra.Command {
	cmd := &cobra.Command{
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
				out = append(out, r.cells())
			}
			table(a.Out, silenceHeader, out)
			return nil
		},
	}
	cmd.AddCommand(a.silenceAddCmd())
	return cmd
}

// silenceAdded is what add reports: the rule as written, what it matches
// among the stored messages, and whether the file already held it.
type silenceAdded struct {
	silenceMatch
	Added bool `json:"added"`
}

func (a *App) silenceAddCmd() *cobra.Command {
	var rule store.SilenceRule
	cmd := &cobra.Command{
		Use:   "add [--chat <oc_ | name>] [--sender <open_id>] [--contains <text>]",
		Short: "Add a silence rule to the config file",
		Long: "The flags given are an AND, and at least one is required. --chat alone silences the whole chat on this machine.\n" +
			"A running daemon takes the rule on its next tick; a TUI that syncs in-process takes it on its next start.",
		Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			rule.Contains = strings.TrimSpace(rule.Contains)
			if err := (store.SilenceRules{rule}).Validate(); err != nil {
				return &usageError{err}
			}
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			ctx := context.Background()
			if rule.Chat != "" {
				if rule.Chat, err = resolveChatLocal(ctx, st, rule.Chat); err != nil {
					return &usageError{err}
				}
			}
			_, added, err := config.UpdateSilence(a.configPath, func(rs store.SilenceRules) store.SilenceRules { return append(rs, rule) })
			if err != nil {
				return err
			}
			n, lastMs, err := st.SilenceMatches(ctx, rule)
			if err != nil {
				return err
			}
			res := silenceAdded{SilenceRule: rule, Matched: n, LastMatch: lastMs, Added: added}
			if a.json() {
				return a.printJSON(res)
			}
			table(a.Out, silenceHeader, [][]string{res.cells()})
			verb := "added to"
			if !added {
				verb = "already in"
			}
			cmd.Printf("%s %s\n", verb, config.Resolve(a.configPath))
			return nil
		},
	}
	cmd.Flags().StringVar(&rule.Chat, "chat", "", "chat whose messages the rule covers: an oc_ id or an exact chat name")
	cmd.Flags().StringVar(&rule.Sender, "sender", "", "sender whose messages the rule covers: a person's or a bot's open id")
	cmd.Flags().StringVar(&rule.Contains, "contains", "", "text the message must contain, case-insensitive")
	mustWire(cmd.RegisterFlagCompletionFunc("chat", a.completeChatRef))
	mustWire(cmd.RegisterFlagCompletionFunc("sender", a.completeSenderID))
	return cmd
}

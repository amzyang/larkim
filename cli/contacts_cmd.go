package cli

import (
	"context"

	"github.com/spf13/cobra"
)

func (a *App) contactsCmd() *cobra.Command {
	contacts := &cobra.Command{Use: "contacts", Short: "Users and bots seen in synced chats"}
	var search string
	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List cached contacts",
		RunE: func(cmd *cobra.Command, _ []string) error {
			st, err := a.openStore()
			if err != nil {
				return err
			}
			defer st.Close()
			// A search is matched in Go, so the row limit has to be applied
			// after it: asking SQLite for a page first would drop hits that
			// sit past it.
			storeLimit := limit
			if search != "" {
				storeLimit = 0
			}
			rows, err := st.ListContacts(context.Background(), storeLimit)
			if err != nil {
				return err
			}
			rows = fuzzyContacts(rows, search, limit)
			if a.json() {
				return a.printJSON(rows)
			}
			out := make([][]string, 0, len(rows))
			for _, c := range rows {
				bot := ""
				if c.IsBot {
					bot = "bot"
				}
				out = append(out, []string{c.OpenID, c.Name, bot, c.Email, c.AvatarPath})
			}
			table(a.Out, []string{"open_id", "name", "type", "email", "avatar"}, out)
			return nil
		},
	}
	list.Flags().StringVar(&search, "search", "", "fuzzy match on the name or email; Chinese names also answer to their pinyin or its initials")
	list.Flags().IntVar(&limit, "limit", 0, "max rows")
	contacts.AddCommand(list)
	return contacts
}

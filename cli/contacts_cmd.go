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
			rows, err := st.ListContacts(context.Background(), search, limit)
			if err != nil {
				return err
			}
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
	list.Flags().StringVar(&search, "search", "", "substring of name or email")
	list.Flags().IntVar(&limit, "limit", 0, "max rows")
	contacts.AddCommand(list)
	return contacts
}

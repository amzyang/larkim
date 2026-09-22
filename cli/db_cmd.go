package cli

import (
	"fmt"

	"github.com/amzyang/larkim/store"
	"github.com/spf13/cobra"
)

func (a *App) dbCmd() *cobra.Command {
	db := &cobra.Command{Use: "db", Short: "Database location for direct SQLite consumers"}
	db.AddCommand(&cobra.Command{
		Use:   "path",
		Short: "Print the SQLite file path",
		RunE: func(cmd *cobra.Command, _ []string) error {
			if a.json() {
				return a.printJSON(map[string]string{"db": a.cfg.DBPath(), "data_dir": a.cfg.DataDir, "resources": a.cfg.ResourcesDir()})
			}
			fmt.Fprintln(a.Out, a.cfg.DBPath())
			return nil
		},
	})
	return db
}

func (a *App) schemaCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "schema",
		Short: "Print the SQLite schema (DDL of every migration)",
		RunE: func(cmd *cobra.Command, _ []string) error {
			fmt.Fprint(a.Out, store.Schema())
			return nil
		},
	}
}

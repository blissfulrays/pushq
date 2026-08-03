package main

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/blissfulrays/pushq/internal/config"
)

func migrateCmd() *cobra.Command {
	var cfgPath, db string
	cmd := &cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations and exit",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if db != "" {
				cfg.DB = db
			}
			st, err := openStore(cfg)
			if err != nil {
				return err
			}
			defer st.Close()
			if err := st.Migrate(cmd.Context()); err != nil {
				return err
			}
			fmt.Println("migrations applied", "("+st.Dialect()+")")
			return nil
		},
	}
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to YAML config file")
	cmd.Flags().StringVar(&db, "db", "", "database URL (sqlite:PATH or postgres://…)")
	return cmd
}

package main

import (
	"context"
	"fmt"
	"os"
	"strings"
	"text/tabwriter"
	"time"

	"github.com/spf13/cobra"

	"github.com/blissfulrays/pushq/internal/accounts"
	"github.com/blissfulrays/pushq/internal/config"
	"github.com/blissfulrays/pushq/internal/store"
)

// usersCmd works on the database directly (like migrate), so it still works
// when nobody can sign in — e.g. the only admin forgot their password.
func usersCmd() *cobra.Command {
	var cfgPath, db string
	cmd := &cobra.Command{
		Use:   "users",
		Short: "Manage dashboard accounts directly in the database (account recovery)",
	}
	cmd.PersistentFlags().StringVarP(&cfgPath, "config", "c", "", "path to YAML config file")
	cmd.PersistentFlags().StringVar(&db, "db", "", "database URL (sqlite:PATH or postgres://…)")

	open := func(ctx context.Context) (*accounts.Store, func(), error) {
		cfg, err := config.Load(cfgPath)
		if err != nil {
			return nil, nil, err
		}
		if db != "" {
			cfg.DB = db
		}
		st, err := openStore(cfg)
		if err != nil {
			return nil, nil, err
		}
		if err := st.Migrate(ctx); err != nil {
			st.Close()
			return nil, nil, fmt.Errorf("migrate: %w", err)
		}
		sb, ok := st.(store.SQLBacked)
		if !ok {
			st.Close()
			return nil, nil, fmt.Errorf("%s store does not support accounts", st.Dialect())
		}
		w, r := sb.SQLHandles()
		return accounts.New(w, r, st.Dialect()), func() { st.Close() }, nil
	}

	list := &cobra.Command{
		Use:   "list",
		Short: "List dashboard accounts",
		RunE: func(cmd *cobra.Command, args []string) error {
			acc, closeFn, err := open(cmd.Context())
			if err != nil {
				return err
			}
			defer closeFn()
			users, err := acc.ListUsers(cmd.Context())
			if err != nil {
				return err
			}
			if len(users) == 0 {
				fmt.Println("No accounts yet — open the dashboard (/ui/) to create the first admin.")
				return nil
			}
			tw := tabwriter.NewWriter(os.Stdout, 0, 4, 2, ' ', 0)
			fmt.Fprintln(tw, "EMAIL\tNAME\tROLE\tLAST LOGIN")
			for _, u := range users {
				last := "never"
				if u.LastLoginAt != nil {
					last = u.LastLoginAt.Local().Format(time.DateTime)
				}
				fmt.Fprintf(tw, "%s\t%s\t%s\t%s\n", u.Email, u.Name, u.Role, last)
			}
			return tw.Flush()
		},
	}

	var baseURL string
	reset := &cobra.Command{
		Use:   "reset-password EMAIL",
		Short: "Print a one-time password-reset link for an account",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			acc, closeFn, err := open(cmd.Context())
			if err != nil {
				return err
			}
			defer closeFn()
			u, err := acc.UserByEmail(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			token, inv, err := acc.CreateResetLink(cmd.Context(), u.ID, nil, time.Now())
			if err != nil {
				return err
			}
			fmt.Printf("Password reset link for %s (%s), valid until %s:\n\n  %s/ui/invite#token=%s\n\n",
				u.Email, u.Role, inv.ExpiresAt.Local().Format(time.DateTime), strings.TrimRight(baseURL, "/"), token)
			fmt.Println("Opening it lets them choose a new password; it works once and signs them out everywhere.")
			return nil
		},
	}
	reset.Flags().StringVar(&baseURL, "url", "http://localhost:8080", "public URL of the pushq server, used to build the link")

	cmd.AddCommand(list, reset)
	return cmd
}

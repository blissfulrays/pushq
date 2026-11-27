package main

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"github.com/spf13/cobra"

	"github.com/blissfulrays/pushq/internal/config"
)

func devCmd() *cobra.Command {
	var listen string
	var memory bool
	cmd := &cobra.Command{
		Use:   "dev",
		Short: "Run a local dev server: temp SQLite, no auth, fast timers, verbose logs",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg := config.Default()
			cfg.Listen = listen
			cfg.Log.Level = "debug"
			cfg.Dispatcher.ReconcileInterval = time.Second
			cfg.Dispatcher.SweepInterval = 2 * time.Second
			cfg.Dispatcher.CleanupInterval = 5 * time.Second
			if memory {
				cfg.DB = "sqlite::memory:"
			} else {
				cfg.DB = "sqlite:" + filepath.Join(os.TempDir(), "pushq-dev.db")
			}

			fmt.Printf(`pushq dev server
  API        http://localhost%s/v1
  dashboard  http://localhost%s/ui/
  metrics    http://localhost%s/metrics
  db         %s
  auth       disabled (dev mode)

Try it:
  curl -X PUT  localhost%[1]s/v1/queues/demo -d '{"rate_limits":{"max_dispatches_per_second":5}}'
  curl -X POST localhost%[1]s/v1/queues/demo/tasks -d '{"url":"https://httpbin.org/post","json_body":{"hello":"world"}}'

`, cfg.Listen, cfg.Listen, cfg.Listen, cfg.DB)
			return runServe(cmd.Context(), cfg, true)
		},
	}
	cmd.Flags().StringVar(&listen, "listen", ":8080", "listen address")
	cmd.Flags().BoolVar(&memory, "memory", false, "use an in-memory database (state lost on exit)")
	return cmd
}

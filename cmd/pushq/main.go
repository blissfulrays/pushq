// pushq is a self-hosted HTTP push task-queue server.
package main

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/blissfulrays/pushq/internal/version"
)

func main() {
	root := &cobra.Command{
		Use:           "pushq",
		Short:         "pushq — self-hosted HTTP push task queue (an open-source Cloud Tasks alternative)",
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.AddCommand(serveCmd(), devCmd(), migrateCmd(), usersCmd(), versionCmd(), queuesCmd(), tasksCmd())

	if err := root.Execute(); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func versionCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "version",
		Short: "Print version",
		Run: func(cmd *cobra.Command, args []string) {
			fmt.Printf("pushq %s (%s)\n", version.Version, version.Commit)
		},
	}
}

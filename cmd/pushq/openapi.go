package main

import (
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"

	"github.com/spf13/cobra"

	"github.com/blissfulrays/pushq/internal/api"
	"github.com/blissfulrays/pushq/internal/auth"
)

// openapiCmd prints the OpenAPI document generated from the server's routes
// (the same one a running server serves at /openapi.json; --console prints
// the dashboard's own API from /openapi/console.json). Handy for SDK
// generators and CI; needs no database.
func openapiCmd() *cobra.Command {
	var console bool
	cmd := &cobra.Command{
		Use:   "openapi",
		Short: "Print the OpenAPI spec generated from the API routes",
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := &api.Server{
				Auth:           auth.New(nil, nil, false),
				Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
				MetricsHandler: http.NotFoundHandler(), // so /metrics is documented
			}
			aud := api.AudienceSDK
			if console {
				aud = api.AudienceConsole
			}
			spec, err := srv.OpenAPISpec(aud)
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(os.Stdout, string(spec))
			return err
		},
	}
	cmd.Flags().BoolVar(&console, "console", false, "print the console (dashboard) API instead of the SDK API (/openapi/console.json)")
	return cmd
}

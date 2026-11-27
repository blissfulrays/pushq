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
// (the same one a running server serves at /openapi.json). Handy for SDK
// generators and CI; needs no database.
func openapiCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "openapi",
		Short: "Print the OpenAPI spec generated from the API routes",
		RunE: func(cmd *cobra.Command, args []string) error {
			srv := &api.Server{
				Auth:           auth.New(nil, nil, false),
				Log:            slog.New(slog.NewTextHandler(io.Discard, nil)),
				MetricsHandler: http.NotFoundHandler(), // so /metrics is documented
			}
			spec, err := srv.OpenAPISpec()
			if err != nil {
				return err
			}
			_, err = fmt.Fprintln(os.Stdout, string(spec))
			return err
		},
	}
}

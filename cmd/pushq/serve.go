package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/blissfulrays/pushq/internal/api"
	"github.com/blissfulrays/pushq/internal/auth"
	"github.com/blissfulrays/pushq/internal/config"
	"github.com/blissfulrays/pushq/internal/dispatch"
	"github.com/blissfulrays/pushq/internal/metrics"
	"github.com/blissfulrays/pushq/internal/store"
	"github.com/blissfulrays/pushq/internal/store/postgres"
	"github.com/blissfulrays/pushq/internal/store/sqlite"
	"github.com/blissfulrays/pushq/internal/version"
)

func serveCmd() *cobra.Command {
	var cfgPath, db, listen, logLevel string
	cmd := &cobra.Command{
		Use:   "serve",
		Short: "Run the pushq server (API + dispatcher)",
		RunE: func(cmd *cobra.Command, args []string) error {
			cfg, err := config.Load(cfgPath)
			if err != nil {
				return err
			}
			if db != "" {
				cfg.DB = db
			}
			if listen != "" {
				cfg.Listen = listen
			}
			if logLevel != "" {
				cfg.Log.Level = logLevel
			}
			if err := cfg.Validate(); err != nil {
				return err
			}
			return runServe(cmd.Context(), cfg)
		},
	}
	cmd.Flags().StringVarP(&cfgPath, "config", "c", "", "path to YAML config file")
	cmd.Flags().StringVar(&db, "db", "", "database URL (sqlite:PATH or postgres://…)")
	cmd.Flags().StringVar(&listen, "listen", "", "listen address (e.g. :8080)")
	cmd.Flags().StringVar(&logLevel, "log-level", "", "log level (debug|info|warn|error)")
	return cmd
}

func newLogger(cfg config.Log) *slog.Logger {
	var level slog.Level
	switch strings.ToLower(cfg.Level) {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: level}
	if strings.EqualFold(cfg.Format, "json") {
		return slog.New(slog.NewJSONHandler(os.Stderr, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stderr, opts))
}

func openStore(cfg config.Config) (store.Store, error) {
	switch {
	case strings.HasPrefix(cfg.DB, "sqlite:"):
		return sqlite.Open(cfg.DB)
	case strings.HasPrefix(cfg.DB, "postgres://"), strings.HasPrefix(cfg.DB, "postgresql://"):
		return openPostgres(cfg.DB)
	default:
		return nil, fmt.Errorf("unsupported db %q", cfg.DB)
	}
}

func runServe(ctx context.Context, cfg config.Config) error {
	logger := newLogger(cfg.Log)

	st, err := openStore(cfg)
	if err != nil {
		return err
	}
	defer st.Close()
	if err := st.Migrate(ctx); err != nil {
		return fmt.Errorf("migrate: %w", err)
	}

	reg := metrics.New()
	var leader dispatch.LeaderElector
	if pg, ok := st.(*postgres.Store); ok {
		// Multiple replicas may share one Postgres; serialize the
		// dispatcher role with an advisory lock.
		leader = pg.NewElector()
	}
	disp := dispatch.New(st, dispatch.Options{
		GlobalConcurrency: cfg.Dispatcher.GlobalConcurrency,
		PerQueueClaimCap:  cfg.Dispatcher.PerQueueClaimCap,
		ReconcileInterval: cfg.Dispatcher.ReconcileInterval,
		SweepInterval:     cfg.Dispatcher.SweepInterval,
		CleanupInterval:   cfg.Dispatcher.CleanupInterval,
		LeaseGrace:        cfg.Dispatcher.LeaseGrace,
		DrainTimeout:      cfg.Dispatcher.DrainTimeout,
		RetentionSucceeded: cfg.Retention.Succeeded,
		RetentionFailed:    cfg.Retention.Failed,
		DedupWindow:        cfg.Limits.DedupWindow,
		Leader:             leader,
	}, logger, reg)

	srv := &api.Server{
		Store:          st,
		Auth:           auth.New(cfg.APIKeys),
		Log:            logger,
		Limits:         cfg.Limits,
		MetricsPublic:  cfg.Metrics.Public,
		MetricsHandler: reg.Handler(),
		Now:            time.Now,
		Role:           disp.Role,
	}
	if srv.Auth.Open() {
		logger.Warn("no API keys configured — the API is UNAUTHENTICATED; set api_keys in config or PUSHQ_API_KEYS")
	}

	httpSrv := &http.Server{
		Addr:              cfg.Listen,
		Handler:           srv.Router(),
		ReadHeaderTimeout: 10 * time.Second,
	}

	ctx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()

	disp.Start(ctx)

	errCh := make(chan error, 1)
	go func() {
		logger.Info("pushq serving", "version", version.Version, "listen", cfg.Listen, "db", st.Dialect())
		if err := httpSrv.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- err
		}
	}()

	select {
	case err := <-errCh:
		disp.Stop()
		return err
	case <-ctx.Done():
	}

	logger.Info("shutting down")
	shutdownCtx, cancel := context.WithTimeout(context.Background(), cfg.Dispatcher.DrainTimeout+5*time.Second)
	defer cancel()
	_ = httpSrv.Shutdown(shutdownCtx)
	disp.Stop()
	return nil
}

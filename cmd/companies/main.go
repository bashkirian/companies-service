package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
	"go.uber.org/zap"

	"github.com/bashkirian/companies-service/internal/app"
	"github.com/bashkirian/companies-service/internal/config"
	"github.com/bashkirian/companies-service/internal/observability"
)

var version = "dev"

func main() {
	var configPath string

	root := &cobra.Command{
		Use:           "companies",
		Short:         "Companies microservice",
		Version:       version,
		SilenceUsage:  true,
		SilenceErrors: true,
	}
	root.PersistentFlags().StringVarP(&configPath, "config", "c", envOr("CONFIG_PATH", "configs/config.yaml"), "path to the YAML config")

	root.AddCommand(&cobra.Command{
		Use:   "serve",
		Short: "Run the HTTP API and the outbox relay",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			log, err := observability.NewLogger(observability.LogConfig{Level: cfg.Log.Level, Format: cfg.Log.Format})
			if err != nil {
				return err
			}
			defer func() { _ = log.Sync() }()

			log.Info("starting", zap.String("version", version))
			if err := app.Run(cmd.Context(), cfg, log); err != nil {
				log.Error("stopped with error", zap.Error(err))
				return err
			}
			log.Info("stopped")
			return nil
		},
	})
	root.AddCommand(&cobra.Command{
		Use:   "migrate",
		Short: "Apply database migrations",
		RunE: func(cmd *cobra.Command, _ []string) error {
			cfg, err := config.Load(configPath)
			if err != nil {
				return err
			}
			return app.Migrate(cmd.Context(), cfg)
		},
	})

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "error:", err)
		os.Exit(1)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

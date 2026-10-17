package app

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/bashkirian/companies-service/internal/adapter/jwt"
	"github.com/bashkirian/companies-service/internal/adapter/kafka"
	"github.com/bashkirian/companies-service/internal/adapter/storage/postgres"
	"github.com/bashkirian/companies-service/internal/config"
	"github.com/bashkirian/companies-service/internal/observability"
	httptransport "github.com/bashkirian/companies-service/internal/transport/http"
	"github.com/bashkirian/companies-service/internal/usecase/company"
	"github.com/bashkirian/companies-service/internal/usecase/outbox"
)

// Run wires dependencies and serves until ctx is canceled.
func Run(ctx context.Context, cfg config.Config, log *zap.Logger) error {
	pool, err := postgres.NewPool(ctx, postgresConfig(cfg))
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()

	if cfg.Postgres.MigrateOnStart {
		if err := postgres.Migrate(ctx, pool); err != nil {
			return err
		}
		log.Info("migrations applied")
	}

	publisher, err := kafka.NewPublisher(kafka.Config{
		Brokers:  cfg.Kafka.Brokers,
		Topic:    cfg.Kafka.Topic,
		ClientID: cfg.Kafka.ClientID,
		TLS: kafka.TLSConfig{
			Enabled:    cfg.Kafka.TLS.Enabled,
			CAFile:     cfg.Kafka.TLS.CAFile,
			CertFile:   cfg.Kafka.TLS.CertFile,
			KeyFile:    cfg.Kafka.TLS.KeyFile,
			ServerName: cfg.Kafka.TLS.ServerName,
		},
		SASL: kafka.SASLConfig{
			Mechanism: cfg.Kafka.SASL.Mechanism,
			Username:  cfg.Kafka.SASL.Username,
			Password:  cfg.Kafka.SASL.Password,
		},
	})
	if err != nil {
		return err
	}
	defer publisher.Close()

	validator, err := jwt.NewValidator(jwt.Config{
		Algorithm:     strings.ToUpper(cfg.JWT.Algorithm),
		Secret:        cfg.JWT.Secret,
		PublicKeyFile: cfg.JWT.PublicKeyFile,
		Issuer:        cfg.JWT.Issuer,
		Audience:      cfg.JWT.Audience,
		Leeway:        cfg.JWT.Leeway,
	})
	if err != nil {
		return fmt.Errorf("create jwt validator: %w", err)
	}

	metrics := observability.NewMetrics()
	tx := postgres.NewTxManager(pool)
	outboxRepo := postgres.NewOutboxRepo(pool)
	svc := company.NewService(postgres.NewCompanyRepo(pool), outboxRepo, tx)
	relay := outbox.NewRelay(outboxRepo, publisher, tx, metrics, outbox.Config{
		PollInterval:    cfg.Outbox.PollInterval,
		BatchSize:       cfg.Outbox.BatchSize,
		PublishTimeout:  cfg.Outbox.PublishTimeout,
		BacklogInterval: cfg.Outbox.BacklogInterval,
	}, log.Named("outbox"))

	srv := newServer(cfg.HTTP, cfg.HTTP.Addr, httptransport.NewRouter(httptransport.Deps{
		Log:       log.Named("http"),
		Service:   svc,
		Validator: validator,
		Metrics:   metrics,
		// Kafka is left out on purpose: the outbox keeps writes working while it is down,
		// and the outbox age metric reports the outage.
		Ready: map[string]httptransport.ReadinessCheck{
			"postgres": func(ctx context.Context) error { return pool.Ping(ctx) },
		},
	}))
	admin := newServer(cfg.HTTP, cfg.HTTP.AdminAddr, httptransport.NewAdminRouter(metrics))

	g, gctx := errgroup.WithContext(ctx)
	for name, s := range map[string]*http.Server{"http": srv, "admin": admin} {
		g.Go(func() error {
			log.Info(name+" server started", zap.String("addr", s.Addr))
			if err := s.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
				return fmt.Errorf("%s server: %w", name, err)
			}
			return nil
		})
	}
	g.Go(func() error {
		return relay.Run(gctx)
	})
	g.Go(func() error {
		return relay.ReportBacklog(gctx)
	})
	g.Go(func() error {
		<-gctx.Done()
		log.Info("shutting down")
		shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), cfg.HTTP.ShutdownTimeout)
		defer cancel()
		return errors.Join(srv.Shutdown(shutdownCtx), admin.Shutdown(shutdownCtx))
	})
	return g.Wait()
}

// Migrate applies database migrations and exits.
func Migrate(ctx context.Context, cfg config.Config) error {
	pool, err := postgres.NewPool(ctx, postgresConfig(cfg))
	if err != nil {
		return fmt.Errorf("connect postgres: %w", err)
	}
	defer pool.Close()
	return postgres.Migrate(ctx, pool)
}

func newServer(cfg config.HTTPConfig, addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: cfg.ReadHeaderTimeout,
		ReadTimeout:       cfg.ReadTimeout,
		WriteTimeout:      cfg.WriteTimeout,
		IdleTimeout:       cfg.IdleTimeout,
	}
}

func postgresConfig(cfg config.Config) postgres.Config {
	return postgres.Config{DSN: cfg.Postgres.DSN, MaxConns: cfg.Postgres.MaxConns, MinConns: cfg.Postgres.MinConns}
}

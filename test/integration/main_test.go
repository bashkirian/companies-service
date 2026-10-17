//go:build integration

package integration

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"testing"
	"time"

	"github.com/testcontainers/testcontainers-go"
	tckafka "github.com/testcontainers/testcontainers-go/modules/kafka"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"
	"github.com/twmb/franz-go/pkg/kadm"
	"github.com/twmb/franz-go/pkg/kgo"
	"go.uber.org/zap"

	"github.com/bashkirian/companies-service/internal/app"
	"github.com/bashkirian/companies-service/internal/config"
)

const (
	jwtSecret = "integration-secret-integration-secret"
	topic     = "companies.events"
)

var (
	baseURL  string
	adminURL string
	brokers  []string
)

func TestMain(m *testing.M) {
	code, err := run(m)
	if err != nil {
		fmt.Fprintln(os.Stderr, "integration setup:", err)
		os.Exit(1)
	}
	os.Exit(code)
}

func run(m *testing.M) (int, error) {
	ctx := context.Background()

	pg, err := tcpostgres.Run(ctx, "postgres:17-alpine",
		tcpostgres.WithDatabase("companies"),
		tcpostgres.WithUsername("companies"),
		tcpostgres.WithPassword("companies"),
		tcpostgres.BasicWaitStrategies(),
	)
	if err != nil {
		return 0, fmt.Errorf("start postgres: %w", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(pg) }()
	dsn, err := pg.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		return 0, err
	}

	kc, err := tckafka.Run(ctx, "confluentinc/confluent-local:7.5.0", tckafka.WithClusterID("integration"))
	if err != nil {
		return 0, fmt.Errorf("start kafka: %w", err)
	}
	defer func() { _ = testcontainers.TerminateContainer(kc) }()
	if brokers, err = kc.Brokers(ctx); err != nil {
		return 0, err
	}
	if err := createTopic(ctx); err != nil {
		return 0, err
	}

	addr, err := freeAddr()
	if err != nil {
		return 0, err
	}
	adminAddr, err := freeAddr()
	if err != nil {
		return 0, err
	}
	cfg := config.Config{
		Log: config.LogConfig{Level: "warn", Format: "console"},
		HTTP: config.HTTPConfig{
			Addr: addr, AdminAddr: adminAddr, ReadHeaderTimeout: time.Second, ReadTimeout: 5 * time.Second,
			WriteTimeout: 5 * time.Second, IdleTimeout: time.Minute, ShutdownTimeout: 5 * time.Second,
		},
		Postgres: config.PostgresConfig{DSN: dsn, MaxConns: 10, MigrateOnStart: true},
		Kafka:    config.KafkaConfig{Brokers: brokers, Topic: topic, ClientID: "integration"},
		Outbox: config.OutboxConfig{
			PollInterval: 50 * time.Millisecond, BatchSize: 50, PublishTimeout: 5 * time.Second, BacklogInterval: time.Second,
		},
		JWT: config.JWTConfig{Algorithm: "HS256", Secret: jwtSecret, Issuer: "integration", Audience: "companies"},
	}

	appCtx, cancel := context.WithCancel(ctx)
	done := make(chan error, 1)
	go func() { done <- app.Run(appCtx, cfg, zap.NewNop()) }()

	baseURL = "http://" + addr
	adminURL = "http://" + adminAddr
	if err := waitReady(done); err != nil {
		cancel()
		return 0, err
	}

	code := m.Run()
	cancel()
	if err := <-done; err != nil {
		return 0, fmt.Errorf("app stopped with error: %w", err)
	}
	return code, nil
}

func createTopic(ctx context.Context) error {
	cl, err := kgo.NewClient(kgo.SeedBrokers(brokers...))
	if err != nil {
		return err
	}
	defer cl.Close()
	resp, err := kadm.NewClient(cl).CreateTopic(ctx, 3, 1, nil, topic)
	if err != nil {
		return fmt.Errorf("create topic: %w", err)
	}
	return resp.Err
}

func freeAddr() (string, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	defer func() { _ = l.Close() }()
	return l.Addr().String(), nil
}

func waitReady(done <-chan error) error {
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		select {
		case err := <-done:
			return fmt.Errorf("app exited early: %w", err)
		default:
		}
		resp, err := http.Get(baseURL + "/ready")
		if err == nil {
			_ = resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(200 * time.Millisecond)
	}
	return fmt.Errorf("app not ready in time")
}

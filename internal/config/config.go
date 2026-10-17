package config

import (
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"gopkg.in/yaml.v3"
)

type Config struct {
	Log      LogConfig      `yaml:"log"`
	HTTP     HTTPConfig     `yaml:"http"`
	Postgres PostgresConfig `yaml:"postgres"`
	Kafka    KafkaConfig    `yaml:"kafka"`
	Outbox   OutboxConfig   `yaml:"outbox"`
	JWT      JWTConfig      `yaml:"jwt"`
}

type LogConfig struct {
	Level  string `yaml:"level"`
	Format string `yaml:"format"`
}

type HTTPConfig struct {
	Addr string `yaml:"addr"`
	// AdminAddr serves /metrics apart from the public API.
	AdminAddr         string        `yaml:"admin_addr"`
	ReadHeaderTimeout time.Duration `yaml:"read_header_timeout"`
	ReadTimeout       time.Duration `yaml:"read_timeout"`
	WriteTimeout      time.Duration `yaml:"write_timeout"`
	IdleTimeout       time.Duration `yaml:"idle_timeout"`
	ShutdownTimeout   time.Duration `yaml:"shutdown_timeout"`
}

type PostgresConfig struct {
	DSN      string `yaml:"dsn"`
	MaxConns int32  `yaml:"max_conns"`
	MinConns int32  `yaml:"min_conns"`
	// MigrateOnStart applies migrations before serving. Keep false when migrations run as a separate job.
	MigrateOnStart bool `yaml:"migrate_on_start"`
}

type KafkaConfig struct {
	// Brokers entries may hold comma-separated lists, so one env variable can carry all brokers.
	Brokers  []string        `yaml:"brokers"`
	Topic    string          `yaml:"topic"`
	ClientID string          `yaml:"client_id"`
	TLS      KafkaTLSConfig  `yaml:"tls"`
	SASL     KafkaSASLConfig `yaml:"sasl"`
}

type KafkaTLSConfig struct {
	Enabled    bool   `yaml:"enabled"`
	CAFile     string `yaml:"ca_file"`
	CertFile   string `yaml:"cert_file"`
	KeyFile    string `yaml:"key_file"`
	ServerName string `yaml:"server_name"`
}

type KafkaSASLConfig struct {
	Mechanism string `yaml:"mechanism"`
	Username  string `yaml:"username"`
	Password  string `yaml:"password"`
}

type OutboxConfig struct {
	PollInterval    time.Duration `yaml:"poll_interval"`
	BatchSize       int           `yaml:"batch_size"`
	PublishTimeout  time.Duration `yaml:"publish_timeout"`
	BacklogInterval time.Duration `yaml:"backlog_interval"`
}

type JWTConfig struct {
	Algorithm     string        `yaml:"algorithm"`
	Secret        string        `yaml:"secret"`
	PublicKeyFile string        `yaml:"public_key_file"`
	Issuer        string        `yaml:"issuer"`
	Audience      string        `yaml:"audience"`
	Leeway        time.Duration `yaml:"leeway"`
}

func defaults() Config {
	return Config{
		Log: LogConfig{Level: "info", Format: "json"},
		HTTP: HTTPConfig{
			Addr:              ":8080",
			AdminAddr:         ":9090",
			ReadHeaderTimeout: 2 * time.Second,
			ReadTimeout:       5 * time.Second,
			WriteTimeout:      10 * time.Second,
			IdleTimeout:       60 * time.Second,
			ShutdownTimeout:   15 * time.Second,
		},
		Postgres: PostgresConfig{MaxConns: 10},
		Kafka:    KafkaConfig{Topic: "companies.events", ClientID: "companies-service"},
		Outbox: OutboxConfig{
			PollInterval:    500 * time.Millisecond,
			BatchSize:       100,
			PublishTimeout:  10 * time.Second,
			BacklogInterval: 15 * time.Second,
		},
		JWT: JWTConfig{Algorithm: "HS256", Leeway: 30 * time.Second},
	}
}

// Load reads a YAML file. ${VAR} references in scalar values are expanded from the
// environment after parsing, so secrets with YAML special characters stay intact.
// A value that expands to an empty string keeps its default.
func Load(path string) (Config, error) {
	data, err := os.ReadFile(path) //nolint:gosec // path comes from the operator
	if err != nil {
		return Config{}, fmt.Errorf("read config %s: %w", path, err)
	}
	var root yaml.Node
	if err := yaml.Unmarshal(data, &root); err != nil {
		return Config{}, fmt.Errorf("parse config %s: %w", path, err)
	}
	expandEnv(&root)

	cfg := defaults()
	if err := root.Decode(&cfg); err != nil {
		return Config{}, fmt.Errorf("decode config %s: %w", path, err)
	}
	cfg.Kafka.Brokers = splitList(cfg.Kafka.Brokers)
	if err := cfg.validate(); err != nil {
		return Config{}, fmt.Errorf("invalid config %s: %w", path, err)
	}
	return cfg, nil
}

func expandEnv(n *yaml.Node) {
	for _, child := range n.Content {
		expandEnv(child)
	}
	if n.Kind != yaml.ScalarNode || !strings.Contains(n.Value, "$") {
		return
	}
	n.Value = os.ExpandEnv(n.Value)
	if n.Value == "" {
		n.Tag = "!!null"
		return
	}
	// Re-resolve the tag so "20" or "true" decode into numbers and bools, but never turn a value into null.
	n.Tag, n.Style = "", 0
	if n.ShortTag() == "!!null" {
		n.Tag = "!!str"
	}
}

func splitList(items []string) []string {
	var out []string
	for _, item := range items {
		for part := range strings.SplitSeq(item, ",") {
			if part = strings.TrimSpace(part); part != "" {
				out = append(out, part)
			}
		}
	}
	return out
}

func (c Config) validate() error {
	var errs []error
	if c.Postgres.DSN == "" {
		errs = append(errs, errors.New("postgres.dsn is required"))
	}
	if len(c.Kafka.Brokers) == 0 {
		errs = append(errs, errors.New("kafka.brokers is required"))
	}
	if c.Kafka.Topic == "" {
		errs = append(errs, errors.New("kafka.topic is required"))
	}
	if c.Outbox.BatchSize <= 0 || c.Outbox.PollInterval <= 0 || c.Outbox.BacklogInterval <= 0 {
		errs = append(errs, errors.New("outbox.batch_size, outbox.poll_interval and outbox.backlog_interval must be positive"))
	}
	if strings.EqualFold(c.Kafka.SASL.Mechanism, "PLAIN") && !c.Kafka.TLS.Enabled {
		errs = append(errs, errors.New("kafka.sasl.mechanism PLAIN sends the password in clear text: enable kafka.tls"))
	}
	return errors.Join(errs...)
}

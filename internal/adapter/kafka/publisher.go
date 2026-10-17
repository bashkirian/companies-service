package kafka

import (
	"context"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"fmt"
	"os"
	"strings"
	"time"

	"github.com/twmb/franz-go/pkg/kgo"
	"github.com/twmb/franz-go/pkg/sasl"
	"github.com/twmb/franz-go/pkg/sasl/plain"
	"github.com/twmb/franz-go/pkg/sasl/scram"

	"github.com/bashkirian/companies-service/internal/usecase/outbox"
)

const (
	headerEventID   = "event_id"
	headerEventType = "event_type"
)

type Config struct {
	Brokers  []string
	Topic    string
	ClientID string
	TLS      TLSConfig
	SASL     SASLConfig
}

type TLSConfig struct {
	Enabled bool
	// CAFile overrides system roots. CertFile and KeyFile enable mTLS.
	CAFile     string
	CertFile   string
	KeyFile    string
	ServerName string
}

type SASLConfig struct {
	// Mechanism is empty (disabled), PLAIN, SCRAM-SHA-256 or SCRAM-SHA-512.
	Mechanism string
	Username  string
	Password  string
}

// Publisher writes outbox messages to Kafka. The client is idempotent with acks=all,
// and messages are keyed by company ID so all events of a company land in one partition.
type Publisher struct {
	client *kgo.Client
	topic  string
}

func NewPublisher(cfg Config) (*Publisher, error) {
	opts := []kgo.Opt{
		kgo.SeedBrokers(cfg.Brokers...),
		kgo.ClientID(cfg.ClientID),
		kgo.RequiredAcks(kgo.AllISRAcks()),
		kgo.ProducerLinger(5 * time.Millisecond),
		kgo.RecordPartitioner(kgo.StickyKeyPartitioner(nil)),
	}
	if cfg.TLS.Enabled {
		tlsCfg, err := buildTLS(cfg.TLS)
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.DialTLSConfig(tlsCfg))
	}
	if cfg.SASL.Mechanism != "" {
		mech, err := buildSASL(cfg.SASL)
		if err != nil {
			return nil, err
		}
		opts = append(opts, kgo.SASL(mech))
	}

	client, err := kgo.NewClient(opts...)
	if err != nil {
		return nil, fmt.Errorf("create kafka client: %w", err)
	}
	return &Publisher{client: client, topic: cfg.Topic}, nil
}

func (p *Publisher) Publish(ctx context.Context, msgs []outbox.Message) error {
	records := make([]*kgo.Record, len(msgs))
	for i, m := range msgs {
		records[i] = &kgo.Record{
			Topic: p.topic,
			Key:   []byte(m.Key),
			Value: m.Payload,
			Headers: []kgo.RecordHeader{
				{Key: headerEventID, Value: []byte(m.ID.String())},
				{Key: headerEventType, Value: []byte(m.Type)},
			},
		}
	}
	if err := p.client.ProduceSync(ctx, records...).FirstErr(); err != nil {
		return fmt.Errorf("produce: %w", err)
	}
	return nil
}

func (p *Publisher) Close() {
	p.client.Close()
}

func buildTLS(cfg TLSConfig) (*tls.Config, error) {
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS12, ServerName: cfg.ServerName}
	if cfg.CAFile != "" {
		pem, err := os.ReadFile(cfg.CAFile)
		if err != nil {
			return nil, fmt.Errorf("read kafka CA: %w", err)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, errors.New("kafka CA file has no PEM certificates")
		}
		tlsCfg.RootCAs = pool
	}
	if cfg.CertFile != "" || cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return nil, fmt.Errorf("load kafka client certificate: %w", err)
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	return tlsCfg, nil
}

func buildSASL(cfg SASLConfig) (sasl.Mechanism, error) {
	switch strings.ToUpper(cfg.Mechanism) {
	case "PLAIN":
		return plain.Auth{User: cfg.Username, Pass: cfg.Password}.AsMechanism(), nil
	case "SCRAM-SHA-256":
		return scram.Auth{User: cfg.Username, Pass: cfg.Password}.AsSha256Mechanism(), nil
	case "SCRAM-SHA-512":
		return scram.Auth{User: cfg.Username, Pass: cfg.Password}.AsSha512Mechanism(), nil
	}
	return nil, fmt.Errorf("unsupported kafka SASL mechanism %q", cfg.Mechanism)
}

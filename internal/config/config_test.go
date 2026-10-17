package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestLoadProductionTemplate(t *testing.T) {
	secret := `a#b: &c *d {e} "f' \g` + "\n" + `h-very-long-secret-value`
	t.Setenv("POSTGRES_DSN", "postgres://u:p@db:5432/x")
	t.Setenv("KAFKA_BROKERS", "k1:9092, k2:9092")
	t.Setenv("KAFKA_TLS_ENABLED", "true")
	t.Setenv("KAFKA_SASL_PASSWORD", "null")
	t.Setenv("JWT_SECRET", secret)
	t.Setenv("LOG_LEVEL", "")

	cfg, err := Load(filepath.Join("..", "..", "configs", "config.yaml"))
	require.NoError(t, err)

	assert.Equal(t, secret, cfg.JWT.Secret, "secret with YAML special characters must survive")
	assert.Equal(t, []string{"k1:9092", "k2:9092"}, cfg.Kafka.Brokers)
	assert.True(t, cfg.Kafka.TLS.Enabled)
	assert.Equal(t, "null", cfg.Kafka.SASL.Password, "env value must never become YAML null")
	assert.Empty(t, cfg.Kafka.SASL.Mechanism)
	assert.Equal(t, "info", cfg.Log.Level, "empty env keeps the default")
	assert.Equal(t, "HS256", cfg.JWT.Algorithm)
	assert.Equal(t, ":9090", cfg.HTTP.AdminAddr)
	assert.Equal(t, 15*time.Second, cfg.Outbox.BacklogInterval)
}

func TestLoadRejectsMissingRequired(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	require.NoError(t, os.WriteFile(path, []byte("postgres:\n  dsn: ${UNSET_DSN_FOR_TEST}\n"), 0o600))

	_, err := Load(path)
	require.Error(t, err)
	assert.Contains(t, err.Error(), "postgres.dsn is required")
	assert.Contains(t, err.Error(), "kafka.brokers is required")
}

func TestLoadRejectsPlainSASLWithoutTLS(t *testing.T) {
	path := filepath.Join(t.TempDir(), "c.yaml")
	body := "postgres: {dsn: x}\nkafka: {brokers: [k:9092], sasl: {mechanism: plain, username: u, password: p}}\n"
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))

	_, err := Load(path)
	require.ErrorContains(t, err, "clear text")

	body = strings.Replace(body, "sasl:", "tls: {enabled: true}, sasl:", 1)
	require.NoError(t, os.WriteFile(path, []byte(body), 0o600))
	_, err = Load(path)
	require.NoError(t, err)
}

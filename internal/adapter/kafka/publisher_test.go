package kafka

import (
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func TestBuildSASL(t *testing.T) {
	for mech, name := range map[string]string{"plain": "PLAIN", "SCRAM-SHA-256": "SCRAM-SHA-256", "scram-sha-512": "SCRAM-SHA-512"} {
		m, err := buildSASL(SASLConfig{Mechanism: mech, Username: "u", Password: "p"})
		require.NoError(t, err, mech)
		assert.Equal(t, name, m.Name())
	}
	_, err := buildSASL(SASLConfig{Mechanism: "GSSAPI"})
	assert.Error(t, err)
}

func TestBuildTLSRejectsBadCA(t *testing.T) {
	_, err := buildTLS(TLSConfig{Enabled: true, CAFile: "testdata/missing.pem"})
	assert.Error(t, err)

	cfg, err := buildTLS(TLSConfig{Enabled: true, ServerName: "kafka.internal"})
	require.NoError(t, err)
	assert.Equal(t, "kafka.internal", cfg.ServerName)
}

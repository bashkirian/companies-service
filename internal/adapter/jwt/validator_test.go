package jwt

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

const (
	testSecret = "test-secret-at-least-32-bytes-long!!"
	testIssuer = "issuer"
	testAud    = "companies"
)

func validClaims() jwt.MapClaims {
	return jwt.MapClaims{
		"sub": "user-1",
		"iss": testIssuer,
		"aud": testAud,
		"exp": time.Now().Add(time.Hour).Unix(),
	}
}

func with(c jwt.MapClaims, key string, val any) jwt.MapClaims {
	if val == nil {
		delete(c, key)
	} else {
		c[key] = val
	}
	return c
}

func sign(t *testing.T, method jwt.SigningMethod, key any, claims jwt.MapClaims) string {
	t.Helper()
	s, err := jwt.NewWithClaims(method, claims).SignedString(key)
	require.NoError(t, err)
	return s
}

func rsaKey(t *testing.T) (*rsa.PrivateKey, string, []byte) {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	der, err := x509.MarshalPKIXPublicKey(&key.PublicKey)
	require.NoError(t, err)
	pubPEM := pem.EncodeToMemory(&pem.Block{Type: "PUBLIC KEY", Bytes: der})
	path := filepath.Join(t.TempDir(), "pub.pem")
	require.NoError(t, os.WriteFile(path, pubPEM, 0o600))
	return key, path, pubPEM
}

func TestValidate(t *testing.T) {
	rsaPriv, pubPath, pubPEM := rsaKey(t)
	otherRSA, _, _ := rsaKey(t)

	hs, err := NewValidator(Config{Algorithm: "HS256", Secret: testSecret, Issuer: testIssuer, Audience: testAud})
	require.NoError(t, err)
	rs, err := NewValidator(Config{Algorithm: "RS256", PublicKeyFile: pubPath, Issuer: testIssuer, Audience: testAud})
	require.NoError(t, err)
	lenient, err := NewValidator(Config{Algorithm: "HS256", Secret: testSecret, Leeway: time.Minute})
	require.NoError(t, err)

	tests := []struct {
		name      string
		validator *Validator
		token     string
		ok        bool
	}{
		{"HS256 valid", hs, sign(t, jwt.SigningMethodHS256, []byte(testSecret), validClaims()), true},
		{"RS256 valid", rs, sign(t, jwt.SigningMethodRS256, rsaPriv, validClaims()), true},
		{"expired", hs, sign(t, jwt.SigningMethodHS256, []byte(testSecret),
			with(validClaims(), "exp", time.Now().Add(-time.Second).Unix())), false},
		{"expired within leeway", lenient, sign(t, jwt.SigningMethodHS256, []byte(testSecret),
			with(validClaims(), "exp", time.Now().Add(-10*time.Second).Unix())), true},
		{"no exp", hs, sign(t, jwt.SigningMethodHS256, []byte(testSecret), with(validClaims(), "exp", nil)), false},
		{"wrong issuer", hs, sign(t, jwt.SigningMethodHS256, []byte(testSecret), with(validClaims(), "iss", "evil")), false},
		{"wrong audience", hs, sign(t, jwt.SigningMethodHS256, []byte(testSecret), with(validClaims(), "aud", "other")), false},
		{"empty sub", hs, sign(t, jwt.SigningMethodHS256, []byte(testSecret), with(validClaims(), "sub", "")), false},
		{"wrong secret", hs, sign(t, jwt.SigningMethodHS256, []byte(testSecret+"x"), validClaims()), false},
		{"RS256 token on HS256 validator", hs, sign(t, jwt.SigningMethodRS256, rsaPriv, validClaims()), false},
		// Classic algorithm confusion: HMAC keyed with the public key the server trusts.
		{"HS256 token on RS256 validator", rs, sign(t, jwt.SigningMethodHS256, pubPEM, validClaims()), false},
		{"RS256 foreign key", rs, sign(t, jwt.SigningMethodRS256, otherRSA, validClaims()), false},
		{"alg none", hs, sign(t, jwt.SigningMethodNone, jwt.UnsafeAllowNoneSignatureType, validClaims()), false},
		{"garbage", hs, "not.a.token", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			claims, err := tt.validator.Validate(tt.token)
			if !tt.ok {
				require.ErrorIs(t, err, ErrInvalidToken)
				return
			}
			require.NoError(t, err)
			assert.Equal(t, "user-1", claims.Subject)
		})
	}
}

func TestNewValidatorRejectsBadConfig(t *testing.T) {
	for name, cfg := range map[string]Config{
		"short secret":    {Algorithm: "HS256", Secret: "short"},
		"unsupported alg": {Algorithm: "none", Secret: testSecret},
		"missing key":     {Algorithm: "RS256", PublicKeyFile: filepath.Join(t.TempDir(), "absent.pem")},
	} {
		_, err := NewValidator(cfg)
		assert.Error(t, err, name)
	}
}

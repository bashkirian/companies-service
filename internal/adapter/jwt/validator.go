package jwt

import (
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

var ErrInvalidToken = errors.New("invalid token")

type Config struct {
	// Algorithm is HS256 (shared Secret) or RS256 (PublicKeyFile in PEM).
	Algorithm     string
	Secret        string
	PublicKeyFile string
	Issuer        string
	Audience      string
	Leeway        time.Duration
}

type Claims struct {
	Subject string
}

// Validator only verifies tokens; issuing them is the identity provider's job.
type Validator struct {
	key    any
	parser *jwt.Parser
}

func NewValidator(cfg Config) (*Validator, error) {
	opts := []jwt.ParserOption{
		jwt.WithValidMethods([]string{cfg.Algorithm}),
		jwt.WithExpirationRequired(),
		jwt.WithLeeway(cfg.Leeway),
	}
	if cfg.Issuer != "" {
		opts = append(opts, jwt.WithIssuer(cfg.Issuer))
	}
	if cfg.Audience != "" {
		opts = append(opts, jwt.WithAudience(cfg.Audience))
	}

	var key any
	switch cfg.Algorithm {
	case "HS256":
		if len(cfg.Secret) < 32 {
			return nil, errors.New("jwt secret must be at least 32 bytes")
		}
		key = []byte(cfg.Secret)
	case "RS256":
		pem, err := os.ReadFile(cfg.PublicKeyFile)
		if err != nil {
			return nil, fmt.Errorf("read jwt public key: %w", err)
		}
		if key, err = jwt.ParseRSAPublicKeyFromPEM(pem); err != nil {
			return nil, fmt.Errorf("parse jwt public key: %w", err)
		}
	default:
		return nil, fmt.Errorf("unsupported jwt algorithm %q", cfg.Algorithm)
	}
	return &Validator{key: key, parser: jwt.NewParser(opts...)}, nil
}

func (v *Validator) Validate(raw string) (Claims, error) {
	var claims jwt.RegisteredClaims
	if _, err := v.parser.ParseWithClaims(raw, &claims, func(*jwt.Token) (any, error) { return v.key, nil }); err != nil {
		return Claims{}, fmt.Errorf("%w: %w", ErrInvalidToken, err)
	}
	if claims.Subject == "" {
		return Claims{}, fmt.Errorf("%w: missing sub", ErrInvalidToken)
	}
	return Claims{Subject: claims.Subject}, nil
}

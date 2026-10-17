package observability

import (
	"fmt"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

type LogConfig struct {
	Level string
	// Format is "json" for production or "console" for local runs.
	Format string
}

func NewLogger(cfg LogConfig) (*zap.Logger, error) {
	var zc zap.Config
	if cfg.Format == "console" {
		zc = zap.NewDevelopmentConfig()
	} else {
		zc = zap.NewProductionConfig()
		zc.EncoderConfig.EncodeTime = zapcore.ISO8601TimeEncoder
	}
	level, err := zapcore.ParseLevel(cfg.Level)
	if err != nil {
		return nil, fmt.Errorf("parse log level: %w", err)
	}
	zc.Level = zap.NewAtomicLevelAt(level)
	return zc.Build()
}

package common

import (
	"os"

	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"
)

var Logger *zap.Logger

// InitLogger initializes the zap logger
func InitLogger() error {
	var config zap.Config

	env := os.Getenv("APP_ENV")
	if env == "production" {
		config = zap.NewProductionConfig()
	} else {
		config = zap.NewDevelopmentConfig()
		config.EncoderConfig.EncodeLevel = zapcore.CapitalColorLevelEncoder
	}

	var err error
	Logger, err = config.Build()
	if err != nil {
		return err
	}

	return nil
}

// SyncLogger flushes any buffered log entries
func SyncLogger() {
	if Logger != nil {
		_ = Logger.Sync()
	}
}


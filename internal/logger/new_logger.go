package logger

import "go.uber.org/zap"

func NewLogger(loggerLevel string) *zap.Logger {
	var logger *zap.Logger

	switch loggerLevel {
	case "debug":
		logger = zap.NewExample()
	}

	return logger
}

package log

import (
	"context"
	"log/slog"
	"os"
)

type ctxKey struct{}

var LoggerKey = ctxKey{}

var defaultLogger *slog.Logger

var logLevel = new(slog.LevelVar)

// init optional lazy initialization
func init() {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: false,
		Level:     logLevel,
	})
	defaultLogger = slog.New(jsonHandler)
	slog.SetDefault(defaultLogger)
}

// InitLogger comment init() if use this initial function
func InitLogger(level slog.Level) *slog.Logger {
	jsonHandler := slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{
		AddSource: false,
		Level:     level,
	})
	logger := slog.New(jsonHandler)
	defaultLogger = logger

	slog.SetDefault(logger)
	return logger
}

func LoggerFrom(ctx context.Context) *slog.Logger {
	if l, ok := ctx.Value(LoggerKey).(*slog.Logger); ok {
		return l
	}
	return defaultLogger
}

func ContextWithLogger(ctx context.Context, logger *slog.Logger) context.Context {
	return context.WithValue(ctx, LoggerKey, logger)
}

func SetLogLevel(level slog.Level) {
	logLevel.Set(level)
}

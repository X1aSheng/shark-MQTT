package logger

import (
	"log/slog"
	"os"
)

// slogLogger implements Logger using the standard slog package.
type slogLogger struct {
	logger *slog.Logger
}

// NewSlogLogger creates a new slog-based logger.
func NewSlogLogger(level string) Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}
	opts := &slog.HandlerOptions{Level: lvl}
	h := slog.NewTextHandler(os.Stderr, opts)
	return &slogLogger{logger: slog.New(h)}
}

func (l *slogLogger) Debug(msg string, keyvals ...any) {
	l.logger.Debug(msg, keyvals...)
}

func (l *slogLogger) Info(msg string, keyvals ...any) {
	l.logger.Info(msg, keyvals...)
}

func (l *slogLogger) Warn(msg string, keyvals ...any) {
	l.logger.Warn(msg, keyvals...)
}

func (l *slogLogger) Error(msg string, keyvals ...any) {
	l.logger.Error(msg, keyvals...)
}

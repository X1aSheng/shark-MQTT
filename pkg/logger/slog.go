package logger

import (
	"log"
	"log/slog"
	"os"
)

func init() {
	log.SetFlags(log.LstdFlags | log.Lmicroseconds)
}

// slogLogger implements Logger using the standard slog package.
type slogLogger struct {
	logger *slog.Logger
}

// NewSlogLogger creates a new slog-based logger.
// level: debug, info, warn, error
// format: "json" for JSON output, "" for text
func NewSlogLogger(level, format string) Logger {
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
	var h slog.Handler
	if format == "json" {
		h = slog.NewJSONHandler(os.Stderr, opts)
	} else {
		h = slog.NewTextHandler(os.Stderr, opts)
	}
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

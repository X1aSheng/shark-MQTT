// Package logger provides a structured logging interface for shark-mqtt.
package logger

// Logger interface for structured logging.
type Logger interface {
	Debug(msg string, keyvals ...any)
	Info(msg string, keyvals ...any)
	Warn(msg string, keyvals ...any)
	Error(msg string, keyvals ...any)
}

// Option configures a Logger.
type Option func(*options)

type options struct {
	level  string
	format string // "json" or "" (text)
}

// New creates a new Logger with the given options.
func New(opts ...Option) Logger {
	o := &options{level: "info"}
	for _, opt := range opts {
		opt(o)
	}
	return NewSlogLogger(o.level, o.format)
}

// Default returns the default logger.
func Default() Logger {
	return NewSlogLogger("info", "")
}

// WithLevel sets the log level.
func WithLevel(level string) Option {
	return func(o *options) { o.level = level }
}

// WithFormat sets the log output format ("json" or "" for text).
func WithFormat(format string) Option {
	return func(o *options) { o.format = format }
}

// Noop returns a no-op logger.
func Noop() Logger {
	return &noopLogger{}
}

type noopLogger struct{}

func (n *noopLogger) Debug(_ string, _ ...any) {}
func (n *noopLogger) Info(_ string, _ ...any)  {}
func (n *noopLogger) Warn(_ string, _ ...any)  {}
func (n *noopLogger) Error(_ string, _ ...any) {}

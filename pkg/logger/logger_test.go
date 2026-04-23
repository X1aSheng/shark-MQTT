package logger

import (
	"testing"
)

func TestNoopLogger(t *testing.T) {
	l := Noop()
	if l == nil {
		t.Fatal("Noop() returned nil")
	}

	// Verify no panics
	l.Debug("test", "key", "value")
	l.Info("test", "key", "value")
	l.Warn("test", "key", "value")
	l.Error("test", "key", "value")
}

func TestDefaultLogger(t *testing.T) {
	l := Default()
	if l == nil {
		t.Fatal("Default() returned nil")
	}

	// Verify no panics
	l.Debug("test")
	l.Info("test")
	l.Warn("test")
	l.Error("test")
}

func TestNewWithLevel(t *testing.T) {
	tests := []struct {
		level string
	}{
		{"debug"},
		{"info"},
		{"warn"},
		{"error"},
		{"unknown"}, // should default to info
	}

	for _, tt := range tests {
		t.Run(tt.level, func(t *testing.T) {
			l := New(WithLevel(tt.level))
			if l == nil {
				t.Fatal("New(WithLevel()) returned nil")
			}
			// Should not panic
			l.Debug("test")
			l.Info("test")
			l.Warn("test")
			l.Error("test")
		})
	}
}

func TestNewNoOptions(t *testing.T) {
	l := New()
	if l == nil {
		t.Fatal("New() with no options returned nil")
	}
	l.Info("default level should be info")
}

func TestLoggerInterface(t *testing.T) {
	var _ Logger = &slogLogger{}
	var _ Logger = &noopLogger{}
}

func TestWithLevelOption(t *testing.T) {
	opts := &options{level: "info"}
	opt := WithLevel("debug")
	opt(opts)
	if opts.level != "debug" {
		t.Errorf("expected level debug, got %s", opts.level)
	}
}

func TestMultipleOptions(t *testing.T) {
	// Currently only one option exists, but verify multiple options don't panic
	l := New(WithLevel("debug"))
	if l == nil {
		t.Fatal("New with multiple options returned nil")
	}
}

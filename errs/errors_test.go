package errs

import (
	"errors"
	"testing"
)

func TestErrorSentinels(t *testing.T) {
	sentinels := []struct {
		name string
		err  error
		want string
	}{
		{"ErrSessionClosed", ErrSessionClosed, "session closed"},
		{"ErrSessionNotFound", ErrSessionNotFound, "session not found"},
		{"ErrWriteQueueFull", ErrWriteQueueFull, "write queue full"},
		{"ErrClientIDEmpty", ErrClientIDEmpty, "client id is empty"},
		{"ErrClientIDConflict", ErrClientIDConflict, "client id already connected"},
		{"ErrInvalidPacket", ErrInvalidPacket, "invalid packet format"},
		{"ErrUnsupportedVersion", ErrUnsupportedVersion, "unsupported protocol version"},
		{"ErrIncomplete", ErrIncomplete, "incomplete packet"},
		{"ErrPacketTooLarge", ErrPacketTooLarge, "packet exceeds maximum size"},
		{"ErrAuthFailed", ErrAuthFailed, "authentication failed"},
		{"ErrNotAuthorized", ErrNotAuthorized, "not authorized"},
		{"ErrInflightFull", ErrInflightFull, "inflight queue full"},
		{"ErrDuplicatePacketID", ErrDuplicatePacketID, "duplicate packet id"},
		{"ErrInvalidQoS", ErrInvalidQoS, "invalid qos level"},
		{"ErrStoreUnavailable", ErrStoreUnavailable, "store unavailable"},
		{"ErrSessionExpired", ErrSessionExpired, "session expired"},
		{"ErrServerClosed", ErrServerClosed, "server closed"},
		{"ErrAlreadyStarted", ErrAlreadyStarted, "already started"},
	}

	for _, tt := range sentinels {
		t.Run(tt.name, func(t *testing.T) {
			if tt.err == nil {
				t.Fatalf("error sentinel is nil")
			}
			if tt.err.Error() != tt.want {
				t.Errorf("error message = %q, want %q", tt.err.Error(), tt.want)
			}
			if !errors.Is(tt.err, tt.err) {
				t.Errorf("errors.Is(err, err) = false, want true")
			}
		})
	}
}

func TestErrorSentinelsAreDistinct(t *testing.T) {
	sentinels := []error{
		ErrSessionClosed, ErrSessionNotFound, ErrWriteQueueFull,
		ErrClientIDEmpty, ErrClientIDConflict, ErrInvalidPacket,
		ErrUnsupportedVersion, ErrIncomplete, ErrPacketTooLarge,
		ErrAuthFailed, ErrNotAuthorized, ErrInflightFull,
		ErrDuplicatePacketID, ErrInvalidQoS, ErrStoreUnavailable,
		ErrSessionExpired, ErrServerClosed, ErrAlreadyStarted,
	}

	for i := 0; i < len(sentinels); i++ {
		for j := i + 1; j < len(sentinels); j++ {
			if sentinels[i] == sentinels[j] {
				t.Errorf("sentinels[%d] and sentinels[%d] are the same error", i, j)
			}
			if sentinels[i].Error() == sentinels[j].Error() {
				t.Errorf("sentinels[%d] and sentinels[%d] have the same message: %q", i, j, sentinels[i].Error())
			}
		}
	}
}

func TestErrorWrapping(t *testing.T) {
	wrapped := errors.Join(ErrSessionClosed, ErrClientIDEmpty)
	if !errors.Is(wrapped, ErrSessionClosed) {
		t.Error("wrapped error should contain ErrSessionClosed")
	}
	if !errors.Is(wrapped, ErrClientIDEmpty) {
		t.Error("wrapped error should contain ErrClientIDEmpty")
	}
}

func TestErrorSentinel(t *testing.T) {
	err := ErrAuthFailed
	if err.Error() != "authentication failed" {
		t.Errorf("unexpected error message: %s", err.Error())
	}
}

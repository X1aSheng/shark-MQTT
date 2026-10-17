package broker

import (
	"testing"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestStatsMergeDoesNotDoubleCount pins the accounting contract after
// TrackSent moved off the session lock.
//
// TrackSent now bumps two atomic counters instead of writing s.stats under
// s.mu, because it runs once per message per subscriber and taking the write
// lock there serialised every subscriber of the same message. Stats merges the
// cumulative atomics into the snapshot, which means two properties have to hold:
// the count is right, and reading the snapshot twice does not inflate it.
func TestStatsMergeDoesNotDoubleCount(t *testing.T) {
	mgr := NewManager(nil)
	s := mgr.CreateSession("stats-client", &protocol.ConnectPacket{
		ClientID: "stats-client",
		Flags:    protocol.ConnectFlags{CleanSession: true},
	}, false)

	s.TrackSent(100)
	s.TrackSent(50)

	first := s.Stats()
	if first.MessagesSent != 2 {
		t.Fatalf("MessagesSent = %d, want 2", first.MessagesSent)
	}
	if first.BytesSent != 150 {
		t.Fatalf("BytesSent = %d, want 150", first.BytesSent)
	}

	// The snapshot is a read, not a drain: a management endpoint polling stats
	// must not shift the numbers it reports.
	second := s.Stats()
	if second.MessagesSent != 2 {
		t.Fatalf("second read MessagesSent = %d, want 2 (snapshot must not accumulate)", second.MessagesSent)
	}
	if second.BytesSent != 150 {
		t.Fatalf("second read BytesSent = %d, want 150", second.BytesSent)
	}

	s.TrackSent(10)
	third := s.Stats()
	if third.MessagesSent != 3 {
		t.Fatalf("MessagesSent after another send = %d, want 3", third.MessagesSent)
	}
	if third.BytesSent != 160 {
		t.Fatalf("BytesSent after another send = %d, want 160", third.BytesSent)
	}
}

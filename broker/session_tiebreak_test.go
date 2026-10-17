package broker

import (
	"testing"

	"github.com/X1aSheng/shark-mqtt/protocol"
)

// TestMatchSubscriptionsTieBreakIsStable pins the deterministic choice between
// filters that match at the same QoS.
//
// Two overlapping subscriptions at the same QoS carry different
// RetainAsPublished / RetainHandling values, and before this fix whichever one
// map iteration happened to reach first supplied the options. Two identical
// publishes could therefore deliver with different retain semantics. The
// tie-break is lexicographic on the filter, which is arbitrary but stable -
// stability is the entire requirement.
func TestMatchSubscriptionsTieBreakIsStable(t *testing.T) {
	s := NewManager(nil).CreateSession("tie", &protocol.ConnectPacket{
		ClientID: "tie",
		Flags:    protocol.ConnectFlags{CleanSession: true},
	}, false)

	// "a/b" and "a/+" both match "a/b" at QoS 0, with different
	// RetainAsPublished. Which one wins is unspecified, so the test asserts the
	// property that matters: the answer does not change between calls.
	s.Subscriptions["a/b"] = 0
	s.Subscriptions["a/+"] = 0
	s.SubOptions["a/b"] = SubscriptionOptions{RetainAsPublished: true}
	s.SubOptions["a/+"] = SubscriptionOptions{RetainAsPublished: false}

	_, _, first, _ := s.matchSubscriptions("a/b", false)
	for i := 0; i < 100; i++ {
		matched, qos, opts, _ := s.matchSubscriptions("a/b", false)
		if !matched {
			t.Fatalf("run %d: expected a match", i)
		}
		if qos != 0 {
			t.Fatalf("run %d: qos = %d, want 0", i, qos)
		}
		if opts != first {
			t.Fatalf("run %d: options changed between calls (%+v vs %+v) - tie-break is not stable",
				i, opts, first)
		}
	}
}

// TestMatchSubscriptionsPrefersHigherQoS keeps the original rule intact: the
// highest QoS among matching filters wins, regardless of filter order.
func TestMatchSubscriptionsPrefersHigherQoS(t *testing.T) {
	s := NewManager(nil).CreateSession("qos", &protocol.ConnectPacket{
		ClientID: "qos",
		Flags:    protocol.ConnectFlags{CleanSession: true},
	}, false)

	s.Subscriptions["a/b"] = 0
	s.Subscriptions["a/+"] = 2
	s.SubOptions["a/b"] = SubscriptionOptions{RetainAsPublished: true}
	s.SubOptions["a/+"] = SubscriptionOptions{RetainAsPublished: false}

	for i := 0; i < 50; i++ {
		_, qos, opts, _ := s.matchSubscriptions("a/b", false)
		if qos != 2 {
			t.Fatalf("run %d: qos = %d, want 2 (highest matching subscription)", i, qos)
		}
		if opts.RetainAsPublished {
			t.Fatalf("run %d: options came from the QoS 0 filter", i)
		}
	}
}

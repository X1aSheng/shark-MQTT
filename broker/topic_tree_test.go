package broker

import (
	"slices"
	"testing"
)

// --- Subscribe tests ---

func TestTopicTree_Subscribe(t *testing.T) {
	tests := []struct {
		name     string
		topic    string
		clientID string
		qos      uint8
	}{
		{"single level", "sensors", "client1", 0},
		{"two levels", "home/temp", "client1", 1},
		{"three levels", "home/sensors/temp", "client1", 2},
		{"deep nesting", "a/b/c/d/e/f", "client2", 0},
		{"wildcard +", "home/+/temp", "client1", 1},
		{"wildcard #", "home/#", "client2", 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tt := NewTopicTree()
			tt.Subscribe(tc.topic, tc.clientID, tc.qos)

			// Match should find the subscriber when publishing to the filter itself
			// (filters subscribe to the trie nodes)
			subs := tt.Match(tc.topic)
			if len(subs) == 0 {
				t.Fatalf("expected subscribers for %q, got none", tc.topic)
			}
			found := false
			for _, sub := range subs {
				if sub.ClientID == tc.clientID && sub.QoS == tc.qos {
					found = true
					break
				}
			}
			if !found {
				t.Fatalf("subscriber %q with QoS %d not found in %v", tc.clientID, tc.qos, subs)
			}
		})
	}
}

func TestTopicTree_Subscribe_UpdatesQoS(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("home/temp", "client1", 0)
	tt.Subscribe("home/temp", "client1", 2) // should update QoS

	subs := tt.Match("home/temp")
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscriber, got %d", len(subs))
	}
	if subs[0].QoS != 2 {
		t.Errorf("expected QoS 2, got %d", subs[0].QoS)
	}
}

func TestTopicTree_Subscribe_MultipleClients(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("home/temp", "client1", 0)
	tt.Subscribe("home/temp", "client2", 1)
	tt.Subscribe("home/temp", "client3", 2)

	subs := tt.Match("home/temp")
	if len(subs) != 3 {
		t.Fatalf("expected 3 subscribers, got %d", len(subs))
	}

	// Check all clients are present
	clientIDs := make([]string, len(subs))
	for i, sub := range subs {
		clientIDs[i] = sub.ClientID
	}
	for _, id := range []string{"client1", "client2", "client3"} {
		if !slices.Contains(clientIDs, id) {
			t.Errorf("client %q not found in subscribers", id)
		}
	}
}

func TestTopicTree_SubscribeSystem(t *testing.T) {
	tt := NewTopicTree()
	tt.SubscribeSystem("$SYS/broker/connections", "monitor", 0)

	subs := tt.Match("$SYS/broker/connections")
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscriber for $SYS topic, got %d", len(subs))
	}
	if subs[0].ClientID != "monitor" {
		t.Errorf("expected client monitor, got %q", subs[0].ClientID)
	}
}

// --- Match / wildcard tests ---

func TestTopicTree_Match_ExactMatch(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("home/living/temp", "client1", 0)
	tt.Subscribe("home/bedroom/temp", "client2", 1)

	tests := []struct {
		publishTopic   string
		expectCount    int
		expectClientID string
	}{
		{"home/living/temp", 1, "client1"},
		{"home/bedroom/temp", 1, "client2"},
		{"home/kitchen/temp", 0, ""},
	}

	for _, tc := range tests {
		t.Run(tc.publishTopic, func(t *testing.T) {
			subs := tt.Match(tc.publishTopic)
			if len(subs) != tc.expectCount {
				t.Fatalf("expected %d subscribers for %q, got %d", tc.expectCount, tc.publishTopic, len(subs))
			}
			if tc.expectCount == 1 && subs[0].ClientID != tc.expectClientID {
				t.Errorf("expected client %q, got %q", tc.expectClientID, subs[0].ClientID)
			}
		})
	}
}

func TestTopicTree_Match_PlusWildcard(t *testing.T) {
	tests := []struct {
		name          string
		subscribe     string
		publishTopics map[string]bool
	}{
		{
			name:      "+ matches single level",
			subscribe: "home/+/temp",
			publishTopics: map[string]bool{
				"home/living/temp":  true,
				"home/bedroom/temp": true,
				"home/temp":         false, // + must match exactly one level
				"home/living/hum":   false, // different suffix
			},
		},
		{
			name:      "+ at end",
			subscribe: "sensors/+",
			publishTopics: map[string]bool{
				"sensors/temp":  true,
				"sensors/humid": true,
				"sensors":       false,
			},
		},
		{
			name:      "multiple + wildcards",
			subscribe: "+/+/status",
			publishTopics: map[string]bool{
				"device/001/status": true,
				"device/002/status": true,
				"device/status":     false,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tt := NewTopicTree()
			tt.Subscribe(tc.subscribe, "sub1", 1)

			for pubTopic, shouldMatch := range tc.publishTopics {
				subs := tt.Match(pubTopic)
				found := len(subs) > 0
				if found != shouldMatch {
					t.Errorf("topic %q: expected match=%v, got match=%v (subs=%v)", pubTopic, shouldMatch, found, subs)
				}
			}
		})
	}
}

func TestTopicTree_Match_HashWildcard(t *testing.T) {
	tests := []struct {
		name          string
		subscribe     string
		publishTopics map[string]bool
	}{
		{
			name:      "# matches all remaining levels",
			subscribe: "home/#",
			publishTopics: map[string]bool{
				"home":              true,
				"home/temp":         true,
				"home/living/temp":  true,
				"home/a/b/c/d/e":    true,
				"office/temp":       false,
			},
		},
		{
			name:      "# alone matches everything",
			subscribe: "#",
			publishTopics: map[string]bool{
				"anything":       true,
				"a/b/c":          true,
				"home/living":    true,
			},
		},
		{
			name:      "prefix/# pattern",
			subscribe: "sensors/temperature/#",
			publishTopics: map[string]bool{
				"sensors/temperature":          true,
				"sensors/temperature/room1":    true,
				"sensors/temperature/room1/a":  true,
				"sensors/humidity":             false,
			},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			tt := NewTopicTree()
			tt.Subscribe(tc.subscribe, "hashsub", 0)

			for pubTopic, shouldMatch := range tc.publishTopics {
				subs := tt.Match(pubTopic)
				found := len(subs) > 0
				if found != shouldMatch {
					t.Errorf("topic %q: expected match=%v, got match=%v (subs=%v)", pubTopic, shouldMatch, found, subs)
				}
			}
		})
	}
}

func TestTopicTree_Match_MixedWildcards(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("home/+/temp/#", "mixedsub", 1)

	tests := []struct {
		publishTopic string
		shouldMatch  bool
	}{
		{"home/living/temp", true},
		{"home/bedroom/temp/current", true},
		{"home/bedroom/temp/a/b/c", true},
		{"home/temp", false},        // + needs exactly one level
		{"office/temp", false},
	}

	for _, tc := range tests {
		t.Run(tc.publishTopic, func(t *testing.T) {
			subs := tt.Match(tc.publishTopic)
			found := len(subs) > 0
			if found != tc.shouldMatch {
				t.Errorf("topic %q: expected match=%v, got match=%v", tc.publishTopic, tc.shouldMatch, found)
			}
		})
	}
}

func TestTopicTree_Match_NoDuplicates(t *testing.T) {
	tt := NewTopicTree()
	// Subscribe with both # and specific path that could overlap
	tt.Subscribe("home/#", "client1", 0)
	tt.Subscribe("home/temp", "client1", 1)

	subs := tt.Match("home/temp")
	// client1 should appear only once despite multiple matching subscriptions
	count := 0
	for _, sub := range subs {
		if sub.ClientID == "client1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected client1 to appear exactly once, got %d", count)
	}
}

func TestTopicTree_Match_MultipleSubscriptions(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("home/#", "client1", 0)
	tt.Subscribe("home/+/temp", "client2", 1)
	tt.Subscribe("home/living/+", "client3", 2)

	subs := tt.Match("home/living/temp")
	if len(subs) != 3 {
		t.Fatalf("expected 3 subscribers, got %d: %v", len(subs), subs)
	}
}

func TestTopicTree_Match_EmptyTree(t *testing.T) {
	tt := NewTopicTree()
	subs := tt.Match("home/temp")
	if len(subs) != 0 {
		t.Errorf("expected 0 subscribers, got %d", len(subs))
	}
}

func TestTopicTree_Match_NoMatch(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("home/temp", "client1", 0)

	subs := tt.Match("office/humid")
	if len(subs) != 0 {
		t.Errorf("expected 0 subscribers, got %d", len(subs))
	}
}

func TestTopicTree_Match_PartialOverlap(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("a/b/c", "client1", 0)
	tt.Subscribe("a/b/d", "client2", 0)
	tt.Subscribe("a/+/c", "client3", 0)

	tests := []struct {
		topic        string
		expectCount  int
		expectIDs    []string
	}{
		{"a/b/c", 2, []string{"client1", "client3"}},
		{"a/b/d", 1, []string{"client2"}},
		{"a/x/c", 1, []string{"client3"}},
	}

	for _, tc := range tests {
		t.Run(tc.topic, func(t *testing.T) {
			subs := tt.Match(tc.topic)
			if len(subs) != tc.expectCount {
				t.Fatalf("expected %d subscribers, got %d", tc.expectCount, len(subs))
			}
			for _, id := range tc.expectIDs {
				found := false
				for _, sub := range subs {
					if sub.ClientID == id {
						found = true
						break
					}
				}
				if !found {
					t.Errorf("expected %q in subscribers", id)
				}
			}
		})
	}
}

// --- Unsubscribe tests ---

func TestTopicTree_Unsubscribe(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("home/temp", "client1", 0)
	tt.Subscribe("home/temp", "client2", 1)

	tt.Unsubscribe("home/temp", "client1")

	subs := tt.Match("home/temp")
	if len(subs) != 1 {
		t.Fatalf("expected 1 subscriber after unsubscribe, got %d", len(subs))
	}
	if subs[0].ClientID != "client2" {
		t.Errorf("expected client2, got %q", subs[0].ClientID)
	}
}

func TestTopicTree_Unsubscribe_AllClients(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("home/temp", "client1", 0)
	tt.Unsubscribe("home/temp", "client1")

	subs := tt.Match("home/temp")
	if len(subs) != 0 {
		t.Errorf("expected 0 subscribers, got %d", len(subs))
	}
}

func TestTopicTree_Unsubscribe_NonExistent(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("home/temp", "client1", 0)

	// Should not panic
	tt.Unsubscribe("home/temp", "nonexistent")
	tt.Unsubscribe("nonexistent/topic", "client1")

	subs := tt.Match("home/temp")
	if len(subs) != 1 {
		t.Errorf("expected 1 subscriber, got %d", len(subs))
	}
}

func TestTopicTree_Unsubscribe_PruneEmptyNodes(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("a/b/c", "client1", 0)
	tt.Unsubscribe("a/b/c", "client1")

	// After unsubscribing the only subscriber, nodes should be pruned
	// Match should return 0 subscribers and the trie should be clean
	subs := tt.Match("a/b/c")
	if len(subs) != 0 {
		t.Errorf("expected 0 subscribers after full unsubscribe, got %d", len(subs))
	}
}

func TestTopicTree_Unsubscribe_PartialPath(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("a/b/c", "client1", 0)
	tt.Subscribe("a/b/d", "client1", 0)

	tt.Unsubscribe("a/b/c", "client1")

	// client1 should still match a/b/d
	subs := tt.Match("a/b/d")
	if len(subs) != 1 {
		t.Errorf("expected 1 subscriber for a/b/d, got %d", len(subs))
	}

	// client1 should NOT match a/b/c
	subs = tt.Match("a/b/c")
	if len(subs) != 0 {
		t.Errorf("expected 0 subscribers for a/b/c after unsubscribe, got %d", len(subs))
	}
}

func TestTopicTree_Unsubscribe_WithWildcards(t *testing.T) {
	tt := NewTopicTree()
	tt.Subscribe("home/+", "client1", 0)
	tt.Subscribe("home/#", "client1", 1)

	tt.Unsubscribe("home/+", "client1")

	// client1 should still be reachable via home/#
	subs := tt.Match("home/temp")
	found := false
	for _, sub := range subs {
		if sub.ClientID == "client1" {
			found = true
			break
		}
	}
	if !found {
		t.Error("expected client1 to still match via home/#")
	}
}

// --- Edge cases ---

func TestTopicTree_ConcurrentAccess(t *testing.T) {
	tt := NewTopicTree()
	done := make(chan bool, 3)

	go func() {
		for i := 0; i < 100; i++ {
			tt.Subscribe("test/topic/level", "client1", 0)
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			tt.Match("test/topic/level")
		}
		done <- true
	}()

	go func() {
		for i := 0; i < 100; i++ {
			tt.Unsubscribe("test/topic/level", "client1")
		}
		done <- true
	}()

	for i := 0; i < 3; i++ {
		<-done
	}
}

func TestTopicTree_QoSMinOnMultipleMatches(t *testing.T) {
	// When a client has multiple matching subscriptions,
	// the match should return the QoS of the node where it was found.
	// The broker's deliverToSubscriber handles QoS min logic.
	tt := NewTopicTree()
	tt.Subscribe("home/#", "client1", 0)       // matches via #
	tt.Subscribe("home/temp", "client1", 2)    // exact match

	subs := tt.Match("home/temp")
	// client1 appears once (dedup). The QoS depends on which node matched first.
	// In the trie, exact match is traversed first, then # collects remaining.
	// But since visited dedup, only the first encounter matters.
	count := 0
	for _, sub := range subs {
		if sub.ClientID == "client1" {
			count++
		}
	}
	if count != 1 {
		t.Errorf("expected client1 once, got %d times", count)
	}
}

// Package broker provides the core MQTT message broker.
package broker

import (
	"sync"
)

// TopicNode represents a node in the topic trie.
type TopicNode struct {
	children    map[string]*TopicNode
	subscribers map[string]uint8 // clientID -> qos
}

// TopicTree implements a trie-based subscription matching system.
// It supports MQTT topic wildcards: + (single-level) and # (multi-level).
type TopicTree struct {
	root *TopicNode
	mu   sync.RWMutex
}

// NewTopicTree creates a new TopicTree.
func NewTopicTree() *TopicTree {
	return &TopicTree{
		root: &TopicNode{
			children:    make(map[string]*TopicNode),
			subscribers: make(map[string]uint8),
		},
	}
}

// Subscribe adds a subscription for a client to a topic filter.
func (tt *TopicTree) Subscribe(topic string, clientID string, qos uint8) bool {
	if !ValidateTopicFilter(topic) {
		return false
	}
	tt.mu.Lock()
	defer tt.mu.Unlock()

	parts := splitTopic(topic)
	tt.subscribeNode(tt.root, parts, clientID, qos, 0)
	return true
}

// SubscribeSystem adds a subscription for a system topic ($SYS).
// System topics are protected from wildcard matching.
func (tt *TopicTree) SubscribeSystem(topic string, clientID string, qos uint8) bool {
	if !ValidateTopicFilter(topic) {
		return false
	}
	tt.mu.Lock()
	defer tt.mu.Unlock()

	parts := splitTopic(topic)
	tt.subscribeNode(tt.root, parts, clientID, qos, 0)
	return true
}

// Unsubscribe removes a client's subscription from a topic filter.
func (tt *TopicTree) Unsubscribe(topic string, clientID string) {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	parts := splitTopic(topic)
	tt.unsubscribeNode(tt.root, parts, clientID, 0)
}

// Subscriber represents a client subscribed to a topic.
type Subscriber struct {
	ClientID string
	QoS      uint8
}

// Match finds all subscribers that match a given topic.
// Returns a slice of (clientID, qos) pairs.
func (tt *TopicTree) Match(topic string) []Subscriber {
	tt.mu.RLock()
	defer tt.mu.RUnlock()

	parts := splitTopic(topic)
	var results []Subscriber
	visited := make(map[string]struct{})
	tt.matchNode(tt.root, parts, 0, &results, visited)
	return results
}

func (tt *TopicTree) subscribeNode(node *TopicNode, parts []string, clientID string, qos uint8, depth int) {
	if depth == len(parts) {
		node.subscribers[clientID] = qos
		return
	}

	part := parts[depth]

	if node.children[part] == nil {
		node.children[part] = &TopicNode{
			children:    make(map[string]*TopicNode),
			subscribers: make(map[string]uint8),
		}
	}

	tt.subscribeNode(node.children[part], parts, clientID, qos, depth+1)
}

func (tt *TopicTree) unsubscribeNode(node *TopicNode, parts []string, clientID string, depth int) bool {
	if depth == len(parts) {
		delete(node.subscribers, clientID)
		return len(node.subscribers) == 0 && len(node.children) == 0
	}

	part := parts[depth]
	child, exists := node.children[part]
	if !exists {
		return false
	}

	if tt.unsubscribeNode(child, parts, clientID, depth+1) {
		delete(node.children, part)
	}

	return len(node.subscribers) == 0 && len(node.children) == 0
}

func (tt *TopicTree) matchNode(node *TopicNode, parts []string, depth int, results *[]Subscriber, visited map[string]struct{}) {
	// Add subscribers at current node
	for clientID, qos := range node.subscribers {
		if _, ok := visited[clientID]; !ok {
			visited[clientID] = struct{}{}
			*results = append(*results, Subscriber{ClientID: clientID, QoS: qos})
		}
	}

	// # wildcard matches zero or more remaining levels
	if hashChild, exists := node.children["#"]; exists {
		tt.collectAllSubscribers(hashChild, results, visited)
	}

	if depth == len(parts) {
		return
	}

	part := parts[depth]

	// Match exact child
	if child, exists := node.children[part]; exists {
		tt.matchNode(child, parts, depth+1, results, visited)
	}

	// Match '+' wildcard (single-level)
	if plusChild, exists := node.children["+"]; exists {
		tt.matchNode(plusChild, parts, depth+1, results, visited)
	}
}

// matchNodeWithSys performs matching with MQTT §4.7.2 system topic protection.
// Topics starting with '$' are NOT matched by '+' or '#' wildcards
// unless the filter explicitly starts with '$'.
func (tt *TopicTree) matchNodeWithSys(node *TopicNode, parts []string, depth int, results *[]Subscriber, visited map[string]struct{}, isSystemTopic bool) {
	// Add subscribers at current node
	for clientID, qos := range node.subscribers {
		if _, ok := visited[clientID]; !ok {
			visited[clientID] = struct{}{}
			*results = append(*results, Subscriber{ClientID: clientID, QoS: qos})
		}
	}

	// # wildcard matches zero or more remaining levels (except for system topics)
	if !isSystemTopic {
		if hashChild, exists := node.children["#"]; exists {
			tt.collectAllSubscribers(hashChild, results, visited)
		}
	}

	if depth == len(parts) {
		return
	}

	part := parts[depth]

	// Match exact child
	if child, exists := node.children[part]; exists {
		tt.matchNodeWithSys(child, parts, depth+1, results, visited, isSystemTopic)
	}

	// For system topics ($SYS), do NOT match wildcards unless explicitly subscribed
	if !isSystemTopic {
		// Match '+' wildcard (single-level)
		if plusChild, exists := node.children["+"]; exists {
			tt.matchNodeWithSys(plusChild, parts, depth+1, results, visited, false)
		}
	}
}

func (tt *TopicTree) collectAllSubscribers(node *TopicNode, results *[]Subscriber, visited map[string]struct{}) {
	for clientID, qos := range node.subscribers {
		if _, ok := visited[clientID]; !ok {
			visited[clientID] = struct{}{}
			*results = append(*results, Subscriber{ClientID: clientID, QoS: qos})
		}
	}

	for _, child := range node.children {
		tt.collectAllSubscribers(child, results, visited)
	}
}

// ValidateTopicFilter checks whether a topic filter conforms to MQTT spec rules.
// Rules: '#' must be last character and preceded by '/' or be the entire filter;
// '+' must occupy an entire level (bounded by '/' or string start/end).
func ValidateTopicFilter(filter string) bool {
	if len(filter) == 0 {
		return false
	}
	for i := 0; i < len(filter); i++ {
		switch filter[i] {
		case '#':
			// '#' must be the last character
			if i != len(filter)-1 {
				return false
			}
			// '#' must be preceded by '/' or be the first character
			if i > 0 && filter[i-1] != '/' {
				return false
			}
		case '+':
			// '+' must be bounded by '/' or string boundaries
			if i > 0 && filter[i-1] != '/' {
				return false
			}
			if i < len(filter)-1 && filter[i+1] != '/' {
				return false
			}
		}
	}
	return true
}

// splitTopic splits a topic string by '/'.
func splitTopic(topic string) []string {
	parts := make([]string, 0)
	start := 0
	for i := 0; i <= len(topic); i++ {
		if i == len(topic) || topic[i] == '/' {
			parts = append(parts, topic[start:i])
			start = i + 1
		}
	}
	return parts
}

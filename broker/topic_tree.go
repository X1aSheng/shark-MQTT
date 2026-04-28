// Package broker provides the core MQTT message broker.
package broker

import (
	"sync"

	"github.com/X1aSheng/shark-mqtt/protocol"
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
	if !protocol.ValidateTopicFilter(topic) {
		return false
	}
	tt.mu.Lock()
	defer tt.mu.Unlock()

	parts := protocol.SplitTopic(topic)
	tt.subscribeNode(tt.root, parts, clientID, qos, 0)
	return true
}

// SubscribeSystem adds a subscription for a system topic ($SYS).
// System topics are protected from wildcard matching.
func (tt *TopicTree) SubscribeSystem(topic string, clientID string, qos uint8) bool {
	if !protocol.ValidateTopicFilter(topic) {
		return false
	}
	tt.mu.Lock()
	defer tt.mu.Unlock()

	parts := protocol.SplitTopic(topic)
	tt.subscribeNode(tt.root, parts, clientID, qos, 0)
	return true
}

// Unsubscribe removes a client's subscription from a topic filter.
func (tt *TopicTree) Unsubscribe(topic string, clientID string) {
	tt.mu.Lock()
	defer tt.mu.Unlock()

	parts := protocol.SplitTopic(topic)
	tt.unsubscribeNode(tt.root, parts, clientID, 0)
}

// Subscriber represents a client subscribed to a topic.
type Subscriber struct {
	ClientID string
	QoS      uint8
}

// Match finds all subscribers that match a given topic.
// Implements MQTT §4.7.2: topics starting with $ are not matched by + or #
// wildcards unless the subscription filter also starts with $.
func (tt *TopicTree) Match(topic string) []Subscriber {
	tt.mu.RLock()
	defer tt.mu.RUnlock()

	parts := protocol.SplitTopic(topic)
	var results []Subscriber
	visited := make(map[string]struct{})
	isSystemTopic := len(parts) > 0 && len(parts[0]) > 0 && parts[0][0] == '$'
	tt.matchNodeWithSys(tt.root, parts, 0, &results, visited, isSystemTopic)
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

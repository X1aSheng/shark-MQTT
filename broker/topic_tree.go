// Package broker provides the core MQTT message broker.
package broker

import (
	"strings"
	"sync"
	"sync/atomic"

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
	root      *TopicNode
	mu        sync.RWMutex
	totalSubs atomic.Int64

	// sharedSubs tracks shared subscriptions keyed as:
	// shareName -> topicFilter -> clientID -> QoS
	sharedSubs     map[string]map[string]map[string]uint8
	sharedCounters map[string]*atomic.Uint64 // shareName -> round-robin counter
	sharedSubsMu   sync.RWMutex
}

// NewTopicTree creates a new TopicTree.
func NewTopicTree() *TopicTree {
	return &TopicTree{
		root: &TopicNode{
			children:    make(map[string]*TopicNode),
			subscribers: make(map[string]uint8),
		},
		sharedSubs:     make(map[string]map[string]map[string]uint8),
		sharedCounters: make(map[string]*atomic.Uint64),
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
// The topic is stored in the same tree as regular subscriptions.
// Protection against wildcard matching of $SYS topics is enforced
// in Match() / matchNodeWithSys() per MQTT §4.7.2.
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
	tt.matchNodeWithSys(tt.root, parts, 0, &results, visited, isSystemTopic, !isSystemTopic)
	return results
}

func (tt *TopicTree) subscribeNode(node *TopicNode, parts []string, clientID string, qos uint8, depth int) {
	if depth == len(parts) {
		if _, exists := node.subscribers[clientID]; !exists {
			tt.totalSubs.Add(1)
		}
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
		if _, exists := node.subscribers[clientID]; exists {
			tt.totalSubs.Add(-1)
		}
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
// Root-level '+' and '#' filters never match topics starting with '$'. Once a
// filter explicitly enters a '$' root segment, its nested wildcards are valid.
func (tt *TopicTree) matchNodeWithSys(node *TopicNode, parts []string, depth int, results *[]Subscriber, visited map[string]struct{}, isSystemTopic bool, wildcardsAllowed bool) {
	// Add subscribers at current node
	for clientID, qos := range node.subscribers {
		if _, ok := visited[clientID]; !ok {
			visited[clientID] = struct{}{}
			*results = append(*results, Subscriber{ClientID: clientID, QoS: qos})
		}
	}

	// # wildcard matches zero or more remaining levels when allowed.
	if wildcardsAllowed {
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
		nextWildcardsAllowed := wildcardsAllowed || (isSystemTopic && depth == 0 && len(part) > 0 && part[0] == '$')
		tt.matchNodeWithSys(child, parts, depth+1, results, visited, isSystemTopic, nextWildcardsAllowed)
	}

	if wildcardsAllowed {
		if plusChild, exists := node.children["+"]; exists {
			tt.matchNodeWithSys(plusChild, parts, depth+1, results, visited, isSystemTopic, true)
		}
	}
}

// SubscriberCount returns the total number of subscription entries.
func (tt *TopicTree) SubscriberCount() int64 {
	return tt.totalSubs.Load()
}

// sharedSubPrefix is the prefix for MQTT 5.0 shared subscriptions.
const sharedSubPrefix = "$share/"

// IsSharedSubscription reports whether a topic filter is a shared subscription.
func IsSharedSubscription(filter string) bool {
	return len(filter) > len(sharedSubPrefix) && filter[:len(sharedSubPrefix)] == sharedSubPrefix
}

// ParseSharedFilter parses a shared subscription filter of the form
// "$share/{ShareName}/{filter}" and returns the share name and actual filter.
func ParseSharedFilter(filter string) (shareName, topicFilter string, ok bool) {
	if !IsSharedSubscription(filter) {
		return "", "", false
	}
	rest := filter[len(sharedSubPrefix):]
	slashIdx := -1
	if i := strings.IndexByte(rest, '/'); i >= 0 {
		slashIdx = i
	} else {
		return "", "", false
	}
	if slashIdx <= 0 || slashIdx >= len(rest)-1 {
		return "", "", false
	}
	return rest[:slashIdx], rest[slashIdx+1:], true
}

// SubscribeShared adds a client to a shared subscription group.
func (tt *TopicTree) SubscribeShared(shareName, topicFilter, clientID string, qos uint8) {
	tt.sharedSubsMu.Lock()
	defer tt.sharedSubsMu.Unlock()

	if tt.sharedSubs[shareName] == nil {
		tt.sharedSubs[shareName] = make(map[string]map[string]uint8)
	}
	if tt.sharedSubs[shareName][topicFilter] == nil {
		tt.sharedSubs[shareName][topicFilter] = make(map[string]uint8)
	}
	if _, exists := tt.sharedSubs[shareName][topicFilter][clientID]; !exists {
		tt.totalSubs.Add(1)
	}
	tt.sharedSubs[shareName][topicFilter][clientID] = qos

	// Ensure round-robin counter exists
	if tt.sharedCounters[shareName] == nil {
		tt.sharedCounters[shareName] = new(atomic.Uint64)
	}
}

// UnsubscribeShared removes a client from a shared subscription group.
func (tt *TopicTree) UnsubscribeShared(shareName, topicFilter, clientID string) {
	tt.sharedSubsMu.Lock()
	defer tt.sharedSubsMu.Unlock()

	if tt.sharedSubs[shareName] == nil {
		return
	}
	if tt.sharedSubs[shareName][topicFilter] == nil {
		return
	}
	if _, exists := tt.sharedSubs[shareName][topicFilter][clientID]; exists {
		tt.totalSubs.Add(-1)
	}
	delete(tt.sharedSubs[shareName][topicFilter], clientID)

	// Clean up empty maps
	if len(tt.sharedSubs[shareName][topicFilter]) == 0 {
		delete(tt.sharedSubs[shareName], topicFilter)
	}
	if len(tt.sharedSubs[shareName]) == 0 {
		delete(tt.sharedSubs, shareName)
		delete(tt.sharedCounters, shareName)
	}
}

// SharedSubscriber represents a shared subscription match result.
type SharedSubscriber struct {
	ClientID  string
	QoS       uint8
	ShareName string
}

// MatchShared returns shared subscribers for a given topic, selecting exactly
// one subscriber per share group in round-robin fashion.
func (tt *TopicTree) MatchShared(topic string) []SharedSubscriber {
	tt.sharedSubsMu.RLock()
	defer tt.sharedSubsMu.RUnlock()

	if len(tt.sharedSubs) == 0 {
		return nil
	}

	var results []SharedSubscriber
	for shareName, filters := range tt.sharedSubs {
		matchingFilters := make([]string, 0)
		for filter := range filters {
			if matchSysProtected(filter, topic) {
				matchingFilters = append(matchingFilters, filter)
			}
		}
		if len(matchingFilters) == 0 {
			continue
		}

		// Collect all clients from matching filters, dedup by clientID
		type member struct {
			clientID string
			qos      uint8
		}
		seen := make(map[string]struct{})
		var members []member
		for _, filter := range matchingFilters {
			for cid, qos := range tt.sharedSubs[shareName][filter] {
				if _, dup := seen[cid]; !dup {
					seen[cid] = struct{}{}
					members = append(members, member{clientID: cid, qos: qos})
				}
			}
		}

		if len(members) == 0 {
			continue
		}

		// Round-robin select one member
		counter := tt.sharedCounters[shareName]
		idx := int(counter.Add(1)-1) % len(members)
		results = append(results, SharedSubscriber{
			ClientID:  members[idx].clientID,
			QoS:       members[idx].qos,
			ShareName: shareName,
		})
	}
	return results
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

// matchSysProtected wraps protocol.MatchTopic with MQTT §4.7.2
// system topic protection for non-TopicTree callers (MatchShared, ACL).
func matchSysProtected(pattern, topic string) bool {
	if len(topic) > 0 && topic[0] == '$' {
		firstLevel := pattern
		if i := strings.IndexByte(pattern, '/'); i >= 0 {
			firstLevel = pattern[:i]
		}
		if firstLevel == "#" || firstLevel == "+" {
			return false
		}
	}
	return protocol.MatchTopic(pattern, topic)
}

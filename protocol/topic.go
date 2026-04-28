package protocol

import "strings"

// ValidatePublishTopic returns true if the topic is valid for PUBLISH.
// Per MQTT 3.1.1 §3.3.2 and MQTT 5.0 §3.3.2, PUBLISH topics must not contain wildcards.
func ValidatePublishTopic(topic string) bool {
	if len(topic) == 0 {
		return false
	}
	for i := 0; i < len(topic); i++ {
		if topic[i] == '#' || topic[i] == '+' {
			return false
		}
	}
	return true
}

// MatchTopic checks if a topic matches an MQTT topic pattern (with wildcards + and #).
// Returns true if the topic matches the pattern.
// Example patterns:
//   - "sport/tennis/+" matches "sport/tennis/player1"
//   - "sport/tennis/+" does NOT match "sport/tennis/player1/ranking"
//   - "sport/tennis/#" matches "sport/tennis/player1/ranking"
//   - "sport/+" does NOT match "sport/tennis/player1"
func MatchTopic(pattern, topic string) bool {
	patternLevels := strings.Split(pattern, "/")
	topicLevels := strings.Split(topic, "/")

	return matchLevels(patternLevels, topicLevels)
}

// SplitTopic splits an MQTT topic string into its level components.
func SplitTopic(topic string) []string {
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
			if i != len(filter)-1 {
				return false
			}
			if i > 0 && filter[i-1] != '/' {
				return false
			}
		case '+':
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

func matchLevels(patternLevels, topicLevels []string) bool {
	for i := 0; i < len(patternLevels); i++ {
		pattern := patternLevels[i]

		// # matches all remaining levels
		if pattern == "#" {
			return true
		}

		// If we've run out of topic levels, no match
		if i >= len(topicLevels) {
			return false
		}

		// + matches exactly one level
		if pattern == "+" {
			continue
		}

		// Exact match required
		if pattern != topicLevels[i] {
			return false
		}
	}

	// Pattern and topic must have the same number of levels (unless # was encountered)
	return len(patternLevels) == len(topicLevels)
}

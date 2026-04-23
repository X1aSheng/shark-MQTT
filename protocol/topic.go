package protocol

import "strings"

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

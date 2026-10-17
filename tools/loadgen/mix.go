package main

import (
	"fmt"
	"strconv"
	"strings"
)

// parseMix turns "qos0:70,qos1:30" into a repeating pattern of QoS levels.
//
// A pattern, not a random draw: when the weights divide evenly the pattern
// reproduces the requested ratio exactly over any window, which is what makes
// two runs of different lengths comparable. Random selection would add sampling
// error on top of the measurement error we are already fighting.
func parseMix(spec string) ([]uint8, error) {
	if spec == "" {
		return nil, nil
	}

	var pattern []uint8
	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("bad mix entry %q, want qosN:weight", part)
		}
		name := strings.TrimSpace(kv[0])
		if !strings.HasPrefix(name, "qos") {
			return nil, fmt.Errorf("bad mix entry %q, want qosN:weight", part)
		}
		q, err := strconv.Atoi(strings.TrimPrefix(name, "qos"))
		if err != nil || q < 0 || q > 2 {
			return nil, fmt.Errorf("bad QoS in %q, want 0, 1 or 2", part)
		}
		w, err := strconv.Atoi(strings.TrimSpace(kv[1]))
		if err != nil || w <= 0 {
			return nil, fmt.Errorf("bad weight in %q, want a positive integer", part)
		}
		for i := 0; i < w; i++ {
			pattern = append(pattern, uint8(q))
		}
	}

	if len(pattern) == 0 {
		return nil, fmt.Errorf("mix %q produced an empty pattern", spec)
	}
	// "qos0:10000" is valid input but a ten-thousand element slice per publisher
	// buys nothing: the ratio is the same at 100.
	const maxPattern = 1000
	if len(pattern) > maxPattern {
		return nil, fmt.Errorf("mix %q expands to %d entries, keep it under %d", spec, len(pattern), maxPattern)
	}
	return pattern, nil
}

// maxQoS returns the highest level in the pattern, which is what subscribers
// must ask for: an MQTT subscription carries a maximum QoS, and asking for less
// than the publishers send would down-grade the delivery and hide the very cost
// the mixed run is meant to measure.
func maxQoS(pattern []uint8) uint8 {
	var m uint8
	for _, q := range pattern {
		if q > m {
			m = q
		}
	}
	return m
}

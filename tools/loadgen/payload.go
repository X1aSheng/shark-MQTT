package main

import (
	"fmt"
	"strconv"
	"strings"
)

// payloadPlan turns "-payload-mix 64:80,4096:20" into a repeating schedule of
// preallocated buffers.
//
// Buffers are allocated up front and reused. The publish loop must not allocate
// per message, or every measurement would include the allocator's behaviour on
// top of the broker's -- and a mixed workload, where frame sizes change every
// message, is exactly where an accidental per-message allocation would hide.
type payloadPlan struct {
	sizes   []int
	pattern []int
	bufs    [][]byte
}

func parsePayloadMix(spec string) (*payloadPlan, error) {
	if spec == "" {
		return nil, nil
	}

	sizes := make([]int, 0, 4)
	pattern := make([]int, 0, 16)
	index := make(map[int]int, 4) // size -> index in sizes

	for _, part := range strings.Split(spec, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		kv := strings.SplitN(part, ":", 2)
		if len(kv) != 2 {
			return nil, fmt.Errorf("bad payload entry %q, want bytes:weight", part)
		}
		size, err := strconv.Atoi(strings.TrimSpace(kv[0]))
		if err != nil || size < 8 {
			return nil, fmt.Errorf("bad payload size in %q, want an integer >= 8 "+
				"(the first 8 bytes carry the publish timestamp)", part)
		}
		w, err := strconv.Atoi(strings.TrimSpace(kv[1]))
		if err != nil || w <= 0 {
			return nil, fmt.Errorf("bad weight in %q, want a positive integer", part)
		}

		idx, ok := index[size]
		if !ok {
			idx = len(sizes)
			index[size] = idx
			sizes = append(sizes, size)
		}
		for i := 0; i < w; i++ {
			pattern = append(pattern, idx)
		}
	}

	if len(pattern) == 0 {
		return nil, fmt.Errorf("payload mix %q produced an empty schedule", spec)
	}
	const maxPattern = 1000
	if len(pattern) > maxPattern {
		return nil, fmt.Errorf("payload mix %q expands to %d entries, keep it under %d",
			spec, len(pattern), maxPattern)
	}

	bufs := make([][]byte, len(sizes))
	for i, sz := range sizes {
		bufs[i] = makePayload(sz)
	}
	return &payloadPlan{sizes: sizes, pattern: pattern, bufs: bufs}, nil
}

// frame returns the buffer for the given global send index. Same pattern
// reasoning as the QoS mix: a deterministic rotation reproduces the requested
// ratio exactly over any window.
func (p *payloadPlan) frame(sent int64) []byte {
	return p.bufs[p.pattern[int(sent)%len(p.pattern)]]
}

// describe renders the plan for the human-readable report line.
func (p *payloadPlan) describe() string {
	parts := make([]string, 0, len(p.sizes))
	for _, sz := range p.sizes {
		parts = append(parts, strconv.Itoa(sz))
	}
	return strings.Join(parts, "/") + "B mixed"
}

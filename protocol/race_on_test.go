//go:build race

package protocol

// raceEnabled reports whether the race detector is active. Allocation-count
// assertions are meaningless under -race (the detector instruments the
// allocator), so tests skip them in that mode.
const raceEnabled = true

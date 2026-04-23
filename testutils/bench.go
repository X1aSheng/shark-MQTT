package testutils

import (
	"fmt"
	"math"
	"sort"
	"sync"
	"time"
)

// BenchResult holds the results of a benchmark run.
type BenchResult struct {
	Duration   time.Duration
	TotalOps   int
	OpsPerSec  float64
	Latencies  []time.Duration
	P50        time.Duration
	P95        time.Duration
	P99        time.Duration
	AvgLatency time.Duration
}

// LatencyRecorder collects latency measurements for benchmarks.
type LatencyRecorder struct {
	mu        sync.Mutex
	latencies []time.Duration
}

// NewLatencyRecorder creates a new latency recorder.
func NewLatencyRecorder() *LatencyRecorder {
	return &LatencyRecorder{
		latencies: make([]time.Duration, 0),
	}
}

// Record records a single latency measurement.
func (r *LatencyRecorder) Record(d time.Duration) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.latencies = append(r.latencies, d)
}

// Result computes and returns the benchmark result.
func (r *LatencyRecorder) Result(duration time.Duration) *BenchResult {
	r.mu.Lock()
	defer r.mu.Unlock()

	n := len(r.latencies)
	if n == 0 {
		return &BenchResult{Duration: duration}
	}

	sort.Slice(r.latencies, func(i, j int) bool {
		return r.latencies[i] < r.latencies[j]
	})

	var total time.Duration
	for _, l := range r.latencies {
		total += l
	}

	return &BenchResult{
		Duration:   duration,
		TotalOps:   n,
		OpsPerSec:  float64(n) / duration.Seconds(),
		Latencies:  r.latencies,
		P50:        r.percentile(0.50),
		P95:        r.percentile(0.95),
		P99:        r.percentile(0.99),
		AvgLatency: total / time.Duration(n),
	}
}

func (r *LatencyRecorder) percentile(p float64) time.Duration {
	if len(r.latencies) == 0 {
		return 0
	}
	idx := int(math.Ceil(float64(len(r.latencies))*p)) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(r.latencies) {
		idx = len(r.latencies) - 1
	}
	return r.latencies[idx]
}

// String returns a human-readable summary of the benchmark result.
func (r *BenchResult) String() string {
	if r.TotalOps == 0 {
		return "no operations recorded"
	}
	return fmt.Sprintf(
		"%d ops in %s (%.0f ops/sec) - avg: %s, p50: %s, p95: %s, p99: %s",
		r.TotalOps,
		r.Duration.Round(time.Millisecond),
		r.OpsPerSec,
		r.AvgLatency.Round(time.Microsecond),
		r.P50.Round(time.Microsecond),
		r.P95.Round(time.Microsecond),
		r.P99.Round(time.Microsecond),
	)
}

// ThroughputCalculator measures operations per second.
type ThroughputCalculator struct {
	mu    sync.Mutex
	count int64
	start time.Time
}

// NewThroughputCalculator creates a new throughput calculator.
func NewThroughputCalculator() *ThroughputCalculator {
	return &ThroughputCalculator{
		start: time.Now(),
	}
}

// Inc increments the operation counter.
func (t *ThroughputCalculator) Inc() {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count++
}

// Add adds n to the operation counter.
func (t *ThroughputCalculator) Add(n int64) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.count += n
}

// Rate returns the current operations per second.
func (t *ThroughputCalculator) Rate() float64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	elapsed := time.Since(t.start).Seconds()
	if elapsed == 0 {
		return 0
	}
	return float64(t.count) / elapsed
}

// Total returns the total operation count.
func (t *ThroughputCalculator) Total() int64 {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.count
}

//go:build nometrics

package metrics

// DefaultMetrics returns the metrics implementation for the `nometrics` minimal
// build (R4): a no-op, so the binary does not link prometheus/client_golang and
// the /metrics endpoint is not registered. Use the default build for metrics.
func DefaultMetrics() Metrics {
	return Default()
}

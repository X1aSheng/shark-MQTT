package defects

import (
	"os"
	"strings"
	"testing"
)

func TestDefectWindowsConnectionChurnBenchmarksDoNotPoisonSuite(t *testing.T) {
	data, err := os.ReadFile("../bench/broker_bench_test.go")
	if err != nil {
		t.Fatalf("read benchmark source: %v", err)
	}

	source := string(data)
	if !strings.Contains(source, `runtime.GOOS == "windows"`) {
		t.Fatal("connection-churn benchmarks lack Windows guard; full benchmark suite can exhaust ephemeral TCP ports")
	}
}

func TestDefectWindowsWillMessageBenchmarkDoesNotPoisonSuite(t *testing.T) {
	data, err := os.ReadFile("../bench/data_delivery_bench_test.go")
	if err != nil {
		t.Fatalf("read E2E benchmark source: %v", err)
	}

	source := string(data)
	if !strings.Contains(source, "BenchmarkE2E_WillMessage") {
		t.Fatal("BenchmarkE2E_WillMessage not found")
	}
	if !strings.Contains(source, `runtime.GOOS == "windows"`) {
		t.Fatal("will-message benchmark lacks Windows guard; abnormal disconnect churn can exhaust ephemeral TCP ports")
	}
}

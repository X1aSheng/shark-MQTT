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

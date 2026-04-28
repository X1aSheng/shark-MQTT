//go:build ignore

// shark-mqtt cross-platform test runner.
//
// Works on every OS with Go installed. Run from the project root:
//
//	go run scripts/run_tests.go                        # run all
//	go run scripts/run_tests.go -mode unit             # unit tests only
//	go run scripts/run_tests.go -mode integration      # integration tests only
//	go run scripts/run_tests.go -mode benchmark        # benchmarks only
//	go run scripts/run_tests.go -mode cover            # coverage report
//
// Logs saved to ./logs/ as <timestamp>_<type>.{json,log}
// Example: logs/20260428_190627_unit.json

package main

import (
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

const jsonModulePrefix = "github.com/X1aSheng/shark-mqtt/"

var reJSONTime = regexp.MustCompile(`("Time":"\d{4}-\d{2}-\d{2}T\d{2}:\d{2}:\d{2}\.\d{3})\d*[^"]*"`)

func main() {
	var (
		mode    = flag.String("mode", "all", "test mode: unit, integration, benchmark, cover, all")
		logDir  = flag.String("logdir", "logs", "directory for log output")
		timeout = flag.Duration("timeout", 5*time.Minute, "overall test timeout")
	)
	flag.Parse()

	projectDir := findProjectDir()
	logsDir := filepath.Join(projectDir, *logDir)
	os.MkdirAll(logsDir, 0o755)

	ts := time.Now().Format("20060102_150405")

	switch *mode {
	case "unit":
		runTest(projectDir, logsDir, ts, "unit", "Unit Tests",
			"./broker/...", "./protocol/...", "./store/...", "./client/...",
			"./config/...", "./errs/...", "./pkg/...", "./plugin/...", "./api/...")
	case "integration":
		runTest(projectDir, logsDir, ts, "integration", "Integration Tests",
			"./tests/integration/...")
	case "benchmark":
		runBenchmark(projectDir, logsDir, ts)
	case "cover":
		runCover(projectDir, logsDir, ts, timeout)
	case "all":
		fmt.Println()
		printBanner("shark-mqtt full test suite", time.Now().Format("2006-01-02 15:04:05"))

		runTest(projectDir, logsDir, ts, "unit", "Unit Tests",
			"./broker/...", "./protocol/...", "./store/...", "./client/...",
			"./config/...", "./errs/...", "./pkg/...", "./plugin/...", "./api/...")
		runTest(projectDir, logsDir, ts, "integration", "Integration Tests",
			"./tests/integration/...")
		runBenchmark(projectDir, logsDir, ts)

		fmt.Println()
		printBanner("All tests complete", "")
		fmt.Printf("  Logs in: %s\n", logsDir)
		listLogs(logsDir, ts)
	default:
		fmt.Fprintf(os.Stderr, "Unknown mode: %s\n", *mode)
		fmt.Fprintln(os.Stderr, "Usage: go run scripts/run_tests.go -mode [unit|integration|benchmark|cover|all]")
		os.Exit(1)
	}
}

// -------------------------------------------------------------------
// runTest runs go test with -json, saves .json + parsed .log
// -------------------------------------------------------------------
func runTest(projectDir, logsDir, ts, name, label string, packages ...string) {
	jsonFile := filepath.Join(logsDir, ts+"_"+name+".json")
	logFile := filepath.Join(logsDir, ts+"_"+name+".log")

	fmt.Println()
	printCyan(fmt.Sprintf(">>> [%s] Running...", label))
	printCyan(fmt.Sprintf("    JSON  => %s", jsonFile))
	printCyan(fmt.Sprintf("    Report=> %s", logFile))
	fmt.Println()

	args := []string{"test", "-json", "-v", "-count=1", "-timeout=300s"}
	args = append(args, packages...)
	goCapture(projectDir, args, jsonFile)

	// Parse JSON to readable report
	out, _ := goOutput(projectDir, []string{"run", "scripts/parse_test_log.go", jsonFile})
	os.WriteFile(logFile, out, 0o644)
	if len(out) > 0 {
		fmt.Print(string(out))
	}

	fmt.Println()
	printGreen(fmt.Sprintf(">>> [%s] Done. Log saved.", label))
}

// -------------------------------------------------------------------
// runBenchmark runs go test -bench, saves .json + parsed .log
// -------------------------------------------------------------------
func runBenchmark(projectDir, logsDir, ts string) {
	packages := []string{
		"./tests/bench/...",
		"./broker/...",
		"./protocol/...",
		"./store/...",
		"./pkg/...",
	}

	jsonFile := filepath.Join(logsDir, ts+"_benchmark.json")
	logFile := filepath.Join(logsDir, ts+"_benchmark.log")

	fmt.Println()
	printCyan(">>> [Benchmarks] Running...")
	printCyan(fmt.Sprintf("    JSON  => %s", jsonFile))
	printCyan(fmt.Sprintf("    Report=> %s", logFile))
	fmt.Println()

	args := []string{"test", "-bench=.", "-benchmem", "-run=^$", "-count=1", "-timeout=300s", "-json"}
	args = append(args, packages...)
	goCapture(projectDir, args, jsonFile)

	out, _ := goOutput(projectDir, []string{"run", "scripts/parse_test_log.go", jsonFile})
	os.WriteFile(logFile, out, 0o644)
	if len(out) > 0 {
		fmt.Print(string(out))
	}

	fmt.Println()
	printGreen(">>> [Benchmarks] Done. Log saved.")
}

// -------------------------------------------------------------------
// runCover runs coverage and saves .log
// -------------------------------------------------------------------
func runCover(projectDir, logsDir, ts string, timeout *time.Duration) {
	logFile := filepath.Join(logsDir, ts+"_cover.log")

	fmt.Println()
	printBanner("Coverage Report", time.Now().Format("2006-01-02 15:04:05"))

	args := []string{"test", "./...", "-count=1", "-cover", fmt.Sprintf("-timeout=%s", *timeout)}
	out, _ := goOutput(projectDir, args)
	os.WriteFile(logFile, out, 0o644)
	fmt.Print(string(out))

	fmt.Println()
	printGreen(fmt.Sprintf(">>> Coverage log: %s", logFile))
}

// -------------------------------------------------------------------
// Helpers
// -------------------------------------------------------------------

func findProjectDir() string {
	// Try relative to executable (when run via `go run`)
	exe, _ := os.Executable()
	candidate := filepath.Clean(filepath.Join(filepath.Dir(exe), "..", ".."))
	if _, err := os.Stat(filepath.Join(candidate, "go.mod")); err == nil {
		return candidate
	}
	// Fallback: current working directory
	wd, _ := os.Getwd()
	if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
		return wd
	}
	fmt.Fprintln(os.Stderr, "Cannot find project root (go.mod not found)")
	os.Exit(1)
	return ""
}

func goCapture(dir string, args []string, outFile string) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, _ := cmd.CombinedOutput()
	os.WriteFile(outFile, sanitizeJSON(out), 0o644)
}

func goOutput(dir string, args []string) ([]byte, error) {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	return cmd.CombinedOutput()
}

func sanitizeJSON(raw []byte) []byte {
	s := strings.ReplaceAll(string(raw), jsonModulePrefix, "")
	s = reJSONTime.ReplaceAllString(s, `${1}"`)
	return []byte(s)
}

func listLogs(dir, ts string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), ts) {
			info, _ := e.Info()
			fmt.Printf("  %6s  %s\n", humanSize(info.Size()), e.Name())
		}
	}
}

func humanSize(n int64) string {
	const (
		KB = 1024
		MB = 1024 * KB
	)
	switch {
	case n >= MB:
		return fmt.Sprintf("%.1fM", float64(n)/float64(MB))
	case n >= KB:
		return fmt.Sprintf("%.1fK", float64(n)/float64(KB))
	default:
		return fmt.Sprintf("%dB", n)
	}
}

func printBanner(title, subtitle string) {
	fmt.Println("========================================")
	fmt.Printf("  %s\n", title)
	if subtitle != "" {
		fmt.Printf("  %s\n", subtitle)
	}
	fmt.Println("========================================")
}

func printCyan(msg string)  { fmt.Printf("\x1b[36m%s\x1b[0m\n", msg) }
func printGreen(msg string) { fmt.Printf("\x1b[32m%s\x1b[0m\n", msg) }

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
//	go run scripts/run_tests.go -save-json             # also save raw JSON files
//
// By default, tests run with -v and save readable text logs to ./logs/.
// With -save-json, raw go test -json output is also saved (larger files).

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

// saveJSON controls whether raw go test -json output files are written.
// Set via -save-json flag. Default false — only readable .log files are saved.
var saveJSON bool

func main() {
	var (
		mode         = flag.String("mode", "all", "test mode: unit, integration, benchmark, cover, all")
		logDir       = flag.String("logdir", "logs", "directory for log output")
		timeout      = flag.Duration("timeout", 5*time.Minute, "overall test timeout")
		saveJSONFlag = flag.Bool("save-json", false, "also save raw JSON output from go test -json")
	)
	flag.Parse()

	saveJSON = *saveJSONFlag

	projectDir := findProjectDir()
	logsDir := filepath.Join(projectDir, *logDir)
	os.MkdirAll(logsDir, 0o755)

	ts := time.Now().Format("20060102_150405")

	switch *mode {
	case "unit":
		if !runTest(projectDir, logsDir, ts, "unit", "Unit Tests",
			"./broker/...", "./protocol/...", "./store/...", "./client/...",
			"./config/...", "./errs/...", "./pkg/...", "./plugin/...", "./api/...",
			"./tests/defects/...") {
			os.Exit(1)
		}
	case "integration":
		if !runTest(projectDir, logsDir, ts, "integration", "Integration Tests",
			"./tests/integration/...") {
			os.Exit(1)
		}
	case "benchmark":
		if !runBenchmark(projectDir, logsDir, ts) {
			os.Exit(1)
		}
	case "cover":
		if !runCover(projectDir, logsDir, ts, timeout) {
			os.Exit(1)
		}
	case "all":
		fmt.Println()
		printBanner("shark-mqtt full test suite", time.Now().Format("2006-01-02 15:04:05"))

		ok := true
		ok = runTest(projectDir, logsDir, ts, "unit", "Unit Tests",
			"./broker/...", "./protocol/...", "./store/...", "./client/...",
			"./config/...", "./errs/...", "./pkg/...", "./plugin/...", "./api/...",
			"./tests/defects/...") && ok
		ok = runTest(projectDir, logsDir, ts, "integration", "Integration Tests",
			"./tests/integration/...") && ok
		ok = runBenchmark(projectDir, logsDir, ts) && ok

		fmt.Println()
		printBanner("All tests complete", "")
		fmt.Printf("  Logs in: %s\n", logsDir)
		listLogs(logsDir, ts)
		if !ok {
			os.Exit(1)
		}
	default:
		fmt.Fprintf(os.Stderr, "Unknown mode: %s\n", *mode)
		fmt.Fprintln(os.Stderr, "Usage: go run scripts/run_tests.go -mode [unit|integration|benchmark|cover|all]")
		os.Exit(1)
	}
}

// -------------------------------------------------------------------
// runTest runs go test. By default saves text .log; with -save-json
// also saves raw .json from go test -json.
// -------------------------------------------------------------------
func runTest(projectDir, logsDir, ts, name, label string, packages ...string) bool {
	logFile := filepath.Join(logsDir, ts+"_"+name+".log")

	fmt.Println()
	printCyan(fmt.Sprintf(">>> [%s] Running...", label))
	if saveJSON {
		printCyan(fmt.Sprintf("    JSON  => %s", filepath.Join(logsDir, ts+"_"+name+".json")))
	}
	printCyan(fmt.Sprintf("    Report=> %s", logFile))
	fmt.Println()

	var testErr error
	if saveJSON {
		jsonFile := filepath.Join(logsDir, ts+"_"+name+".json")
		args := []string{"test", "-json", "-v", "-count=1", "-timeout=300s"}
		args = append(args, packages...)
		testErr = goCapture(projectDir, args, jsonFile)

		// Parse JSON to readable report
		out, _ := goOutput(projectDir, []string{"run", "scripts/parse_test_log.go", jsonFile})
		os.WriteFile(logFile, out, 0o644)
		if len(out) > 0 {
			fmt.Print(string(out))
		}
	} else {
		args := []string{"test", "-v", "-count=1", "-timeout=300s"}
		args = append(args, packages...)
		out, err := goOutput(projectDir, args)
		if len(out) > 0 {
			fmt.Print(string(out))
		}
		os.WriteFile(logFile, out, 0o644)
		testErr = err
	}

	fmt.Println()
	if testErr != nil {
		printRed(fmt.Sprintf(">>> [%s] Failed: %v", label, testErr))
		return false
	}
	printGreen(fmt.Sprintf(">>> [%s] Passed. Log saved.", label))
	return true
}

// -------------------------------------------------------------------
// runBenchmark runs go test -bench, saves text .log (or raw .json with -save-json).
// -------------------------------------------------------------------
func runBenchmark(projectDir, logsDir, ts string) bool {
	packages := []string{
		"./tests/bench/...",
		"./broker/...",
		"./protocol/...",
		"./store/...",
		"./pkg/...",
	}

	logFile := filepath.Join(logsDir, ts+"_benchmark.log")

	fmt.Println()
	printCyan(">>> [Benchmarks] Running...")
	if saveJSON {
		printCyan(fmt.Sprintf("    JSON  => %s", filepath.Join(logsDir, ts+"_benchmark.json")))
	}
	printCyan(fmt.Sprintf("    Report=> %s", logFile))
	fmt.Println()

	var testErr error
	if saveJSON {
		jsonFile := filepath.Join(logsDir, ts+"_benchmark.json")
		args := []string{"test", "-bench=.", "-benchmem", "-benchtime=500ms", "-run=^$", "-count=1", "-timeout=300s", "-json"}
		args = append(args, packages...)
		testErr = goCapture(projectDir, args, jsonFile)

		out, _ := goOutput(projectDir, []string{"run", "scripts/parse_test_log.go", jsonFile})
		os.WriteFile(logFile, out, 0o644)
		if len(out) > 0 {
			fmt.Print(string(out))
		}
	} else {
		args := []string{"test", "-bench=.", "-benchmem", "-benchtime=500ms", "-run=^$", "-count=1", "-timeout=300s"}
		args = append(args, packages...)
		out, err := goOutput(projectDir, args)
		if len(out) > 0 {
			fmt.Print(string(out))
		}
		os.WriteFile(logFile, out, 0o644)
		testErr = err
	}

	fmt.Println()
	if testErr != nil {
		printRed(fmt.Sprintf(">>> [Benchmarks] Failed: %v", testErr))
		return false
	}
	printGreen(">>> [Benchmarks] Passed. Log saved.")
	return true
}

// -------------------------------------------------------------------
// runCover runs coverage and saves .log
// -------------------------------------------------------------------
func runCover(projectDir, logsDir, ts string, timeout *time.Duration) bool {
	logFile := filepath.Join(logsDir, ts+"_cover.log")

	fmt.Println()
	printBanner("Coverage Report", time.Now().Format("2006-01-02 15:04:05"))

	args := []string{"test", "./...", "-count=1", "-cover", fmt.Sprintf("-timeout=%s", *timeout)}
	out, err := goOutput(projectDir, args)
	os.WriteFile(logFile, out, 0o644)
	fmt.Print(string(out))

	fmt.Println()
	if err != nil {
		printRed(fmt.Sprintf(">>> Coverage failed: %v", err))
		return false
	}
	printGreen(fmt.Sprintf(">>> Coverage log: %s", logFile))
	return true
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

func goCapture(dir string, args []string, outFile string) error {
	cmd := exec.Command("go", args...)
	cmd.Dir = dir
	out, err := cmd.CombinedOutput()
	os.WriteFile(outFile, sanitizeJSON(out), 0o644)
	return err
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
func printRed(msg string)   { fmt.Printf("\x1b[31m%s\x1b[0m\n", msg) }

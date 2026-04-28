//go:build ignore

package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"time"
)

type TestEvent struct {
	Time    string  `json:"Time"`
	Action  string  `json:"Action"`
	Package string  `json:"Package"`
	Test    string  `json:"Test"`
	Elapsed float64 `json:"Elapsed"`
	Output  string  `json:"Output"`
}

type testResult struct {
	action  string
	elapsed float64
	outputs []string
}

type benchResult struct {
	name     string
	iter     int64
	nsPerOp  float64
	memPerOp string
	allocs   string
	extra    string
}

const modulePrefix = "github.com/X1aSheng/shark-mqtt/"

var (
	reBenchHeader = regexp.MustCompile(`^(Benchmark\S+?)-\d+\s*$`)
	reBenchData   = regexp.MustCompile(`^\s*(\d+)\s+([\d.]+)\s+ns/op`)
	reBenchMem    = regexp.MustCompile(`\s+([\d.]+)\s+(B/op|KB/op|MB/op)`)
	reBenchAllocs = regexp.MustCompile(`\s+(\d+)\s+allocs/op`)
)

func main() {
	if len(os.Args) < 2 {
		fmt.Fprintf(os.Stderr, "usage: parse_test_log <file.json> [file2.json ...]\n")
		os.Exit(1)
	}

	for _, path := range os.Args[1:] {
		if err := parseFile(path); err != nil {
			fmt.Fprintf(os.Stderr, "error parsing %s: %v\n", path, err)
			os.Exit(1)
		}
	}
}

func parseFile(path string) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	tests := make(map[string]*testResult)
	var pkgOrder []string
	pkgSet := make(map[string]bool)
	coverages := make(map[string]string)
	startTime := ""

	pkgOutputs := make(map[string][]string)
	var benchPkgOrder []string
	benchPkgSet := make(map[string]bool)
	pkgElapsed := make(map[string]float64)

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		var ev TestEvent
		if err := json.Unmarshal(scanner.Bytes(), &ev); err != nil {
			continue
		}

		// Strip module prefix for clean output
		pkg := strings.TrimPrefix(ev.Package, modulePrefix)

		if startTime == "" && ev.Time != "" {
			startTime = ev.Time
		}

		key := pkg + "/" + ev.Test

		switch ev.Action {
		case "run":
			if ev.Test != "" {
				tests[key] = &testResult{action: "run", outputs: []string{}}
			}
			if !pkgSet[pkg] && pkg != "" {
				pkgSet[pkg] = true
				pkgOrder = append(pkgOrder, pkg)
			}
		case "pass", "fail", "skip":
			if ev.Test != "" {
				if t, ok := tests[key]; ok {
					t.action = ev.Action
					t.elapsed = ev.Elapsed
				} else {
					tests[key] = &testResult{action: ev.Action, elapsed: ev.Elapsed}
				}
			}
			if ev.Test == "" && pkg != "" {
				pkgElapsed[pkg] = ev.Elapsed
			}
		case "output":
			if ev.Test != "" {
				if t, ok := tests[key]; ok {
					t.outputs = append(t.outputs, ev.Output)
				}
			}
			if pkg != "" {
				pkgOutputs[pkg] = append(pkgOutputs[pkg], ev.Output)
				if !benchPkgSet[pkg] {
					benchPkgSet[pkg] = true
					benchPkgOrder = append(benchPkgOrder, pkg)
				}
			}
			if strings.Contains(ev.Output, "coverage:") {
				coverage := strings.TrimSpace(strings.TrimPrefix(ev.Output, "coverage:"))
				if pkg != "" && coverage != "" {
					coverages[pkg] = coverage
				}
			}
		}
	}

	base := filepath.Base(path)
	testType := "Tests"
	isBenchmark := false
	if strings.Contains(base, "unit") {
		testType = "Unit Tests"
	} else if strings.Contains(base, "integration") {
		testType = "Integration Tests"
	} else if strings.Contains(base, "benchmark") {
		testType = "Benchmark Tests"
		isBenchmark = true
	}

	ts := "unknown"
	if startTime != "" {
		for _, layout := range []string{
			"2006-01-02T15:04:05.000",
			"2006-01-02T15:04:05",
			time.RFC3339Nano,
		} {
			if t, err := time.Parse(layout, startTime); err == nil {
				ts = t.Format("2006-01-02 15:04:05")
				break
			}
		}
	}

	sep := strings.Repeat("=", 70)
	sep2 := strings.Repeat("-", 70)

	fmt.Println(sep)
	fmt.Printf("  shark-mqtt test report\n")
	fmt.Printf("  %s — %s\n", testType, ts)
	fmt.Println(sep)
	fmt.Println()

	if len(pkgOrder) > 0 {
		passCount, failCount, skipCount := 0, 0, 0
		totalElapsed := 0.0

		for _, pkg := range pkgOrder {
			pkgShort := shortPkg(pkg)
			fmt.Printf("  [%s]\n", pkgShort)

			for key, t := range tests {
				if !strings.HasPrefix(key, pkg+"/") || key == pkg+"/" {
					continue
				}
				status := strings.ToUpper(t.action)
				testName := evTest(key)
				padded := fmt.Sprintf("%-5s %-55s", status, testName)
				elapsedStr := ""
				if t.elapsed > 0 {
					elapsedStr = fmt.Sprintf("%.2fs", t.elapsed)
					totalElapsed += t.elapsed
				}
				fmt.Printf("    %s %s\n", padded, elapsedStr)

				switch t.action {
				case "pass":
					passCount++
				case "fail":
					failCount++
					for _, out := range t.outputs {
						if strings.TrimSpace(out) != "" {
							fmt.Printf("        >> %s", out)
						}
					}
				case "skip":
					skipCount++
				}
			}
			fmt.Println()
		}

		fmt.Println(sep2)
		fmt.Printf("  Summary: %d passed, %d failed, %d skipped\n", passCount, failCount, skipCount)
		fmt.Printf("  Duration: %.2fs\n", totalElapsed)

		if len(coverages) > 0 {
			fmt.Println()
			fmt.Println("  Coverage:")
			for _, pkg := range pkgOrder {
				if cov, ok := coverages[pkg]; ok {
					fmt.Printf("    %-40s %s\n", shortPkg(pkg), cov)
				}
			}
		}

		fmt.Println(sep)
		fmt.Println()
	}

	if isBenchmark {
		totalBench := 0
		totalDuration := 0.0

		fmt.Println(sep)
		fmt.Printf("  shark-mqtt benchmark report\n")
		fmt.Printf("  %s\n", ts)
		fmt.Println(sep)
		fmt.Println()

		for _, pkg := range benchPkgOrder {
			outputs := pkgOutputs[pkg]
			benches := parseBenchOutputs(outputs)
			if len(benches) == 0 {
				continue
			}

			pkgShort := shortPkg(pkg)
			fmt.Printf("  [%s]\n", pkgShort)

			for _, b := range benches {
				totalBench++
				fmt.Printf("    %-50s %10s ns/op\n", b.name, formatFloat(b.nsPerOp))
				if b.memPerOp != "" || b.allocs != "" {
					fmt.Printf("      %-48s %10s  %s allocs\n", "", b.memPerOp, b.allocs)
				}
				if b.extra != "" {
					fmt.Printf("      %-48s %s\n", "", b.extra)
				}
			}
			if elapsed, ok := pkgElapsed[pkg]; ok {
				totalDuration += elapsed
				fmt.Printf("\n    Package duration: %.2fs\n", elapsed)
			}
			fmt.Println()
		}

		fmt.Println(sep2)
		fmt.Printf("  Benchmarks: %d\n", totalBench)
		fmt.Printf("  Total duration: %.2fs\n", totalDuration)
		fmt.Println(sep)
		fmt.Println()
	}

	return nil
}

func parseBenchOutputs(outputs []string) []benchResult {
	var results []benchResult

	var lines []string
	for _, o := range outputs {
		trimmed := strings.TrimRight(o, "\n\r")
		if trimmed == "" {
			continue
		}
		if strings.HasPrefix(trimmed, "goos:") ||
			strings.HasPrefix(trimmed, "goarch:") ||
			strings.HasPrefix(trimmed, "pkg:") ||
			strings.HasPrefix(trimmed, "cpu:") ||
			strings.HasPrefix(trimmed, "ok ") ||
			strings.HasPrefix(trimmed, "FAIL") ||
			strings.HasPrefix(trimmed, "PASS") ||
			strings.HasPrefix(trimmed, "coverage:") {
			continue
		}
		lines = append(lines, trimmed)
	}

	for i := 0; i < len(lines); i++ {
		line := lines[i]

		if strings.Contains(line, "ns/op") {
			b := parseBenchLine(line)
			if b != nil {
				results = append(results, *b)
			}
			continue
		}

		if reBenchHeader.MatchString(strings.TrimSpace(line)) {
			name := extractBenchName(strings.TrimSpace(line))
			if name == "" {
				continue
			}
			if i+1 < len(lines) {
				nextLine := lines[i+1]
				if strings.Contains(nextLine, "ns/op") {
					b := parseBenchDataLine(name, nextLine)
					if b != nil {
						results = append(results, *b)
					}
					i++
				}
			}
		}
	}

	return results
}

func extractBenchName(line string) string {
	line = strings.TrimRight(line, "\t ")
	parts := strings.SplitN(line, "-", 2)
	if len(parts) < 2 {
		return ""
	}
	name := parts[0]
	if !strings.HasPrefix(name, "Benchmark") {
		return ""
	}
	return name
}

func parseBenchLine(line string) *benchResult {
	name := extractBenchName(strings.TrimSpace(line))
	if name == "" {
		return nil
	}
	idx := strings.Index(line, "\t")
	if idx < 0 {
		return nil
	}
	remainder := line[idx:]
	remainder = strings.TrimLeft(remainder, "\t ")
	return parseBenchDataLine(name, remainder)
}

func parseBenchDataLine(name, line string) *benchResult {
	b := &benchResult{name: name}

	m := reBenchData.FindStringSubmatch(line)
	if m == nil {
		return nil
	}
	if _, err := fmt.Sscanf(m[1], "%d", &b.iter); err != nil {
		return nil
	}
	if _, err := fmt.Sscanf(m[2], "%f", &b.nsPerOp); err != nil {
		return nil
	}

	memMatch := reBenchMem.FindStringSubmatch(line)
	if memMatch != nil {
		b.memPerOp = memMatch[1] + " " + memMatch[2]
	}

	allocMatch := reBenchAllocs.FindStringSubmatch(line)
	if allocMatch != nil {
		b.allocs = allocMatch[1]
	}

	tpRegex := regexp.MustCompile(`([\d.]+)\s+MB/s`)
	tpMatch := tpRegex.FindStringSubmatch(line)
	if tpMatch != nil {
		b.extra = tpMatch[1] + " MB/s"
	}

	return b
}

func formatFloat(v float64) string {
	if v < 1 {
		return fmt.Sprintf("%.3f", v)
	}
	if v < 1000 {
		return fmt.Sprintf("%.1f", v)
	}
	if v < 1_000_000 {
		return fmt.Sprintf("%.0f", v)
	}
	return fmt.Sprintf("%.0f", v)
}

func shortPkg(pkg string) string {
	parts := strings.Split(pkg, "/")
	if len(parts) >= 2 {
		return strings.Join(parts[len(parts)-2:], "/")
	}
	return pkg
}

func evTest(key string) string {
	idx := strings.Index(key, "/")
	if idx >= 0 {
		return key[idx+1:]
	}
	return key
}

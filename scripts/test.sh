#!/usr/bin/env bash
# ============================================================================
#  Shark-MQTT Test Runner (Bash)
#  Platform: Linux / macOS / Git Bash / MSYS2 / WSL
#
#  All test runs automatically save timestamped logs to logs/ directory.
#  Log format: logs/{YYYYMMDD_HHmmss}_{type}.log
#
#  Usage:
#    ./scripts/test.sh              # run all (unit + integration + benchmark)
#    ./scripts/test.sh unit         # unit tests only
#    ./scripts/test.sh integration  # integration tests only
#    ./scripts/test.sh bench        # benchmark (set BENCHTIME, default 1s)
#    ./scripts/test.sh quick        # quick benchmark (500ms)
#    ./scripts/test.sh race         # unit tests with race detector
#    ./scripts/test.sh coverage     # generate coverage report
#    ./scripts/test.sh redis        # Redis store tests
#    ./scripts/test.sh ci           # full CI pipeline (vet + race + build)
#    ./scripts/test.sh help         # show this help
# ============================================================================

set -euo pipefail

# ---- Colors ----
RED="" GREEN="" YELLOW="" CYAN="" BOLD="" RESET=""
if [ -t 1 ]; then
  if command -v tput &>/dev/null && [ "$(tput colors 2>/dev/null || echo 0)" -ge 8 ]; then
    RED="$(tput setaf 1)"; GREEN="$(tput setaf 2)"; YELLOW="$(tput setaf 3)"
    CYAN="$(tput setaf 6)"; BOLD="$(tput bold)"; RESET="$(tput sgr0)"
  fi
fi

# ---- Paths ----
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
LOG_DIR="$PROJECT_ROOT/logs"

# ---- Helpers ----
info()  { printf "${CYAN}[INFO]${RESET}  %s\n" "$*"; }
ok()    { printf "${GREEN}[PASS]${RESET}  %s\n" "$*"; }
warn()  { printf "${YELLOW}[WARN]${RESET}  %s\n" "$*"; }
fail()  { printf "${RED}[FAIL]${RESET}  %s\n" "$*" >&2; }

timestamp() {
	local ts
	ts=$(date +"%Y%m%d_%H%M%S_%3N" 2>/dev/null)
	case "$ts" in
	*_[0-9][0-9][0-9]) echo "$ts" ;;
	*) echo "$(date +"%Y%m%d_%H%M%S")_000" ;;
	esac
}
human_ts() {
	local ts
	ts=$(date +"%Y-%m-%d %H:%M:%S.%3N" 2>/dev/null)
	case "$ts" in
	*.[0-9][0-9][0-9]) echo "$ts" ;;
	*) date +"%Y-%m-%d %H:%M:%S.000" ;;
	esac
}

ensure_log_dir() { mkdir -p "$LOG_DIR"; }

log_header() {
  local ts="$1" type="$2"
  echo "# ===================================================================="
  echo "#  Shark-MQTT ${type}"
  echo "#  Timestamp: ${ts}"
  echo "#  Started:   $(human_ts)"
  echo "# ===================================================================="
  echo ""
}

# ---- Target: unit ----
run_unit() {
  ensure_log_dir
  local ts
  ts=$(timestamp)
  local log_file="${LOG_DIR}/${ts}_unit.log"
  info "[$ts] Running unit tests..."
  info "  Log: $log_file"
  { log_header "$ts" "Unit Tests"; } > "$log_file"
  if go test -v -count=1 ./... 2>&1 | tee -a "$log_file"; then
    ok "Unit tests passed"
  else
    fail "Unit tests failed"
    return 1
  fi
}

# ---- Target: integration ----
run_integration() {
  ensure_log_dir
  local ts
  ts=$(timestamp)
  local log_file="${LOG_DIR}/${ts}_integration.log"
  info "[$ts] Running integration tests..."
  info "  Log: $log_file"
  { log_header "$ts" "Integration Tests"; } > "$log_file"
  if go test -v -count=1 -timeout 180s ./tests/integration/... 2>&1 | tee -a "$log_file"; then
    ok "Integration tests passed"
  else
    fail "Integration tests failed"
    return 1
  fi
}

# ---- Target: bench ----
run_bench() {
  ensure_log_dir
  local benchtime="${BENCHTIME:-1s}"
  local ts
  ts=$(timestamp)
  local log_file="${LOG_DIR}/${ts}_benchmark.log"
  info "[$ts] Running benchmarks (benchtime=${benchtime})..."
  info "  Log: $log_file"
  { log_header "$ts" "Benchmark Tests"; } > "$log_file"
  if go test -v -bench=. -benchmem -benchtime="$benchtime" -count=1 \
    -timeout 600s ./tests/bench/... 2>&1 | tee -a "$log_file"; then
    ok "Benchmarks passed"
  else
    fail "Benchmarks failed"
    return 1
  fi
}

# ---- Target: quick ----
run_quick() {
  BENCHTIME=500ms run_bench
}

# ---- Target: race ----
run_race() {
  ensure_log_dir
  local ts
  ts=$(timestamp)
  local log_file="${LOG_DIR}/${ts}_race.log"
  info "[$ts] Running race detector..."
  info "  Log: $log_file"
  { log_header "$ts" "Race Detector"; } > "$log_file"
  if go test -v -race -count=1 ./... 2>&1 | tee -a "$log_file"; then
    ok "Race detector: clean"
  else
    fail "Race detector found issues"
    return 1
  fi
}

# ---- Target: coverage ----
run_coverage() {
  ensure_log_dir
  local ts
  ts=$(timestamp)
  local log_file="${LOG_DIR}/${ts}_coverage.log"
  info "[$ts] Generating coverage report..."
  info "  Log: $log_file"
  { log_header "$ts" "Coverage Report"; } > "$log_file"
  {
    go test -coverprofile=coverage.out -covermode=atomic -count=1 ./... 2>&1 || true
    echo ""
    go tool cover -func=coverage.out 2>&1 || true
  } | tee -a "$log_file"
  go tool cover -html=coverage.out -o coverage.html 2>/dev/null || true
  ok "Coverage report: coverage.html + $log_file"
}

# ---- Target: redis ----
run_redis() {
  ensure_log_dir
  if [ -z "${MQTT_REDIS_ADDR:-}" ]; then
    warn "MQTT_REDIS_ADDR not set, using localhost:6379"
    export MQTT_REDIS_ADDR=localhost:6379
  fi
  local ts
  ts=$(timestamp)
  local log_file="${LOG_DIR}/${ts}_redis.log"
  info "[$ts] Running Redis tests (MQTT_REDIS_ADDR=$MQTT_REDIS_ADDR)..."
  info "  Log: $log_file"
  { log_header "$ts" "Redis Store Tests"; } > "$log_file"
  if go test -v -count=1 ./store/redis/... 2>&1 | tee -a "$log_file"; then
    ok "Redis tests passed"
  else
    fail "Redis tests failed"
    return 1
  fi
}

# ---- Target: ci ----
run_ci() {
  ensure_log_dir
  local ts
  ts=$(timestamp)
  local log_file="${LOG_DIR}/${ts}_ci.log"
  info "[$ts] Running CI pipeline..."
  info "  Log: $log_file"
  { log_header "$ts" "CI Pipeline"; } > "$log_file"

  local failed=0

  echo "--- go vet ---" | tee -a "$log_file"
  if ! go vet ./... 2>&1 | tee -a "$log_file"; then
    failed=1
  fi

  echo "--- go test -race ---" | tee -a "$log_file"
  if ! go test -v -race -count=1 ./... 2>&1 | tee -a "$log_file"; then
    failed=1
  fi

  echo "--- go build ---" | tee -a "$log_file"
  if ! go build -v ./... 2>&1 | tee -a "$log_file"; then
    failed=1
  fi

  if [ "$failed" -ne 0 ]; then
    fail "CI pipeline failed"
    return 1
  fi
  ok "CI pipeline passed"
}

# ---- Target: all ----
run_all() {
  ensure_log_dir

  local ts1 ts2 ts3 ts4
  local u_pass=0 u_skip=0 u_fail=0
  local i_pass=0 i_fail=0
  local b_cnt=0
  local all_failed=0

  # --- Batch 1: Unit ---
  ts1=$(timestamp)
  info "Batch 1 [$ts1] Unit tests started at $(human_ts)"
  local unit_pkgs=(api broker client config errs pkg plugin protocol store)
  for pkg in "${unit_pkgs[@]}"; do
    local pkg_log="${LOG_DIR}/${ts1}_unit_${pkg}.log"
    { log_header "$ts1" "Unit - ${pkg}"; } > "$pkg_log"
    local output
    if output=$(go test -v -count=1 "./${pkg}/..." 2>&1); then
      local p s
      p=$(echo "$output" | grep -c "^--- PASS" || true)
      s=$(echo "$output" | grep -c "^--- SKIP" || true)
      u_pass=$((u_pass + p)); u_skip=$((u_skip + s))
      echo "$output" >> "$pkg_log"
      ok "  ./${pkg}  PASS=${p}  SKIP=${s}"
    else
      local f
      f=$(echo "$output" | grep -c "^--- FAIL" || true)
      u_fail=$((u_fail + f)); all_failed=1
      echo "$output" >> "$pkg_log"
      fail "  ./${pkg}  FAIL=${f}"
    fi
  done
  ok "Batch 1 complete (${u_pass} pass, ${u_skip} skip, ${u_fail} fail)"

  # --- Batch 2: Integration ---
  ts2=$(timestamp)
  info "Batch 2 [$ts2] Integration tests started at $(human_ts)"
  local int_log="${LOG_DIR}/${ts2}_integration.log"
  { log_header "$ts2" "Integration Tests"; } > "$int_log"
  local int_output
  if int_output=$(go test -v -count=1 -timeout 180s ./tests/integration/... 2>&1); then
    i_pass=$(echo "$int_output" | grep -c "^--- PASS" || true)
    echo "$int_output" >> "$int_log"
    ok "Batch 2 complete (${i_pass} passed)"
  else
    i_fail=$(echo "$int_output" | grep -c "^--- FAIL" || true)
    all_failed=1
    echo "$int_output" >> "$int_log"
    fail "Batch 2 failed (${i_fail} failed)"
  fi

  # --- Batch 3: Benchmark ---
  ts3=$(timestamp)
  info "Batch 3 [$ts3] Benchmark tests started at $(human_ts)"
  local bench_log="${LOG_DIR}/${ts3}_benchmark.log"
  { log_header "$ts3" "Benchmark Tests"; } > "$bench_log"
  local bench_output
  bench_output=$(go test -v -bench=. -benchmem -count=1 -timeout 600s ./tests/bench/... 2>&1) || true
  b_cnt=$(echo "$bench_output" | grep -c "ns/op" || true)
  echo "$bench_output" >> "$bench_log"
  echo "$bench_output" | grep -E "(Benchmark|ns/op|^ok|^PASS)" || true
  ok "Batch 3 complete (${b_cnt} benchmarks)"

  # --- Summary ---
  ts4=$(timestamp)
  generate_summary "$ts1" "$ts2" "$ts3" "$ts4" \
    "$u_pass" "$u_skip" "$u_fail" "$i_pass" "$i_fail" "$b_cnt"

  if [ "$all_failed" -ne 0 ]; then
    fail "Some tests failed — see summary log"
    return 1
  fi
  ok "All tests completed."
}

# ---- Generate summary ----
generate_summary() {
  local ts1="$1" ts2="$2" ts3="$3" ts4="$4"
  local u_pass="$5" u_skip="$6" u_fail="$7"
  local i_pass="$8" i_fail="$9" b_cnt="${10}"
  local summary="${LOG_DIR}/${ts4}_summary.log"

  {
    echo "======================================================================"
    echo "  Shark-MQTT Test Summary"
    echo "  Generated: $(human_ts)"
    echo "======================================================================"
    echo ""
    echo "  Log Files:"
    echo "  ---------------------------------------------------"
    for f in "${LOG_DIR}/${ts1}"_unit_*.log; do
      [ -f "$f" ] || continue
      printf "    %-42s %s\n" "$(basename "$f")" "$(du -h "$f" | cut -f1)"
    done
    if [ -f "${LOG_DIR}/${ts2}_integration.log" ]; then
      printf "    %-42s %s\n" "$(basename "${LOG_DIR}/${ts2}_integration.log")" \
        "$(du -h "${LOG_DIR}/${ts2}_integration.log" | cut -f1)"
    fi
    if [ -f "${LOG_DIR}/${ts3}_benchmark.log" ]; then
      printf "    %-42s %s\n" "$(basename "${LOG_DIR}/${ts3}_benchmark.log")" \
        "$(du -h "${LOG_DIR}/${ts3}_benchmark.log" | cut -f1)"
    fi
    echo ""
    echo "  Results:"
    echo "  ---------------------------------------------------"
    printf "    %-16s %6s %6s %6s\n" "Type" "Pass" "Fail" "Skip"
    printf "    %-16s %6d %6d %6d\n" "Unit" "$u_pass" "$u_fail" "$u_skip"
    printf "    %-16s %6d %6d %6d\n" "Integration" "$i_pass" "$i_fail" "0"
    printf "    %-16s %6d %6s %6s\n" "Benchmark" "$b_cnt" "-" "-"
    echo "  ---------------------------------------------------"
    printf "    %-16s %6d %6d %6d\n" "Total" "$((u_pass + i_pass + b_cnt))" \
      "$((u_fail + i_fail))" "$u_skip"
    echo ""
    echo "  Batches:"
    echo "    Batch 1 (Unit):        ${ts1}"
    echo "    Batch 2 (Integration): ${ts2}"
    echo "    Batch 3 (Benchmark):   ${ts3}"
    echo "======================================================================"
  } | tee "$summary"
}

# ---- Main ----
case "${1:-all}" in
  unit)         run_unit ;;
  integration)  run_integration ;;
  bench)        run_bench ;;
  quick)        run_quick ;;
  race)         run_race ;;
  coverage)     run_coverage ;;
  redis)        run_redis ;;
  ci)           run_ci ;;
  all)          run_all ;;
  help|--help|-h)
    echo "Shark-MQTT Test Runner"
    echo ""
    echo "Usage: $0 <target>"
    echo ""
    echo "Targets:"
    echo "  all           Run unit + integration + benchmark (default)"
    echo "  unit          Unit tests"
    echo "  integration   Integration tests"
    echo "  bench         Benchmark (set BENCHTIME, default 1s)"
    echo "  quick         Quick benchmark (500ms)"
    echo "  race          Unit tests with race detector"
    echo "  coverage      Coverage report"
    echo "  redis         Redis store tests"
    echo "  ci            Full CI pipeline (vet + race + build)"
    echo "  help          Show this help"
    echo ""
    echo "All targets save timestamped logs to logs/ directory."
    ;;
  *)
    fail "Unknown target: $1  (run '$0 help' for usage)"
    ;;
esac

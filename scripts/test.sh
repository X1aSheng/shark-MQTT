#!/usr/bin/env bash
# ============================================================================
#  Shark-MQTT Test Runner (Bash)
#  Platform: Linux / macOS / Git Bash / MSYS2 / WSL
#
#  Usage:
#    ./scripts/test.sh              # run all (unit + integration + benchmark)
#    ./scripts/test.sh unit         # unit tests only
#    ./scripts/test.sh integration  # integration tests only
#    ./scripts/test.sh bench         # benchmark tests only
#    ./scripts/test.sh quick         # quick benchmark (1s per test)
#    ./scripts/test.sh race          # unit tests with race detector
#    ./scripts/test.sh coverage      # generate coverage report
#    ./scripts/test.sh redis         # Redis store tests (needs MQTT_REDIS_ADDR)
#    ./scripts/test.sh log           # run all with timestamped logs
# ============================================================================

set -euo pipefail

# ---- Colors ( degrade gracefully on terminals without color ) ----
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
fail()  { printf "${RED}[FAIL]${RESET}  %s\n" "$*" >&2; exit 1; }

timestamp() { date +"%Y%m%d_%H%M%S"; }
human_ts()  { date +"%Y-%m-%d %H:%M:%S"; }

ensure_log_dir() {
  mkdir -p "$LOG_DIR"
}

# ---- Target: unit ----
run_unit() {
  info "Running unit tests..."
  local pkgs=(
    "api"
    "broker"
    "client"
    "config"
    "errs"
    "pkg/..."
    "plugin"
    "protocol"
    "store/..."
  )
  local pass=0 skip=0 fail_cnt=0 total=0
  for pkg in "${pkgs[@]}"; do
    info "  -> ./${pkg}"
    if output=$(go test -v -count=1 "./${pkg}/" 2>&1); then
      p=$(echo "$output" | grep -c "^--- PASS" || true)
      s=$(echo "$output" | grep -c "^--- SKIP" || true)
      pass=$((pass + p)); skip=$((skip + s)); total=$((total + p + s))
      ok "  ./${pkg}  PASS=${p}  SKIP=${s}"
    else
      f=$(echo "$output" | grep -c "^--- FAIL" || true)
      fail_cnt=$((fail_cnt + f)); total=$((total + f))
      fail "  ./${pkg}  FAIL=${f}"
      echo "$output"
    fi
  done
  echo ""
  printf "  ${BOLD}Unit tests: %d total, %d pass, %d skip, %d fail${RESET}\n" \
    "$total" "$pass" "$skip" "$fail_cnt"
  echo ""
  return $fail_cnt
}

# ---- Target: integration ----
run_integration() {
  info "Running integration tests..."
  local output
  if output=$(go test -v -count=1 -timeout 180s ./tests/integration/... 2>&1); then
    local p=$(echo "$output" | grep -c "^--- PASS" || true)
    ok "  Integration: ${p} passed"
    echo "$output"
    return 0
  else
    fail "  Integration tests failed"
    echo "$output"
    return 1
  fi
}

# ---- Target: bench ----
run_bench() {
  info "Running benchmark tests..."
  local benchtime="${BENCHTIME:-1s}"
  local output
  if output=$(go test -v -bench=. -benchmem -benchtime="$benchtime" -count=1 \
    -timeout 600s ./tests/bench/... 2>&1); then
    local cnt=$(echo "$output" | grep -c "ns/op" || true)
    ok "  Benchmark: ${cnt} tests"
    echo "$output" | grep -E "(Benchmark|ns/op|^ok|^PASS)"
    return 0
  else
    fail "  Benchmark tests failed"
    echo "$output"
    return 1
  fi
}

run_quick_bench() {
  BENCHTIME="500ms" run_bench
}

# ---- Target: race ----
run_race() {
  info "Running unit tests with race detector..."
  go test -v -race -count=1 ./...
  ok "Race detector: clean"
}

# ---- Target: coverage ----
run_coverage() {
  info "Generating coverage report..."
  go test -coverprofile=coverage.out -covermode=atomic -count=1 ./...
  go tool cover -html=coverage.out -o coverage.html
  ok "Report: coverage.html"
  go tool cover -func=coverage.out | grep total || true
}

# ---- Target: redis ----
run_redis() {
  if [ -z "${MQTT_REDIS_ADDR:-}" ]; then
    warn "MQTT_REDIS_ADDR not set, using localhost:6379"
    export MQTT_REDIS_ADDR=localhost:6379
  fi
  info "Running Redis store tests (MQTT_REDIS_ADDR=$MQTT_REDIS_ADDR)..."
  go test -v -count=1 ./store/redis/...
  ok "Redis store tests passed"
}

# ---- Target: log (run all with logs) ----
run_log() {
  ensure_log_dir
  local ts1 ts2 ts3 ts4

  # --- Batch 1: Unit ---
  ts1=$(timestamp)
  info "Batch 1 [${ts1}] Unit tests  started at $(human_ts)"
  for pkg in api broker client config errs pkg plugin protocol store; do
    go test -v -count=1 "./${pkg}/..." 2>&1 \
      | sed "1i# Timestamp: ${ts1}\n# Type: Unit - ${pkg}\n" \
      > "${LOG_DIR}/${ts1}_unit_${pkg}.log"
  done
  ok "Batch 1 complete"

  # --- Batch 2: Integration ---
  ts2=$(timestamp)
  info "Batch 2 [${ts2}] Integration tests  started at $(human_ts)"
  go test -v -count=1 -timeout 180s ./tests/integration/... 2>&1 \
    | sed "1i# Timestamp: ${ts2}\n# Type: Integration\n" \
    > "${LOG_DIR}/${ts2}_integration.log"
  ok "Batch 2 complete"

  # --- Batch 3: Benchmark ---
  ts3=$(timestamp)
  info "Batch 3 [${ts3}] Benchmark tests  started at $(human_ts)"
  go test -v -bench=. -benchmem -count=1 -timeout 600s ./tests/bench/... 2>&1 \
    | sed "1i# Timestamp: ${ts3}\n# Type: Benchmark\n" \
    > "${LOG_DIR}/${ts3}_benchmark.log"
  ok "Batch 3 complete"

  # --- Summary ---
  ts4=$(timestamp)
  generate_summary "$ts1" "$ts2" "$ts3" "$ts4"
  ok "Summary: ${LOG_DIR}/${ts4}_summary.log"
  info "All logs saved to ${LOG_DIR}/"
}

# ---- Generate summary from logs ----
generate_summary() {
  local ts1="$1" ts2="$2" ts3="$3" ts4="$4"
  local summary="${LOG_DIR}/${ts4}_summary.log"

  # Count unit results
  local u_pass=0 u_skip=0
  for f in "${LOG_DIR}/${ts1}"_unit_*.log; do
    [ -f "$f" ] || continue
    u_pass=$((u_pass + $(grep -c "^--- PASS" "$f" || true)))
    u_skip=$((u_skip + $(grep -c "^--- SKIP" "$f" || true)))
  done

  # Count integration results
  local i_pass=0 i_fail=0
  if [ -f "${LOG_DIR}/${ts2}_integration.log" ]; then
    i_pass=$(grep -c "^--- PASS" "${LOG_DIR}/${ts2}_integration.log" || true)
    i_fail=$(grep -c "^--- FAIL" "${LOG_DIR}/${ts2}_integration.log" || true)
  fi

  # Count benchmark results
  local b_cnt=0
  if [ -f "${LOG_DIR}/${ts3}_benchmark.log" ]; then
    b_cnt=$(grep -c "ns/op" "${LOG_DIR}/${ts3}_benchmark.log" || true)
  fi

  {
    echo "================================================================================"
    echo "  Shark-MQTT Test Summary"
    echo "  Generated: $(human_ts)"
    echo "================================================================================"
    echo ""
    echo "  Log Files:"
    echo "  ---------------------------------------------------"
    for f in "${LOG_DIR}/${ts1}"_unit_*.log; do
      [ -f "$f" ] && printf "    %-42s %s\n" "$(basename "$f")" "$(du -h "$f" | cut -f1)"
    done
    [ -f "${LOG_DIR}/${ts2}_integration.log" ] && \
      printf "    %-42s %s\n" "$(basename "${LOG_DIR}/${ts2}_integration.log")" "$(du -h "${LOG_DIR}/${ts2}_integration.log" | cut -f1)"
    [ -f "${LOG_DIR}/${ts3}_benchmark.log" ] && \
      printf "    %-42s %s\n" "$(basename "${LOG_DIR}/${ts3}_benchmark.log")" "$(du -h "${LOG_DIR}/${ts3}_benchmark.log" | cut -f1)"
    echo ""
    echo "  Results:"
    echo "  ---------------------------------------------------"
    printf "    %-16s %6s %6s %6s\n" "Type" "Pass" "Fail" "Skip"
    printf "    %-16s %6d %6d %6d\n" "Unit" "$u_pass" "0" "$u_skip"
    printf "    %-16s %6d %6d %6d\n" "Integration" "$i_pass" "$i_fail" "0"
    printf "    %-16s %6d %6s %6s\n" "Benchmark" "$b_cnt" "-" "-"
    echo "  ---------------------------------------------------"
    printf "    %-16s %6d %6d %6d\n" "Total" "$((u_pass + i_pass + b_cnt))" "$i_fail" "$u_skip"
    echo ""
    echo "  Batches:"
    echo "    Batch 1 (Unit):        ${ts1}"
    echo "    Batch 2 (Integration): ${ts2}"
    echo "    Batch 3 (Benchmark):   ${ts3}"
    echo "================================================================================"
  } | tee "$summary"
}

# ---- Target: all ----
run_all() {
  info "Running all tests..."
  echo ""
  run_unit
  echo ""
  run_integration
  echo ""
  run_quick_bench
  echo ""
  ok "All tests completed."
}

# ---- Main ----
case "${1:-all}" in
  unit)         run_unit ;;
  integration)  run_integration ;;
  bench)        run_bench ;;
  quick)        run_quick_bench ;;
  race)         run_race ;;
  coverage)     run_coverage ;;
  redis)        run_redis ;;
  log)          run_log ;;
  all)          run_all ;;
  help|--help|-h)
    echo "Shark-MQTT Test Runner"
    echo ""
    echo "Usage: $0 <target>"
    echo ""
    echo "Targets:"
    echo "  all           Run unit + integration + quick benchmark (default)"
    echo "  unit          Unit tests only"
    echo "  integration   Integration tests only"
    echo "  bench         Benchmark tests (set BENCHTIME, default 1s)"
    echo "  quick         Quick benchmark (500ms)"
    echo "  race          Unit tests with race detector"
    echo "  coverage      Generate coverage report"
    echo "  redis         Redis store tests"
    echo "  log           Run all with timestamped logs + summary"
    echo "  help          Show this help"
    ;;
  *)
    fail "Unknown target: $1  (run '$0 help' for usage)"
    ;;
esac

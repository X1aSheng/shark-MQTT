#!/usr/bin/env bash
#
# shark-mqtt test runner (Linux / macOS / Git Bash / WSL)
#
# Usage:
#   bash scripts/run_tests.sh                # run all (unit + integration + benchmark)
#   bash scripts/run_tests.sh --unit         # unit tests only
#   bash scripts/run_tests.sh --integration  # integration tests only
#   bash scripts/run_tests.sh --benchmark    # benchmarks only
#   bash scripts/run_tests.sh --cover        # coverage report
#   bash scripts/run_tests.sh --all          # same as default
#
# Logs saved to ./logs/ as <timestamp>_<type>.{json,log}
# Example: logs/20260428_190627_unit.json
#

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
LOGDIR="$PROJECT_DIR/logs"

mkdir -p "$LOGDIR"

TS=$(date +%Y%m%d_%H%M%S)

C_GREEN='\033[0;32m'
C_CYAN='\033[0;36m'
C_YELLOW='\033[0;33m'
C_NC='\033[0m'

# -------------------------------------------------------------------
# run_test  <name> <label> <go-test-args...>
#   Runs go test with -json, saves .json + parsed .log to $LOGDIR.
# -------------------------------------------------------------------
run_test() {
    local name="$1"; shift
    local label="$1"; shift

    local jsonfile="$LOGDIR/${TS}_${name}.json"
    local logfile="$LOGDIR/${TS}_${name}.log"

    echo ""
    echo -e "${C_CYAN}>>> [$label] Running...${C_NC}"
    echo -e "${C_CYAN}    JSON  => $jsonfile${C_NC}"
    echo -e "${C_CYAN}    Report=> $logfile${C_NC}"
    echo ""

    cd "$PROJECT_DIR"
    go test "$@" -json -v -count=1 -timeout 300s > "$jsonfile" 2>&1 || true
    go run scripts/parse_test_log.go "$jsonfile" > "$logfile" 2>/dev/null || true

    if [ -s "$logfile" ]; then
        cat "$logfile"
    fi

    echo ""
    echo -e "${C_GREEN}>>> [$label] Done. Log saved.${C_NC}"
}

# -------------------------------------------------------------------
# run_cover
#   Runs coverage and saves .log to $LOGDIR.
# -------------------------------------------------------------------
run_cover() {
    local logfile="$LOGDIR/${TS}_cover.log"

    echo ""
    echo -e "${C_YELLOW}========================================${C_NC}"
    echo -e "${C_YELLOW}  Coverage Report  $(date '+%Y-%m-%d %H:%M:%S')${C_NC}"
    echo -e "${C_YELLOW}========================================${C_NC}"
    echo ""

    cd "$PROJECT_DIR"
    go test ./... -count=1 -cover -timeout 300s > "$logfile" 2>&1 || true
    cat "$logfile"

    echo ""
    echo -e "${C_GREEN}>>> Coverage log: $logfile${C_NC}"
}

# -------------------------------------------------------------------
# Main
# -------------------------------------------------------------------
MODE="${1:---all}"

case "$MODE" in
    --unit)
        run_test unit "Unit Tests" \
            ./broker/... ./protocol/... ./store/... ./client/... \
            ./config/... ./errs/... ./pkg/... ./plugin/... ./api/...
        ;;
    --integration)
        run_test integration "Integration Tests" ./tests/integration/...
        ;;
    --benchmark)
        run_test benchmark "Benchmarks" \
            -bench=. -benchmem -run=^$ \
            ./tests/bench/... ./broker/... ./protocol/... ./store/... ./pkg/...
        ;;
    --cover)
        run_cover
        ;;
    --all)
        echo ""
        echo -e "${C_YELLOW}========================================${C_NC}"
        echo -e "${C_YELLOW}  shark-mqtt full test suite${C_NC}"
        echo -e "${C_YELLOW}  $(date '+%Y-%m-%d %H:%M:%S')${C_NC}"
        echo -e "${C_YELLOW}========================================${C_NC}"

        run_test unit "Unit Tests" \
            ./broker/... ./protocol/... ./store/... ./client/... \
            ./config/... ./errs/... ./pkg/... ./plugin/... ./api/...
        run_test integration "Integration Tests" ./tests/integration/...
        run_test benchmark "Benchmarks" \
            -bench=. -benchmem -run=^$ \
            ./tests/bench/... ./broker/... ./protocol/... ./store/... ./pkg/...

        echo ""
        echo -e "${C_GREEN}========================================${C_NC}"
        echo -e "${C_GREEN}  All tests complete.${C_NC}"
        echo -e "${C_GREEN}  Logs in: $LOGDIR/${C_NC}"
        echo -e "${C_GREEN}----------------------------------------${C_NC}"
        for f in "$LOGDIR/${TS}"_*; do
            [ -f "$f" ] && ls -lh "$f"
        done
        echo -e "${C_GREEN}========================================${C_NC}"
        ;;
    *)
        echo "Usage: bash scripts/run_tests.sh [--unit|--integration|--benchmark|--cover|--all]"
        exit 1
        ;;
esac

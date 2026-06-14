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
#   bash scripts/run_tests.sh --save-json    # also save raw JSON from go test -json
#
# Readable text logs (.log) are always saved to ./logs/.
# Raw JSON (.json) files are only saved when --save-json is used.
# Example: logs/20260428_190627_unit.log
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
#   Runs go test and saves .log to $LOGDIR.
#   When SAVE_JSON=1, also saves raw .json from go test -json.
# -------------------------------------------------------------------
run_test() {
    local name="$1"; shift
    local label="$1"; shift

    local logfile="$LOGDIR/${TS}_${name}.log"

    echo ""
    echo -e "${C_CYAN}>>> [$label] Running...${C_NC}"
    if [ "$SAVE_JSON" = "1" ]; then
        local jsonfile="$LOGDIR/${TS}_${name}.json"
        echo -e "${C_CYAN}    JSON  => $jsonfile${C_NC}"
    fi
    echo -e "${C_CYAN}    Report=> $logfile${C_NC}"
    echo ""

    cd "$PROJECT_DIR"
    set +e
    if [ "$SAVE_JSON" = "1" ]; then
        # Run with -json, save raw JSON, then parse to readable .log
        go test "$@" -json -v -count=1 -timeout 300s > "$jsonfile" 2>&1
        local status=$?
        set -e
        go run scripts/parse_test_log.go "$jsonfile" > "$logfile" 2>/dev/null || true
    else
        # Run with -v, save output directly as .log
        go test "$@" -v -count=1 -timeout 300s > "$logfile" 2>&1
        local status=$?
        set -e
    fi

    if [ -s "$logfile" ]; then
        cat "$logfile"
    fi

    echo ""
    if [ "$status" -ne 0 ]; then
        echo -e "\033[0;31m>>> [$label] Failed with exit code $status.${C_NC}"
        return "$status"
    fi
    echo -e "${C_GREEN}>>> [$label] Passed. Log saved.${C_NC}"
    return 0
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
    set +e
    go test ./... -count=1 -cover -timeout 300s > "$logfile" 2>&1
    local status=$?
    set -e
    cat "$logfile"

    echo ""
    if [ "$status" -ne 0 ]; then
        echo -e "\033[0;31m>>> Coverage failed with exit code $status.${C_NC}"
        return "$status"
    fi
    echo -e "${C_GREEN}>>> Coverage log: $logfile${C_NC}"
    return 0
}

# -------------------------------------------------------------------
# Main
# -------------------------------------------------------------------
SAVE_JSON=0
ARGS=()

for arg in "$@"; do
    case "$arg" in
        --save-json) SAVE_JSON=1 ;;
        --unit|--integration|--benchmark|--cover|--all) MODE="$arg" ;;
        --help|-h) echo "Usage: bash scripts/run_tests.sh [--unit|--integration|--benchmark|--cover|--all] [--save-json]"; exit 0 ;;
        *) echo "Unknown option: $arg"; exit 1 ;;
    esac
done

MODE="${MODE:---all}"

case "$MODE" in
    --unit)
        run_test unit "Unit Tests" \
            ./broker/... ./protocol/... ./store/... ./client/... \
            ./config/... ./errs/... ./pkg/... ./plugin/... ./api/... \
            ./tests/defects/...
        ;;
    --integration)
        run_test integration "Integration Tests" ./tests/integration/...
        ;;
    --benchmark)
        run_test benchmark "Benchmarks" \
            -bench=. -benchmem -benchtime=500ms -run=^$ \
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

        status=0

        run_test unit "Unit Tests" \
            ./broker/... ./protocol/... ./store/... ./client/... \
            ./config/... ./errs/... ./pkg/... ./plugin/... ./api/... \
            ./tests/defects/... || status=$?
        run_test integration "Integration Tests" ./tests/integration/... || status=$?
        run_test benchmark "Benchmarks" \
            -bench=. -benchmem -benchtime=500ms -run=^$ \
            ./tests/bench/... ./broker/... ./protocol/... ./store/... ./pkg/... || status=$?

        echo ""
        echo -e "${C_GREEN}========================================${C_NC}"
        echo -e "${C_GREEN}  All tests complete.${C_NC}"
        echo -e "${C_GREEN}  Logs in: $LOGDIR/${C_NC}"
        echo -e "${C_GREEN}----------------------------------------${C_NC}"
        for f in "$LOGDIR/${TS}"_*; do
            [ -f "$f" ] && ls -lh "$f"
        done
        echo -e "${C_GREEN}========================================${C_NC}"
        exit "$status"
        ;;
    *)
        echo "Usage: bash scripts/run_tests.sh [--unit|--integration|--benchmark|--cover|--all] [--save-json]"
        exit 1
        ;;
esac

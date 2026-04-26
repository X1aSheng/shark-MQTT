@echo off
REM ============================================================================
REM  Shark-MQTT Test Runner (Windows Batch)
REM  Platform: Windows CMD
REM
REM  All test runs automatically save timestamped logs to logs\ directory.
REM  Log format: logs\{YYYYMMDD_HHmmss}_{type}.log
REM
REM  Usage:
REM    scripts\test.bat              run all (unit + integration + benchmark)
REM    scripts\test.bat unit         unit tests only
REM    scripts\test.bat integration  integration tests only
REM    scripts\test.bat bench        benchmark (set BENCHTIME, default 1s)
REM    scripts\test.bat quick        quick benchmark (500ms)
REM    scripts\test.bat race         unit tests with race detector
REM    scripts\test.bat coverage     generate coverage report
REM    scripts\test.bat redis        Redis store tests
REM    scripts\test.bat ci           full CI pipeline (vet + race + build)
REM    scripts\test.bat help         show this help
REM ============================================================================

setlocal enabledelayedexpansion

set "TARGET=%~1"
if "%TARGET%"=="" set "TARGET=all"

set "SCRIPT_DIR=%~dp0"
set "PROJECT_ROOT=%SCRIPT_DIR%.."
set "LOG_DIR=%PROJECT_ROOT%\logs"

go version >nul 2>&1
if errorlevel 1 (
    echo [FAIL]  go not found in PATH
    exit /b 1
)

if "%TARGET%"=="help" goto :help
if "%TARGET%"=="--help" goto :help
if "%TARGET%"=="-h" goto :help
if "%TARGET%"=="unit" goto :unit
if "%TARGET%"=="integration" goto :integration
if "%TARGET%"=="bench" goto :bench
if "%TARGET%"=="quick" goto :quick
if "%TARGET%"=="race" goto :race
if "%TARGET%"=="coverage" goto :coverage
if "%TARGET%"=="redis" goto :redis
if "%TARGET%"=="ci" goto :ci
if "%TARGET%"=="all" goto :all

echo [FAIL]  Unknown target: %TARGET%
echo         Run 'scripts\test.bat help' for usage
exit /b 1

REM ---- Helper: get timestamp ----
:get_ts
for /f "tokens=*" %%t in ('powershell -noprofile -command "Get-Date -Format yyyyMMdd_HHmmss_fff"') do set "%~1=%%t"
goto :eof

REM ---- Helper: write log header ----
:write_header
REM %1=log file, %2=type name, %3=timestamp
(
    echo # ====================================================================
    echo #  Shark-MQTT %~2
    echo #  Timestamp: %~3
    for /f "tokens=*" %%d in ('powershell -noprofile -command "Get-Date -Format 'yyyy-MM-dd HH:mm:ss.fff'"') do echo #  Started:   %%d
    echo # ====================================================================
    echo.
) > "%~1"
goto :eof

REM ---- Helper: run command, save to log, display result ----
:run_logged
REM %1=log file, %2+=command
set "RL_LOG=%~1"
shift
(
    %*
) >> "%RL_LOG%" 2>&1
set "RL_EXIT=!errorlevel!"
type "%RL_LOG%"
if !RL_EXIT! neq 0 exit /b 1
goto :eof

REM ============================================================================
REM  Targets
REM ============================================================================

:unit
if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"
call :get_ts TS
set "LOG_FILE=%LOG_DIR%\%TS%_unit.log"
echo [INFO]  [%TS%] Running unit tests...
echo [INFO]    Log: %LOG_FILE%
call :write_header "%LOG_FILE%" "Unit Tests" "%TS%"
go test -v -count=1 ./... >> "%LOG_FILE%" 2>&1
if errorlevel 1 (
    type "%LOG_FILE%"
    echo [FAIL]  Unit tests failed
    exit /b 1
)
type "%LOG_FILE%"
echo [PASS]  Unit tests passed
goto :eof

:integration
if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"
call :get_ts TS
set "LOG_FILE=%LOG_DIR%\%TS%_integration.log"
echo [INFO]  [%TS%] Running integration tests...
echo [INFO]    Log: %LOG_FILE%
call :write_header "%LOG_FILE%" "Integration Tests" "%TS%"
go test -v -count=1 -timeout 180s ./tests/integration/... >> "%LOG_FILE%" 2>&1
if errorlevel 1 (
    type "%LOG_FILE%"
    echo [FAIL]  Integration tests failed
    exit /b 1
)
type "%LOG_FILE%"
echo [PASS]  Integration tests passed
goto :eof

:bench
if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"
if "%BENCHTIME%"=="" set "BENCHTIME=1s"
call :get_ts TS
set "LOG_FILE=%LOG_DIR%\%TS%_benchmark.log"
echo [INFO]  [%TS%] Running benchmarks (benchtime=%BENCHTIME%)...
echo [INFO]    Log: %LOG_FILE%
call :write_header "%LOG_FILE%" "Benchmark Tests" "%TS%"
go test -v -bench=. -benchmem -benchtime=%BENCHTIME% -count=1 -timeout 600s ./tests/bench/... >> "%LOG_FILE%" 2>&1
if errorlevel 1 (
    type "%LOG_FILE%"
    echo [FAIL]  Benchmarks failed
    exit /b 1
)
type "%LOG_FILE%"
echo [PASS]  Benchmarks passed
goto :eof

:quick
set "BENCHTIME=500ms"
goto :bench

:race
if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"
call :get_ts TS
set "LOG_FILE=%LOG_DIR%\%TS%_race.log"
echo [INFO]  [%TS%] Running race detector...
echo [INFO]    Log: %LOG_FILE%
call :write_header "%LOG_FILE%" "Race Detector" "%TS%"
go test -v -race -count=1 ./... >> "%LOG_FILE%" 2>&1
if errorlevel 1 (
    type "%LOG_FILE%"
    echo [FAIL]  Race detector found issues
    exit /b 1
)
type "%LOG_FILE%"
echo [PASS]  Race detector: clean
goto :eof

:coverage
if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"
call :get_ts TS
set "LOG_FILE=%LOG_DIR%\%TS%_coverage.log"
echo [INFO]  [%TS%] Generating coverage report...
echo [INFO]    Log: %LOG_FILE%
call :write_header "%LOG_FILE%" "Coverage Report" "%TS%"
go test -coverprofile=coverage.out -covermode=atomic -count=1 ./... >> "%LOG_FILE%" 2>&1
go tool cover -func=coverage.out >> "%LOG_FILE%" 2>&1
type "%LOG_FILE%"
go tool cover -html=coverage.out -o coverage.html 2>nul
echo [PASS]  Coverage report: coverage.html + %LOG_FILE%
goto :eof

:redis
if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"
if "%MQTT_REDIS_ADDR%"=="" (
    echo [WARN]  MQTT_REDIS_ADDR not set, using localhost:6379
    set "MQTT_REDIS_ADDR=localhost:6379"
)
call :get_ts TS
set "LOG_FILE=%LOG_DIR%\%TS%_redis.log"
echo [INFO]  [%TS%] Running Redis tests (MQTT_REDIS_ADDR=!MQTT_REDIS_ADDR!)...
echo [INFO]    Log: %LOG_FILE%
call :write_header "%LOG_FILE%" "Redis Store Tests" "%TS%"
go test -v -count=1 ./store/redis/... >> "%LOG_FILE%" 2>&1
if errorlevel 1 (
    type "%LOG_FILE%"
    echo [FAIL]  Redis tests failed
    exit /b 1
)
type "%LOG_FILE%"
echo [PASS]  Redis tests passed
goto :eof

:ci
if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"
call :get_ts TS
set "LOG_FILE=%LOG_DIR%\%TS%_ci.log"
echo [INFO]  [%TS%] Running CI pipeline...
echo [INFO]    Log: %LOG_FILE%
call :write_header "%LOG_FILE%" "CI Pipeline" "%TS%"
set "CI_FAILED=0"

echo --- go vet --- >> "%LOG_FILE%"
go vet ./... >> "%LOG_FILE%" 2>&1
if errorlevel 1 set "CI_FAILED=1"

echo --- go test -race --- >> "%LOG_FILE%"
go test -v -race -count=1 ./... >> "%LOG_FILE%" 2>&1
if errorlevel 1 set "CI_FAILED=1"

echo --- go build --- >> "%LOG_FILE%"
go build -v ./... >> "%LOG_FILE%" 2>&1
if errorlevel 1 set "CI_FAILED=1"

type "%LOG_FILE%"
if "!CI_FAILED!"=="1" (
    echo [FAIL]  CI pipeline failed
    exit /b 1
)
echo [PASS]  CI pipeline passed
goto :eof

REM ============================================================================
REM  Target: all (unit + integration + benchmark + summary)
REM ============================================================================

:all
if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"
set "ALL_FAILED=0"

REM --- Batch 1: Unit ---
call :get_ts TS1
echo [INFO]  Batch 1 [%TS1%] Unit tests started
set "U_PASS=0"
set "U_FAIL=0"
for %%p in (api broker client config errs pkg plugin protocol store) do (
    call :write_header "%LOG_DIR%\%TS1%_unit_%%p.log" "Unit - %%p" "%TS1%"
    echo [INFO]    - ./%%p/...
    go test -v -count=1 ./%%p/... >> "%LOG_DIR%\%TS1%_unit_%%p.log" 2>&1
    if errorlevel 1 (
        echo [FAIL]  ./%%p
        set "ALL_FAILED=1"
        set /a "U_FAIL+=1"
    ) else (
        echo [PASS]  ./%%p
        set /a "U_PASS+=1"
    )
)
echo [PASS]  Batch 1 complete (pass=!U_PASS!, fail=!U_FAIL!)

REM --- Batch 2: Integration ---
call :get_ts TS2
echo [INFO]  Batch 2 [%TS2%] Integration tests started
call :write_header "%LOG_DIR%\%TS2%_integration.log" "Integration Tests" "%TS2%"
go test -v -count=1 -timeout 180s ./tests/integration/... >> "%LOG_DIR%\%TS2%_integration.log" 2>&1
if errorlevel 1 (
    echo [FAIL]  Integration tests failed
    set "ALL_FAILED=1"
) else (
    echo [PASS]  Integration tests passed
)

REM --- Batch 3: Benchmark ---
call :get_ts TS3
echo [INFO]  Batch 3 [%TS3%] Benchmark tests started
call :write_header "%LOG_DIR%\%TS3%_benchmark.log" "Benchmark Tests" "%TS3%"
go test -v -bench=. -benchmem -benchtime=500ms -count=1 -timeout 600s ./tests/bench/... >> "%LOG_DIR%\%TS3%_benchmark.log" 2>&1
if errorlevel 1 (
    echo [FAIL]  Benchmark tests failed
) else (
    echo [PASS]  Benchmark tests passed
)

REM --- Summary ---
call :get_ts TS4
(
    echo ======================================================================
    echo   Shark-MQTT Test Summary
    for /f "tokens=*" %%d in ('powershell -noprofile -command "Get-Date -Format 'yyyy-MM-dd HH:mm:ss.fff'"') do echo   Generated: %%d
    echo ======================================================================
    echo.
    echo   Log Files:
    echo   ---------------------------------------------------
    for %%f in ("%LOG_DIR%\%TS1%_unit_*.log") do echo     %%~nxf
    echo     %TS2%_integration.log
    echo     %TS3%_benchmark.log
    echo.
    echo   Batches:
    echo     Batch 1 ^(Unit^):        %TS1%
    echo     Batch 2 ^(Integration^): %TS2%
    echo     Batch 3 ^(Benchmark^):   %TS3%
    echo ======================================================================
) > "%LOG_DIR%\%TS4%_summary.log"
type "%LOG_DIR%\%TS4%_summary.log"

if "!ALL_FAILED!"=="1" (
    echo [FAIL]  Some tests failed - see logs
    exit /b 1
)
echo [PASS]  All tests completed.
goto :eof

REM ============================================================================
REM  Help
REM ============================================================================

:help
echo Shark-MQTT Test Runner (Windows Batch)
echo.
echo Usage: scripts\test.bat ^<target^>
echo.
echo Targets:
echo   all           Run unit + integration + benchmark (default)
echo   unit          Unit tests
echo   integration   Integration tests
echo   bench         Benchmark (set BENCHTIME, default 1s)
echo   quick         Quick benchmark (500ms)
echo   race          Unit tests with race detector
echo   coverage      Coverage report
echo   redis         Redis store tests
echo   ci            Full CI pipeline (vet + race + build)
echo   help          Show this help
echo.
echo All targets save timestamped logs to logs\ directory.
goto :eof

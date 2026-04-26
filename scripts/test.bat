@echo off
REM ============================================================================
REM  Shark-MQTT Test Runner (Windows Batch)
REM  Platform: Windows CMD
REM
REM  Usage:
REM    scripts\test.bat              run all (unit + integration + quick bench)
REM    scripts\test.bat unit         unit tests only
REM    scripts\test.bat integration  integration tests only
REM    scripts\test.bat bench         benchmark tests only
REM    scripts\test.bat quick         quick benchmark (500ms)
REM    scripts\test.bat race          unit tests with race detector
REM    scripts\test.bat coverage      generate coverage report
REM    scripts\test.bat redis         Redis store tests
REM    scripts\test.bat log           run all with timestamped logs
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
if "%TARGET%"=="log" goto :log
if "%TARGET%"=="all" goto :all

echo [FAIL]  Unknown target: %TARGET%
echo         Run 'scripts\test.bat help' for usage
exit /b 1

:unit
echo [INFO]  Running unit tests...
set "UNIT_PKGS=api broker client config errs pkg plugin protocol store"
for %%p in (%UNIT_PKGS%) do (
    echo [INFO]    - ./%%p/...
    go test -v -count=1 ./%%p/... 2>&1
    if errorlevel 1 (
        echo [FAIL]  ./%%p failed
        exit /b 1
    )
)
echo [PASS]  Unit tests passed
goto :eof

:integration
echo [INFO]  Running integration tests...
go test -v -count=1 -timeout 180s ./tests/integration/...
if errorlevel 1 (
    echo [FAIL]  Integration tests failed
    exit /b 1
)
echo [PASS]  Integration tests passed
goto :eof

:bench
echo [INFO]  Running benchmark tests...
set "BENCHTIME=%BENCHTIME%"
if "%BENCHTIME%"=="" set "BENCHTIME=1s"
go test -v -bench=. -benchmem -benchtime=%BENCHTIME% -count=1 -timeout 600s ./tests/bench/...
if errorlevel 1 (
    echo [FAIL]  Benchmark tests failed
    exit /b 1
)
echo [PASS]  Benchmark tests passed
goto :eof

:quick
set "BENCHTIME=500ms"
goto :bench

:race
echo [INFO]  Running unit tests with race detector...
go test -v -race -count=1 ./...
if errorlevel 1 (
    echo [FAIL]  Race detector found issues
    exit /b 1
)
echo [PASS]  Race detector: clean
goto :eof

:coverage
echo [INFO]  Generating coverage report...
go test -coverprofile=coverage.out -covermode=atomic -count=1 ./...
go tool cover -html=coverage.out -o coverage.html
echo [PASS]  Report: coverage.html
go tool cover -func=coverage.out | findstr "total"
goto :eof

:redis
if "%MQTT_REDIS_ADDR%"=="" (
    echo [WARN]  MQTT_REDIS_ADDR not set, using localhost:6379
    set "MQTT_REDIS_ADDR=localhost:6379"
)
echo [INFO]  Running Redis store tests ^(MQTT_REDIS_ADDR=!MQTT_REDIS_ADDR!^)...
go test -v -count=1 ./store/redis/...
if errorlevel 1 (
    echo [FAIL]  Redis store tests failed
    exit /b 1
)
echo [PASS]  Redis store tests passed
goto :eof

:log
if not exist "%LOG_DIR%" mkdir "%LOG_DIR%"

REM --- Batch 1: Unit ---
for /f "tokens=*" %%t in ('powershell -noprofile -command "Get-Date -Format yyyyMMdd_HHmmss"') do set "TS1=%%t"
echo [INFO]  Batch 1 [%TS1%] Unit tests started
for %%p in (api broker client config errs pkg plugin protocol store) do (
    echo [INFO]    - %%p
    (
        echo # Timestamp: %TS1%
        echo # Type: Unit - %%p
        echo.
    ) > "%LOG_DIR%\%TS1%_unit_%%p.log"
    go test -v -count=1 ./%%p/... >> "%LOG_DIR%\%TS1%_unit_%%p.log" 2>&1
)
echo [PASS]  Batch 1 complete

REM --- Batch 2: Integration ---
for /f "tokens=*" %%t in ('powershell -noprofile -command "Get-Date -Format yyyyMMdd_HHmmss"') do set "TS2=%%t"
echo [INFO]  Batch 2 [%TS2%] Integration tests started
(
    echo # Timestamp: %TS2%
    echo # Type: Integration
    echo.
) > "%LOG_DIR%\%TS2%_integration.log"
go test -v -count=1 -timeout 180s ./tests/integration/... >> "%LOG_DIR%\%TS2%_integration.log" 2>&1
echo [PASS]  Batch 2 complete

REM --- Batch 3: Benchmark ---
for /f "tokens=*" %%t in ('powershell -noprofile -command "Get-Date -Format yyyyMMdd_HHmmss"') do set "TS3=%%t"
echo [INFO]  Batch 3 [%TS3%] Benchmark tests started
(
    echo # Timestamp: %TS3%
    echo # Type: Benchmark
    echo.
) > "%LOG_DIR%\%TS3%_benchmark.log"
go test -v -bench=. -benchmem -count=1 -timeout 600s ./tests/bench/... >> "%LOG_DIR%\%TS3%_benchmark.log" 2>&1
echo [PASS]  Batch 3 complete

echo [PASS]  All logs saved to %LOG_DIR%\
goto :eof

:all
echo [INFO]  Running all tests...
echo.
call :unit
if errorlevel 1 exit /b 1
echo.
call :integration
if errorlevel 1 exit /b 1
echo.
set "BENCHTIME=500ms"
call :bench
if errorlevel 1 exit /b 1
echo.
echo [PASS]  All tests completed.
goto :eof

:help
echo Shark-MQTT Test Runner (Windows Batch)
echo.
echo Usage: scripts\test.bat ^<target^>
echo.
echo Targets:
echo   all           Run unit + integration + quick benchmark (default)
echo   unit          Unit tests only
echo   integration   Integration tests only
echo   bench         Benchmark tests (set BENCHTIME, default 1s)
echo   quick         Quick benchmark (500ms)
echo   race          Unit tests with race detector
echo   coverage      Generate coverage report
echo   redis         Redis store tests
echo   log           Run all with timestamped logs
echo   help          Show this help
goto :eof

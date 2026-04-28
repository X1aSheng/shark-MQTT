@echo off
setlocal EnableExtensions

REM ============================================================
REM  shark-mqtt test runner for Windows CMD
REM
REM  Usage:
REM    scripts\run_tests.bat                -- run all
REM    scripts\run_tests.bat --unit         -- unit tests only
REM    scripts\run_tests.bat --integration  -- integration tests only
REM    scripts\run_tests.bat --benchmark    -- benchmarks only
REM    scripts\run_tests.bat --cover        -- coverage report
REM    scripts\run_tests.bat --all          -- same as default
REM
REM  Logs saved to .\logs\ as <timestamp>_<type>.{json,log}
REM  Example: logs\20260428_190627_unit.json
REM ============================================================

set "SCRIPT_DIR=%~dp0"
set "PROJECT_DIR=%SCRIPT_DIR%.."
set "LOGDIR=%PROJECT_DIR%\logs"

if not exist "%LOGDIR%" mkdir "%LOGDIR%"

REM --- Locale-independent timestamp: YYYYMMDD_HHMMSS ---
for /f %%a in ('powershell -NoProfile -Command "Get-Date -Format yyyyMMdd_HHmmss"') do set "TS=%%a"

set "MODE=%~1"
if not defined MODE set "MODE=--all"

if "%MODE%"=="--unit"        goto :do_unit
if "%MODE%"=="--integration" goto :do_integration
if "%MODE%"=="--benchmark"   goto :do_benchmark
if "%MODE%"=="--cover"       goto :do_cover
if "%MODE%"=="--all"         goto :do_all
goto :usage

REM ============================================================
:do_unit
call :run_test unit "Unit Tests" ./broker/... ./protocol/... ./store/... ./client/... ./config/... ./errs/... ./pkg/... ./plugin/... ./api/...
goto :end

:do_integration
call :run_test integration "Integration Tests" ./tests/integration/...
goto :end

:do_benchmark
call :run_test benchmark "Benchmarks" -bench=. -benchmem -run=^^ ./tests/bench/... ./broker/... ./protocol/... ./store/... ./pkg/...
goto :end

:do_cover
echo.
echo [33m========================================[0m
echo [33m  Coverage Report  %DATE% %TIME%[0m
echo [33m========================================[0m
echo.
pushd "%PROJECT_DIR%"
go test ./... -count=1 -cover -timeout 300s > "%LOGDIR%\%TS%_cover.log" 2>&1
popd
type "%LOGDIR%\%TS%_cover.log"
echo.
echo [32m^>^>^> Coverage log: %LOGDIR%\%TS%_cover.log[0m
goto :end

:do_all
echo.
echo [33m========================================[0m
echo [33m  shark-mqtt full test suite[0m
echo [33m  %DATE% %TIME%[0m
echo [33m========================================[0m

call :run_test unit "Unit Tests" ./broker/... ./protocol/... ./store/... ./client/... ./config/... ./errs/... ./pkg/... ./plugin/... ./api/...
call :run_test integration "Integration Tests" ./tests/integration/...
call :run_test benchmark "Benchmarks" -bench=. -benchmem -run=^^ ./tests/bench/... ./broker/... ./protocol/... ./store/... ./pkg/...

echo.
echo [32m========================================[0m
echo [32m  All tests complete.[0m
echo [32m  Logs in: %LOGDIR%[0m
echo [32m----------------------------------------[0m
dir /b "%LOGDIR%\%TS%_*" 2>nul
echo [32m========================================[0m
goto :end

REM ============================================================
REM  :run_test  name label args...
REM    Each mode calls this directly with hardcoded args.
REM ============================================================
:run_test
set "RT_NAME=%~1"
set "RT_LABEL=%~2"

set "RT_JSON=%LOGDIR%\%TS%_%RT_NAME%.json"
set "RT_LOG=%LOGDIR%\%TS%_%RT_NAME%.log"

echo.
echo [36m^>^>^> [%RT_LABEL%] Running...[0m
echo [36m    JSON  =^> %RT_JSON%[0m
echo [36m    Report=^> %RT_LOG%[0m
echo.

pushd "%PROJECT_DIR%"
shift /1 & shift /1
go test %* -json -v -count=1 -timeout 300s > "%RT_JSON%" 2>&1
go run scripts/parse_test_log.go "%RT_JSON%" > "%RT_LOG%" 2>nul
popd

if exist "%RT_LOG%" type "%RT_LOG%"
echo.
echo [32m^>^>^> [%RT_LABEL%] Done. Log saved.[0m
goto :eof

REM ============================================================
:usage
echo.
echo  Usage: scripts\run_tests.bat [--unit --integration --benchmark --cover --all]
echo.
echo    --unit         Unit tests only
echo    --integration  Integration tests only
echo    --benchmark    Benchmarks only
echo    --cover        Coverage report
echo    --all          Run all (default)
echo.
exit /b 1

:end
endlocal

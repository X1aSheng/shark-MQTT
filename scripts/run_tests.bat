@echo off
setlocal EnableExtensions EnableDelayedExpansion

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
REM    scripts\run_tests.bat --save-json    -- also save raw JSON files
REM
REM  Readable text logs (.log) are always saved to .\logs\.
REM  Raw JSON (.json) is only saved when --save-json is used.
REM  Example: logs\20260428_190627_unit.log
REM ============================================================

set "SCRIPT_DIR=%~dp0"
set "PROJECT_DIR=%SCRIPT_DIR%.."
set "LOGDIR=%PROJECT_DIR%\logs"

if not exist "%LOGDIR%" mkdir "%LOGDIR%"

REM --- Locale-independent timestamp: YYYYMMDD_HHMMSS ---
for /f %%a in ('powershell -NoProfile -Command "Get-Date -Format yyyyMMdd_HHmmss"') do set "TS=%%a"

set "MODE=--all"
set "SAVE_JSON=0"
set "STATUS=0"

:parse_args
if "%~1"=="" goto :done_parse
if "%~1"=="--save-json" set "SAVE_JSON=1" & shift /1 & goto :parse_args
if "%~1"=="--unit" set "MODE=--unit" & shift /1 & goto :parse_args
if "%~1"=="--integration" set "MODE=--integration" & shift /1 & goto :parse_args
if "%~1"=="--benchmark" set "MODE=--benchmark" & shift /1 & goto :parse_args
if "%~1"=="--cover" set "MODE=--cover" & shift /1 & goto :parse_args
if "%~1"=="--all" set "MODE=--all" & shift /1 & goto :parse_args
if "%~1"=="/?" goto :usage
echo Unknown option: %~1
exit /b 1

:done_parse
if "%MODE%"=="--unit"        goto :do_unit
if "%MODE%"=="--integration" goto :do_integration
if "%MODE%"=="--benchmark"   goto :do_benchmark
if "%MODE%"=="--cover"       goto :do_cover
if "%MODE%"=="--all"         goto :do_all
goto :usage

REM ============================================================
:do_unit
call :run_test unit "Unit Tests" ./broker/... ./protocol/... ./store/... ./client/... ./config/... ./errs/... ./pkg/... ./plugin/... ./api/... ./tests/defects/...
set "STATUS=%ERRORLEVEL%"
goto :end

:do_integration
call :run_test integration "Integration Tests" ./tests/integration/...
set "STATUS=%ERRORLEVEL%"
goto :end

:do_benchmark
call :run_test benchmark "Benchmarks" -bench=. -benchmem -run=^^ ./tests/bench/... ./broker/... ./protocol/... ./store/... ./pkg/...
set "STATUS=%ERRORLEVEL%"
goto :end

:do_cover
echo.
echo [33m========================================[0m
echo [33m  Coverage Report  %DATE% %TIME%[0m
echo [33m========================================[0m
echo.
pushd "%PROJECT_DIR%"
go test ./... -count=1 -cover -timeout 300s > "%LOGDIR%\%TS%_cover.log" 2>&1
set "STATUS=%ERRORLEVEL%"
popd
type "%LOGDIR%\%TS%_cover.log"
echo.
if not "%STATUS%"=="0" (
    echo [31m^>^>^> Coverage failed with exit code %STATUS%.[0m
) else (
    echo [32m^>^>^> Coverage log: %LOGDIR%\%TS%_cover.log[0m
)
goto :end

:do_all
echo.
echo [33m========================================[0m
echo [33m  shark-mqtt full test suite[0m
echo [33m  %DATE% %TIME%[0m
echo [33m========================================[0m

call :run_test unit "Unit Tests" ./broker/... ./protocol/... ./store/... ./client/... ./config/... ./errs/... ./pkg/... ./plugin/... ./api/... ./tests/defects/...
if errorlevel 1 set "STATUS=1"
call :run_test integration "Integration Tests" ./tests/integration/...
if errorlevel 1 set "STATUS=1"
call :run_test benchmark "Benchmarks" -bench=. -benchmem -run=^^ ./tests/bench/... ./broker/... ./protocol/... ./store/... ./pkg/...
if errorlevel 1 set "STATUS=1"

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
REM    When SAVE_JSON=1, also saves raw JSON from go test -json.
REM ============================================================
:run_test
set "RT_NAME=%~1"
set "RT_LABEL=%~2"

set "RT_LOG=%LOGDIR%\%TS%_%RT_NAME%.log"

echo.
echo [36m^>^>^> [%RT_LABEL%] Running...[0m
if "%SAVE_JSON%"=="1" (
    set "RT_JSON=%LOGDIR%\%TS%_%RT_NAME%.json"
    echo [36m    JSON  =^> !RT_JSON![0m
)
echo [36m    Report=^> %RT_LOG%[0m
echo.

pushd "%PROJECT_DIR%"
set "RT_ARGS="
:collect_args
if "%~3"=="" goto :run_go_test
set "RT_ARGS=!RT_ARGS! %3"
shift /3
goto :collect_args

:run_go_test
if "%SAVE_JSON%"=="1" (
    go test !RT_ARGS! -json -v -count=1 -timeout 300s > "!RT_JSON!" 2>&1
    set "RT_STATUS=!ERRORLEVEL!"
    go run scripts/parse_test_log.go "!RT_JSON!" > "%RT_LOG%" 2>nul
) else (
    go test !RT_ARGS! -v -count=1 -timeout 300s > "%RT_LOG%" 2>&1
    set "RT_STATUS=!ERRORLEVEL!"
)
popd

if exist "%RT_LOG%" type "%RT_LOG%"
echo.
if not "%RT_STATUS%"=="0" (
    echo [31m^>^>^> [%RT_LABEL%] Failed with exit code %RT_STATUS%.[0m
    exit /b %RT_STATUS%
)
echo [32m^>^>^> [%RT_LABEL%] Passed. Log saved.[0m
exit /b 0

REM ============================================================
:usage
echo.
echo  Usage: scripts\run_tests.bat [--unit --integration --benchmark --cover --all] [--save-json]
echo.
echo    --unit         Unit tests only
echo    --integration  Integration tests only
echo    --benchmark    Benchmarks only
echo    --cover        Coverage report
echo    --all          Run all (default)
echo    --save-json    Also save raw JSON output from go test -json
echo.
exit /b 1

:end
endlocal & exit /b %STATUS%

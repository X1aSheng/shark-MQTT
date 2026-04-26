# ============================================================================
#  Shark-MQTT Test Runner (PowerShell)
#  Platform: Windows (PowerShell 5.1+ / PowerShell 7)
#
#  Usage:
#    .\scripts\test.ps1              # run all (unit + integration + benchmark)
#    .\scripts\test.ps1 unit         # unit tests only
#    .\scripts\test.ps1 integration  # integration tests only
#    .\scripts\test.ps1 bench         # benchmark tests only
#    .\scripts\test.ps1 quick         # quick benchmark (500ms)
#    .\scripts\test.ps1 race          # unit tests with race detector
#    .\scripts\test.ps1 coverage      # generate coverage report
#    .\scripts\test.ps1 redis         # Redis store tests
#    .\scripts\test.ps1 log           # run all with timestamped logs
# ============================================================================

$ErrorActionPreference = "Stop"

# ---- Paths ----
$ScriptDir = Split-Path -Parent $MyInvocation.MyCommand.Path
$ProjectRoot = Resolve-Path (Join-Path $ScriptDir "..")
$LogDir = Join-Path $ProjectRoot "logs"

# ---- Helpers ----
function Write-Info($msg)  { Write-Host "[INFO]  $msg" -ForegroundColor Cyan }
function Write-Pass($msg)  { Write-Host "[PASS]  $msg" -ForegroundColor Green }
function Write-Warn($msg)  { Write-Host "[WARN]  $msg" -ForegroundColor Yellow }
function Write-Fail($msg)  { Write-Host "[FAIL]  $msg" -ForegroundColor Red }

function Get-Timestamp { Get-Date -Format "yyyyMMdd_HHmmss" }
function Get-HumanTs   { Get-Date -Format "yyyy-MM-dd HH:mm:ss" }

function Count-InFile($Path, $Pattern) {
    if (Test-Path $Path) {
        return (Select-String -Path $Path -Pattern $Pattern -SimpleMatch).Count
    }
    return 0
}

# ---- Target: unit ----
function Run-Unit {
    Write-Info "Running unit tests..."
    $pkgs = @("api","broker","client","config","errs","pkg/...","plugin","protocol","store/...")
    $script:pass = 0; $script:skip = 0; $script:failCnt = 0

    foreach ($pkg in $pkgs) {
        Write-Info "  -> ./$pkg"
        $output = go test -v -count=1 "./$pkg" 2>&1 | Out-String
        $p = ([regex]::Matches($output, "^--- PASS", "Multiline")).Count
        $s = ([regex]::Matches($output, "^--- SKIP", "Multiline")).Count
        $f = ([regex]::Matches($output, "^--- FAIL", "Multiline")).Count
        $script:pass += $p; $script:skip += $s; $script:failCnt += $f

        if ($f -gt 0) {
            Write-Fail "  ./$pkg  FAIL=$f"
            Write-Host $output
        } else {
            Write-Pass "  ./$pkg  PASS=$p  SKIP=$s"
        }
    }

    Write-Host ""
    $total = $script:pass + $script:skip + $script:failCnt
    Write-Host ("  Unit tests: {0} total, {1} pass, {2} skip, {3} fail" -f `
        $total, $script:pass, $script:skip, $script:failCnt)
    Write-Host ""
    if ($script:failCnt -gt 0) { return $false }
    return $true
}

# ---- Target: integration ----
function Run-Integration {
    Write-Info "Running integration tests..."
    $output = go test -v -count=1 -timeout 180s ./tests/integration/... 2>&1 | Out-String
    $p = ([regex]::Matches($output, "^--- PASS", "Multiline")).Count
    $f = ([regex]::Matches($output, "^--- FAIL", "Multiline")).Count

    if ($f -gt 0) {
        Write-Fail "  Integration: $f failed"
        Write-Host $output
        return $false
    }
    Write-Pass "  Integration: $p passed"
    return $true
}

# ---- Target: bench ----
function Run-Bench {
    param([string]$BenchTime = "1s")
    Write-Info "Running benchmark tests (benchtime=$BenchTime)..."
    $output = go test -v -bench=. -benchmem -benchtime=$BenchTime -count=1 `
        -timeout 600s ./tests/bench/... 2>&1 | Out-String
    $cnt = ([regex]::Matches($output, "ns/op")).Count

    if ($LASTEXITCODE -ne 0 -and $cnt -eq 0) {
        Write-Fail "  Benchmark failed"
        Write-Host $output
        return $false
    }
    Write-Pass "  Benchmark: $cnt tests"
    # Print summary lines
    $output -split "`n" | Where-Object { $_ -match "(Benchmark.*ns/op|^ok|PASS)" } | ForEach-Object {
        Write-Host "  $_"
    }
    return $true
}

function Run-QuickBench {
    Run-Bench -BenchTime "500ms"
}

# ---- Target: race ----
function Run-Race {
    Write-Info "Running unit tests with race detector..."
    go test -v -race -count=1 ./...
    Write-Pass "Race detector: clean"
}

# ---- Target: coverage ----
function Run-Coverage {
    Write-Info "Generating coverage report..."
    go test -coverprofile=coverage.out -covermode=atomic -count=1 ./...
    go tool cover -html=coverage.out -o coverage.html
    Write-Pass "Report: coverage.html"
    go tool cover -func=coverage.out | Select-String "total"
}

# ---- Target: redis ----
function Run-Redis {
    if (-not $env:MQTT_REDIS_ADDR) {
        Write-Warn "MQTT_REDIS_ADDR not set, using localhost:6379"
        $env:MQTT_REDIS_ADDR = "localhost:6379"
    }
    Write-Info "Running Redis store tests (MQTT_REDIS_ADDR=$env:MQTT_REDIS_ADDR)..."
    go test -v -count=1 ./store/redis/...
    Write-Pass "Redis store tests passed"
}

# ---- Target: log ----
function Run-Log {
    if (-not (Test-Path $LogDir)) { New-Item -ItemType Directory -Path $LogDir | Out-Null }

    # Batch 1: Unit
    $ts1 = Get-Timestamp
    Write-Info "Batch 1 [$ts1] Unit tests started at $(Get-HumanTs)"
    $unitPkgs = @("api","broker","client","config","errs","pkg","plugin","protocol","store")
    foreach ($pkg in $unitPkgs) {
        $header = "# Timestamp: $ts1`n# Type: Unit - $pkg`n"
        $output = go test -v -count=1 "./$pkg/..." 2>&1 | Out-String
        "$header$output" | Out-File -Encoding utf8 (Join-Path $LogDir "${ts1}_unit_${pkg}.log")
    }
    Write-Pass "Batch 1 complete"

    # Batch 2: Integration
    $ts2 = Get-Timestamp
    Write-Info "Batch 2 [$ts2] Integration tests started at $(Get-HumanTs)"
    $iHeader = "# Timestamp: $ts2`n# Type: Integration`n"
    $iOutput = go test -v -count=1 -timeout 180s ./tests/integration/... 2>&1 | Out-String
    "$iHeader$iOutput" | Out-File -Encoding utf8 (Join-Path $LogDir "${ts2}_integration.log")
    Write-Pass "Batch 2 complete"

    # Batch 3: Benchmark
    $ts3 = Get-Timestamp
    Write-Info "Batch 3 [$ts3] Benchmark tests started at $(Get-HumanTs)"
    $bHeader = "# Timestamp: $ts3`n# Type: Benchmark`n"
    $bOutput = go test -v -bench=. -benchmem -count=1 -timeout 600s ./tests/bench/... 2>&1 | Out-String
    "$bHeader$bOutput" | Out-File -Encoding utf8 (Join-Path $LogDir "${ts3}_benchmark.log")
    Write-Pass "Batch 3 complete"

    # Summary
    $ts4 = Get-Timestamp
    Generate-Summary $ts1 $ts2 $ts3 $ts4
    Write-Pass "Summary: $(Join-Path $LogDir "${ts4}_summary.log")"
    Write-Info "All logs saved to $LogDir\"
}

function Generate-Summary {
    param($ts1, $ts2, $ts3, $ts4)

    $uPass = 0; $uSkip = 0
    foreach ($f in Get-ChildItem (Join-Path $LogDir "${ts1}_unit_*.log") -ErrorAction SilentlyContinue) {
        $uPass += Count-InFile $f.FullName "--- PASS"
        $uSkip += Count-InFile $f.FullName "--- SKIP"
    }

    $iPass = Count-InFile (Join-Path $LogDir "${ts2}_integration.log") "--- PASS"
    $iFail = Count-InFile (Join-Path $LogDir "${ts2}_integration.log") "--- FAIL"
    $bCnt  = Count-InFile (Join-Path $LogDir "${ts3}_benchmark.log") "ns/op"

    $summaryPath = Join-Path $LogDir "${ts4}_summary.log"
    $sb = [System.Text.StringBuilder]::new()
    [void]$sb.AppendLine("================================================================================")
    [void]$sb.AppendLine("  Shark-MQTT Test Summary")
    [void]$sb.AppendLine("  Generated: $(Get-HumanTs)")
    [void]$sb.AppendLine("================================================================================")
    [void]$sb.AppendLine("")
    [void]$sb.AppendLine("  Log Files:")
    [void]$sb.AppendLine("  ---------------------------------------------------")
    foreach ($f in Get-ChildItem (Join-Path $LogDir "${ts1}_unit_*.log") -ErrorAction SilentlyContinue) {
        [void]$sb.AppendLine("    $($f.Name)`t`t$('{0:N0} KB' -f ($f.Length/1KB))")
    }
    $iFile = Get-Item (Join-Path $LogDir "${ts2}_integration.log") -ErrorAction SilentlyContinue
    if ($iFile) { [void]$sb.AppendLine("    $($iFile.Name)`t`t$('{0:N0} KB' -f ($iFile.Length/1KB))") }
    $bFile = Get-Item (Join-Path $LogDir "${ts3}_benchmark.log") -ErrorAction SilentlyContinue
    if ($bFile) { [void]$sb.AppendLine("    $($bFile.Name)`t`t$('{0:N0} KB' -f ($bFile.Length/1KB))") }
    [void]$sb.AppendLine("")
    [void]$sb.AppendLine("  Results:")
    [void]$sb.AppendLine("  ---------------------------------------------------")
    [void]$sb.AppendLine("    Type             Pass   Fail   Skip")
    [void]$sb.AppendLine("    Unit           $uPass      0   $uSkip")
    [void]$sb.AppendLine("    Integration    $iPass      $iFail      0")
    [void]$sb.AppendLine("    Benchmark      $bCnt      -      -")
    [void]$sb.AppendLine("  ---------------------------------------------------")
    $total = $uPass + $iPass + $bCnt
    [void]$sb.AppendLine("    Total          $total      $iFail      $uSkip")
    [void]$sb.AppendLine("")
    [void]$sb.AppendLine("  Batches:")
    [void]$sb.AppendLine("    Batch 1 (Unit):        $ts1")
    [void]$sb.AppendLine("    Batch 2 (Integration): $ts2")
    [void]$sb.AppendLine("    Batch 3 (Benchmark):   $ts3")
    [void]$sb.AppendLine("================================================================================")

    $sb.ToString() | Out-File -Encoding utf8 $summaryPath
    Write-Host $sb.ToString()
}

# ---- Target: all ----
function Run-All {
    Write-Info "Running all tests..."
    Write-Host ""
    Run-Unit | Out-Null
    Write-Host ""
    Run-Integration | Out-Null
    Write-Host ""
    Run-QuickBench | Out-Null
    Write-Host ""
    Write-Pass "All tests completed."
}

# ---- Main ----
$target = if ($args.Count -gt 0) { $args[0] } else { "all" }

switch ($target) {
    "unit"         { Run-Unit }
    "integration"  { Run-Integration }
    "bench"        { Run-Bench }
    "quick"        { Run-QuickBench }
    "race"         { Run-Race }
    "coverage"     { Run-Coverage }
    "redis"        { Run-Redis }
    "log"          { Run-Log }
    "all"          { Run-All }
    { $_ -in "help","--help","-h" } {
        Write-Host "Shark-MQTT Test Runner (PowerShell)"
        Write-Host ""
        Write-Host "Usage: .\scripts\test.ps1 <target>"
        Write-Host ""
        Write-Host "Targets:"
        Write-Host "  all           Run unit + integration + quick benchmark (default)"
        Write-Host "  unit          Unit tests only"
        Write-Host "  integration   Integration tests only"
        Write-Host "  bench         Benchmark tests (set `$env:BENCHTIME, default 1s)"
        Write-Host "  quick         Quick benchmark (500ms)"
        Write-Host "  race          Unit tests with race detector"
        Write-Host "  coverage      Generate coverage report"
        Write-Host "  redis         Redis store tests"
        Write-Host "  log           Run all with timestamped logs + summary"
        Write-Host "  help          Show this help"
    }
    default {
        Write-Fail "Unknown target: $target  (run '.\scripts\test.ps1 help' for usage)"
        exit 1
    }
}

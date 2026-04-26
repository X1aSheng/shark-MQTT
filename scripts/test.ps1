# ============================================================================
#  Shark-MQTT Test Runner (PowerShell)
#  Platform: Windows (PowerShell 5.1+ / PowerShell 7)
#
#  All test runs automatically save timestamped logs to logs/ directory.
#  Log format: logs/{YYYYMMDD_HHmmss}_{type}.log
#
#  Usage:
#    .\scripts\test.ps1              # run all (unit + integration + benchmark)
#    .\scripts\test.ps1 unit         # unit tests only
#    .\scripts\test.ps1 integration  # integration tests only
#    .\scripts\test.ps1 bench        # benchmark (set $env:BENCHTIME, default 1s)
#    .\scripts\test.ps1 quick        # quick benchmark (500ms)
#    .\scripts\test.ps1 race         # unit tests with race detector
#    .\scripts\test.ps1 coverage     # generate coverage report
#    .\scripts\test.ps1 redis        # Redis store tests
#    .\scripts\test.ps1 ci           # full CI pipeline (vet + race + build)
#    .\scripts\test.ps1 help         # show this help
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

function Get-Timestamp { Get-Date -Format "yyyyMMdd_HHmmss_fff" }
function Get-HumanTs   { Get-Date -Format "yyyy-MM-dd HH:mm:ss.fff" }

function Ensure-LogDir {
    if (-not (Test-Path $LogDir)) { New-Item -ItemType Directory -Path $LogDir | Out-Null }
}

function Write-LogHeader {
    param([string]$Path, [string]$Type, [string]$Ts)
    $header = @"
# ====================================================================
#  Shark-MQTT $Type
#  Timestamp: $Ts
#  Started:   $(Get-HumanTs)
# ====================================================================

"@
    [System.IO.File]::WriteAllText($Path, $header, [System.Text.Encoding]::UTF8)
}

function Count-InFile($Path, $Pattern) {
    if (Test-Path $Path) {
        return (Select-String -Path $Path -Pattern $Pattern -SimpleMatch).Count
    }
    return 0
}

# ---- Target: unit ----
function Run-Unit {
    Ensure-LogDir
    $ts = Get-Timestamp
    $logFile = Join-Path $LogDir "${ts}_unit.log"
    Write-Info "[$ts] Running unit tests..."
    Write-Info "  Log: $logFile"
    Write-LogHeader $logFile "Unit Tests" $ts
    $output = go test -v -count=1 ./... 2>&1 | ForEach-Object { "$_" } | Out-String
    $output | Out-File -Append -Encoding utf8 $logFile
    Write-Host $output
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "Unit tests failed"
        return $false
    }
    Write-Pass "Unit tests passed"
    return $true
}

# ---- Target: integration ----
function Run-Integration {
    Ensure-LogDir
    $ts = Get-Timestamp
    $logFile = Join-Path $LogDir "${ts}_integration.log"
    Write-Info "[$ts] Running integration tests..."
    Write-Info "  Log: $logFile"
    Write-LogHeader $logFile "Integration Tests" $ts
    $output = go test -v -count=1 -timeout 180s ./tests/integration/... 2>&1 | ForEach-Object { "$_" } | Out-String
    $output | Out-File -Append -Encoding utf8 $logFile
    Write-Host $output
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "Integration tests failed"
        return $false
    }
    Write-Pass "Integration tests passed"
    return $true
}

# ---- Target: bench ----
function Run-Bench {
    param([string]$BenchTime = "1s")
    Ensure-LogDir
    $ts = Get-Timestamp
    $logFile = Join-Path $LogDir "${ts}_benchmark.log"
    Write-Info "[$ts] Running benchmarks (benchtime=$BenchTime)..."
    Write-Info "  Log: $logFile"
    Write-LogHeader $logFile "Benchmark Tests" $ts
    $output = go test -v -bench=. -benchmem -benchtime=$BenchTime -count=1 `
        -timeout 600s ./tests/bench/... 2>&1 | ForEach-Object { "$_" } | Out-String
    $output | Out-File -Append -Encoding utf8 $logFile
    $output -split "`n" | Where-Object { $_ -match "(Benchmark.*ns/op|^ok|PASS)" } | ForEach-Object {
        Write-Host "  $_"
    }
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "Benchmarks failed"
        return $false
    }
    $cnt = ([regex]::Matches($output, "ns/op")).Count
    Write-Pass "Benchmarks passed ($cnt tests)"
    return $true
}

# ---- Target: quick ----
function Run-Quick {
    Run-Bench -BenchTime "500ms"
}

# ---- Target: race ----
function Run-Race {
    Ensure-LogDir
    $ts = Get-Timestamp
    $logFile = Join-Path $LogDir "${ts}_race.log"
    Write-Info "[$ts] Running race detector..."
    Write-Info "  Log: $logFile"
    Write-LogHeader $logFile "Race Detector" $ts
    $output = go test -v -race -count=1 ./... 2>&1 | ForEach-Object { "$_" } | Out-String
    $output | Out-File -Append -Encoding utf8 $logFile
    Write-Host $output
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "Race detector found issues"
        return $false
    }
    Write-Pass "Race detector: clean"
    return $true
}

# ---- Target: coverage ----
function Run-Coverage {
    Ensure-LogDir
    $ts = Get-Timestamp
    $logFile = Join-Path $LogDir "${ts}_coverage.log"
    Write-Info "[$ts] Generating coverage report..."
    Write-Info "  Log: $logFile"
    Write-LogHeader $logFile "Coverage Report" $ts

    $output = go test -coverprofile=coverage.out -covermode=atomic -count=1 ./... 2>&1 | Out-String
    $output | Out-File -Append -Encoding utf8 $logFile
    Write-Host $output

    $funcOutput = go tool cover -func=coverage.out 2>&1 | Out-String
    $funcOutput | Out-File -Append -Encoding utf8 $logFile
    Write-Host $funcOutput

    go tool cover -html=coverage.out -o coverage.html 2>$null
    Write-Pass "Coverage report: coverage.html + $logFile"
    return $true
}

# ---- Target: redis ----
function Run-Redis {
    Ensure-LogDir
    if (-not $env:MQTT_REDIS_ADDR) {
        Write-Warn "MQTT_REDIS_ADDR not set, using localhost:6379"
        $env:MQTT_REDIS_ADDR = "localhost:6379"
    }
    $ts = Get-Timestamp
    $logFile = Join-Path $LogDir "${ts}_redis.log"
    Write-Info "[$ts] Running Redis tests (MQTT_REDIS_ADDR=$env:MQTT_REDIS_ADDR)..."
    Write-Info "  Log: $logFile"
    Write-LogHeader $logFile "Redis Store Tests" $ts
    $output = go test -v -count=1 ./store/redis/... 2>&1 | ForEach-Object { "$_" } | Out-String
    $output | Out-File -Append -Encoding utf8 $logFile
    Write-Host $output
    if ($LASTEXITCODE -ne 0) {
        Write-Fail "Redis tests failed"
        return $false
    }
    Write-Pass "Redis tests passed"
    return $true
}

# ---- Target: ci ----
function Run-CI {
    Ensure-LogDir
    $ts = Get-Timestamp
    $logFile = Join-Path $LogDir "${ts}_ci.log"
    Write-Info "[$ts] Running CI pipeline..."
    Write-Info "  Log: $logFile"
    Write-LogHeader $logFile "CI Pipeline" $ts

    $failed = $false

    Write-Host "--- go vet ---"
    $vetOut = go vet ./... 2>&1 | Out-String
    $vetOut | Out-File -Append -Encoding utf8 $logFile
    Write-Host $vetOut
    if ($LASTEXITCODE -ne 0) { $failed = $true }

    Write-Host "--- go test -race ---"
    $raceOut = go test -v -race -count=1 ./... 2>&1 | Out-String
    $raceOut | Out-File -Append -Encoding utf8 $logFile
    Write-Host $raceOut
    if ($LASTEXITCODE -ne 0) { $failed = $true }

    Write-Host "--- go build ---"
    $buildOut = go build -v ./... 2>&1 | Out-String
    $buildOut | Out-File -Append -Encoding utf8 $logFile
    Write-Host $buildOut
    if ($LASTEXITCODE -ne 0) { $failed = $true }

    if ($failed) {
        Write-Fail "CI pipeline failed"
        return $false
    }
    Write-Pass "CI pipeline passed"
    return $true
}

# ---- Target: all ----
function Run-All {
    Ensure-LogDir

    $allFailed = $false
    $uPass = 0; $uSkip = 0; $uFail = 0
    $iPass = 0; $iFail = 0; $bCnt = 0

    # --- Batch 1: Unit ---
    $ts1 = Get-Timestamp
    Write-Info "Batch 1 [$ts1] Unit tests started at $(Get-HumanTs)"
    $unitPkgs = @("api","broker","client","config","errs","pkg","plugin","protocol","store")
    foreach ($pkg in $unitPkgs) {
        $pkgLog = Join-Path $LogDir "${ts1}_unit_${pkg}.log"
        Write-LogHeader $pkgLog "Unit - $pkg" $ts1
        $output = go test -v -count=1 "./$pkg/..." 2>&1 | ForEach-Object { "$_" } | Out-String
        $output | Out-File -Append -Encoding utf8 $pkgLog

        $p = ([regex]::Matches($output, "--- PASS")).Count
        $s = ([regex]::Matches($output, "--- SKIP")).Count
        $f = ([regex]::Matches($output, "--- FAIL")).Count
        $uPass += $p; $uSkip += $s; $uFail += $f

        if ($f -gt 0) {
            Write-Fail "  ./$pkg  PASS=$p  FAIL=$f"
            $allFailed = $true
        } else {
            Write-Pass "  ./$pkg  PASS=$p  SKIP=$s"
        }
    }
    Write-Pass "Batch 1 complete ($uPass pass, $uSkip skip, $uFail fail)"

    # --- Batch 2: Integration ---
    $ts2 = Get-Timestamp
    Write-Info "Batch 2 [$ts2] Integration tests started at $(Get-HumanTs)"
    $intLog = Join-Path $LogDir "${ts2}_integration.log"
    Write-LogHeader $intLog "Integration Tests" $ts2
    $intOutput = go test -v -count=1 -timeout 180s ./tests/integration/... 2>&1 | ForEach-Object { "$_" } | Out-String
    $intOutput | Out-File -Append -Encoding utf8 $intLog
    $iPass = ([regex]::Matches($intOutput, "--- PASS")).Count
    $iFail = ([regex]::Matches($intOutput, "--- FAIL")).Count
    if ($iFail -gt 0) {
        Write-Fail "Batch 2 failed ($iFail failed)"
        $allFailed = $true
    } else {
        Write-Pass "Batch 2 complete ($iPass passed)"
    }

    # --- Batch 3: Benchmark ---
    $ts3 = Get-Timestamp
    Write-Info "Batch 3 [$ts3] Benchmark tests started at $(Get-HumanTs)"
    $benchLog = Join-Path $LogDir "${ts3}_benchmark.log"
    Write-LogHeader $benchLog "Benchmark Tests" $ts3
    $benchOutput = go test -v -bench=. -benchmem -count=1 -timeout 600s ./tests/bench/... 2>&1 | ForEach-Object { "$_" } | Out-String
    $benchOutput | Out-File -Append -Encoding utf8 $benchLog
    $benchOutput -split "`n" | Where-Object { $_ -match "(Benchmark.*ns/op|^ok|PASS)" } | ForEach-Object {
        Write-Host "  $_"
    }
    $bCnt = ([regex]::Matches($benchOutput, "ns/op")).Count
    Write-Pass "Batch 3 complete ($bCnt benchmarks)"

    # --- Summary ---
    $ts4 = Get-Timestamp
    Generate-Summary $ts1 $ts2 $ts3 $ts4 $uPass $uSkip $uFail $iPass $iFail $bCnt

    if ($allFailed) {
        Write-Fail "Some tests failed - see summary log"
        return $false
    }
    Write-Pass "All tests completed."
    return $true
}

# ---- Generate summary ----
function Generate-Summary {
    param($ts1, $ts2, $ts3, $ts4,
          $uPass, $uSkip, $uFail, $iPass, $iFail, $bCnt)

    $summaryPath = Join-Path $LogDir "${ts4}_summary.log"
    $sb = [System.Text.StringBuilder]::new()

    [void]$sb.AppendLine("======================================================================")
    [void]$sb.AppendLine("  Shark-MQTT Test Summary")
    [void]$sb.AppendLine("  Generated: $(Get-HumanTs)")
    [void]$sb.AppendLine("======================================================================")
    [void]$sb.AppendLine("")
    [void]$sb.AppendLine("  Log Files:")
    [void]$sb.AppendLine("  ---------------------------------------------------")
    foreach ($f in Get-ChildItem (Join-Path $LogDir "${ts1}_unit_*.log") -ErrorAction SilentlyContinue) {
        [void]$sb.AppendLine("    $($f.Name)`t$('{0:N0} KB' -f ($f.Length/1KB))")
    }
    $iFile = Get-Item (Join-Path $LogDir "${ts2}_integration.log") -ErrorAction SilentlyContinue
    if ($iFile) { [void]$sb.AppendLine("    $($iFile.Name)`t$('{0:N0} KB' -f ($iFile.Length/1KB))") }
    $bFile = Get-Item (Join-Path $LogDir "${ts3}_benchmark.log") -ErrorAction SilentlyContinue
    if ($bFile) { [void]$sb.AppendLine("    $($bFile.Name)`t$('{0:N0} KB' -f ($bFile.Length/1KB))") }
    [void]$sb.AppendLine("")
    [void]$sb.AppendLine("  Results:")
    [void]$sb.AppendLine("  ---------------------------------------------------")
    [void]$sb.AppendLine("    Type             Pass   Fail   Skip")
    [void]$sb.AppendLine("    Unit           $uPass      $uFail      $uSkip")
    [void]$sb.AppendLine("    Integration    $iPass      $iFail      0")
    [void]$sb.AppendLine("    Benchmark      $bCnt      -      -")
    [void]$sb.AppendLine("  ---------------------------------------------------")
    $total = $uPass + $iPass + $bCnt
    $totalFail = $uFail + $iFail
    [void]$sb.AppendLine("    Total          $total      $totalFail      $uSkip")
    [void]$sb.AppendLine("")
    [void]$sb.AppendLine("  Batches:")
    [void]$sb.AppendLine("    Batch 1 (Unit):        $ts1")
    [void]$sb.AppendLine("    Batch 2 (Integration): $ts2")
    [void]$sb.AppendLine("    Batch 3 (Benchmark):   $ts3")
    [void]$sb.AppendLine("======================================================================")

    $sb.ToString() | Out-File -Encoding utf8 $summaryPath
    Write-Host $sb.ToString()
}

# ---- Main ----
$target = if ($args.Count -gt 0) { $args[0] } else { "all" }

switch ($target) {
    "unit"         { Run-Unit }
    "integration"  { Run-Integration }
    "bench"        { Run-Bench }
    "quick"        { Run-Quick }
    "race"         { Run-Race }
    "coverage"     { Run-Coverage }
    "redis"        { Run-Redis }
    "ci"           { Run-CI }
    "all"          { Run-All }
    { $_ -in "help","--help","-h" } {
        Write-Host "Shark-MQTT Test Runner (PowerShell)"
        Write-Host ""
        Write-Host "Usage: .\scripts\test.ps1 <target>"
        Write-Host ""
        Write-Host "Targets:"
        Write-Host "  all           Run unit + integration + benchmark (default)"
        Write-Host "  unit          Unit tests"
        Write-Host "  integration   Integration tests"
        Write-Host "  bench         Benchmark (set `$env:BENCHTIME, default 1s)"
        Write-Host "  quick         Quick benchmark (500ms)"
        Write-Host "  race          Unit tests with race detector"
        Write-Host "  coverage      Coverage report"
        Write-Host "  redis         Redis store tests"
        Write-Host "  ci            Full CI pipeline (vet + race + build)"
        Write-Host "  help          Show this help"
        Write-Host ""
        Write-Host "All targets save timestamped logs to logs/ directory."
    }
    default {
        Write-Fail "Unknown target: $target  (run '.\scripts\test.ps1 help' for usage)"
        exit 1
    }
}

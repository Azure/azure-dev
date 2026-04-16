#!/usr/bin/env pwsh

<#
.SYNOPSIS
    Analyzes Go test execution time and enforces per-package and total time budgets.

.DESCRIPTION
    Runs 'go test -json' (or parses an existing JSON output file) and produces
    a per-package timing report sorted by slowest. Optionally fails if any
    single package exceeds a per-package budget or the total exceeds a budget.

    Designed to be called from CI to prevent test duration from silently growing.

.PARAMETER PackagePath
    Go package pattern to test. Defaults to './...'.

.PARAMETER JsonFile
    Path to an existing 'go test -json' output file. If provided, skips
    running tests and parses the file instead.

.PARAMETER MaxPackageSeconds
    Maximum allowed seconds for any single package. The script exits with
    a non-zero code when any package exceeds this value. Set to 0 to disable.

.PARAMETER MaxTotalSeconds
    Maximum allowed seconds for the total test run. The script exits with
    a non-zero code when the total exceeds this value. Set to 0 to disable.

.PARAMETER Short
    If set, passes -short to go test (unit tests only).

.PARAMETER Top
    Number of slowest packages to display. Defaults to 20.

.EXAMPLE
    # Run tests and report timing (no enforcement)
    ./Test-TestTiming.ps1 -Short

.EXAMPLE
    # Enforce: no package > 120s, total < 600s
    ./Test-TestTiming.ps1 -Short -MaxPackageSeconds 120 -MaxTotalSeconds 600

.EXAMPLE
    # Parse existing JSON output
    ./Test-TestTiming.ps1 -JsonFile test-output.json -MaxPackageSeconds 120
#>

param(
    [string]$PackagePath = './...',
    [string]$JsonFile = '',
    [double]$MaxPackageSeconds = 0,
    [double]$MaxTotalSeconds = 0,
    [switch]$Short,
    [int]$Top = 20
)

$ErrorActionPreference = 'Stop'

# Run tests or read existing output
if ($JsonFile -ne '') {
    if (-not (Test-Path $JsonFile)) {
        throw "JSON file '$JsonFile' not found"
    }
    Write-Host "Parsing existing test output from $JsonFile..."
    $jsonLines = Get-Content $JsonFile
} else {
    Write-Host "Running tests with timing collection..."
    $testArgs = @('test', '-json', '-count=1', '-timeout', '600s')
    if ($Short) {
        $testArgs += '-short'
    }
    $testArgs += $PackagePath

    $tempFile = [System.IO.Path]::GetTempFileName()
    try {
        & go @testArgs 2>&1 | Tee-Object -FilePath $tempFile
        $testExitCode = $LASTEXITCODE
        if (-not (Test-Path $tempFile) -or (Get-Item $tempFile).Length -eq 0) {
            throw "Failed to capture test output to temp file"
        }
        $jsonLines = Get-Content $tempFile
    } finally {
        Remove-Item $tempFile -Force -ErrorAction SilentlyContinue
    }

    if ($testExitCode -ne 0) {
        Write-Host "##vso[task.logissue type=warning]Tests exited with code $testExitCode. Timing report may be incomplete."
    }
}

# Parse JSON output — extract package-level pass/fail events with Elapsed
$packageTimes = @{}
$firstTimestamp = $null
$lastTimestamp = $null

foreach ($line in $jsonLines) {
    if ([string]::IsNullOrWhiteSpace($line)) { continue }

    try {
        $event = $line | ConvertFrom-Json
    } catch {
        continue
    }

    # Track wall clock from first/last event timestamps
    if ($event.Time) {
        $ts = [datetime]$event.Time
        if ($null -eq $firstTimestamp -or $ts -lt $firstTimestamp) {
            $firstTimestamp = $ts
        }
        if ($null -eq $lastTimestamp -or $ts -gt $lastTimestamp) {
            $lastTimestamp = $ts
        }
    }

    # Package-level events have Action but no Test field
    if ($event.Action -in @('pass', 'fail', 'skip') -and
        -not [string]::IsNullOrWhiteSpace($event.Package) -and
        [string]::IsNullOrWhiteSpace($event.Test) -and
        $null -ne $event.Elapsed) {

        $pkg = $event.Package -replace 'github\.com/azure/azure-dev/cli/azd/', ''
        $packageTimes[$pkg] = @{
            Elapsed = [double]$event.Elapsed
            Action  = $event.Action
        }
    }
}

if ($packageTimes.Count -eq 0) {
    Write-Host "##vso[task.logissue type=warning]No package timing data found in test output."
    exit 0
}

# Calculate wall clock time
$wallClockSeconds = 0
if ($null -ne $firstTimestamp -and $null -ne $lastTimestamp) {
    $wallClockSeconds = ($lastTimestamp - $firstTimestamp).TotalSeconds
}

# Sort by elapsed time descending
$sorted = $packageTimes.GetEnumerator() |
    Sort-Object { $_.Value.Elapsed } -Descending

$totalSeconds = ($sorted | ForEach-Object { $_.Value.Elapsed } | Measure-Object -Sum).Sum

# Display report
Write-Host ""
Write-Host "=========================================="
Write-Host "  Test Timing Report"
Write-Host "=========================================="
Write-Host ""
Write-Host ("{0,-60} {1,10} {2,8}" -f "Package", "Seconds", "Status")
Write-Host ("{0,-60} {1,10} {2,8}" -f "-------", "-------", "------")

$displayed = 0
$violations = @()

foreach ($entry in $sorted) {
    $pkg = $entry.Key
    $elapsed = $entry.Value.Elapsed
    $action = $entry.Value.Action
    $status = if ($action -eq 'pass') { 'ok' } elseif ($action -eq 'skip') { 'skip' } else { 'FAIL' }

    $budgetFlag = ""
    if ($MaxPackageSeconds -gt 0 -and $elapsed -gt $MaxPackageSeconds) {
        $budgetFlag = " ⚠ OVER BUDGET"
        $violations += $pkg
    }

    if ($displayed -lt $Top) {
        Write-Host ("{0,-60} {1,10:N1} {2,8}{3}" -f $pkg, $elapsed, $status, $budgetFlag)
        $displayed++
    }
}

$remaining = $packageTimes.Count - $Top
if ($remaining -gt 0) {
    Write-Host "  ... and $remaining more packages"
}

Write-Host ""
Write-Host "Total packages: $($packageTimes.Count)"
if ($wallClockSeconds -gt 0) {
    Write-Host ("Wall clock:      {0:N1}s ({1:N1}m)" -f $wallClockSeconds, ($wallClockSeconds / 60))
    Write-Host ("Cumulative:      {0:N1}s ({1:N1}m) — sum of all package times" -f $totalSeconds, ($totalSeconds / 60))
    $parallelism = if ($wallClockSeconds -gt 0) { $totalSeconds / $wallClockSeconds } else { 0 }
    Write-Host ("Parallelism:     {0:N1}x" -f $parallelism)
    $slowest = ($sorted | Select-Object -First 1)
    Write-Host ("Bottleneck:      {0} ({1:N1}s)" -f $slowest.Key, $slowest.Value.Elapsed)
} else {
    Write-Host ("Total time: {0:N1}s ({1:N1}m)" -f $totalSeconds, ($totalSeconds / 60))
}

if ($MaxPackageSeconds -gt 0) {
    Write-Host "Per-package budget: ${MaxPackageSeconds}s"
}
if ($MaxTotalSeconds -gt 0) {
    Write-Host "Total budget: ${MaxTotalSeconds}s"
}

# Enforce budgets
$failed = $false

if ($MaxPackageSeconds -gt 0 -and $violations.Count -gt 0) {
    $msg = "$($violations.Count) package(s) exceeded the ${MaxPackageSeconds}s budget: $($violations -join ', ')"
    Write-Host ""
    Write-Host "##vso[task.logissue type=error]$msg"
    $failed = $true
}

if ($MaxTotalSeconds -gt 0 -and $totalSeconds -gt $MaxTotalSeconds) {
    $msg = "Total test time {0:N1}s exceeds budget of {1}s" -f $totalSeconds, $MaxTotalSeconds
    Write-Host "##vso[task.logissue type=error]$msg"
    $failed = $true
}

if ($failed) {
    exit 1
}

Write-Host ""
Write-Host "All timing budgets met. ✓"

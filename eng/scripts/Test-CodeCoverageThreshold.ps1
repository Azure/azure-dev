#!/usr/bin/env pwsh

<#
.SYNOPSIS
    Checks that code coverage meets a minimum threshold.

.DESCRIPTION
    Parses a Go coverage profile using 'go tool cover -func' to extract the
    total statement coverage percentage, then fails if it is below the
    specified minimum.

    This script is called automatically by Get-LocalCoverageReport.ps1
    (when -MinCoverage is set) and by the CI pipeline (release-cli.yml) to
    enforce the coverage gate. It can also be run standalone on any Go
    coverage profile.

    See cli/azd/docs/code-coverage-guide.md for details on the CI gate
    and ratchet policy.

.PARAMETER CoverageFile
    Path to the Go coverage profile (typically cover.out).

.PARAMETER MinimumCoveragePercent
    The minimum acceptable coverage percentage (0-100). The script exits
    with a non-zero code when coverage is below this value.

.EXAMPLE
    ./Test-CodeCoverageThreshold.ps1 -CoverageFile cover.out -MinimumCoveragePercent 48
#>

param(
    [Parameter(Mandatory = $true)]
    [string]$CoverageFile,

    [Parameter(Mandatory = $true)]
    [ValidateRange(0, 100)]
    [double]$MinimumCoveragePercent
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path $CoverageFile)) {
    throw "Coverage file '$CoverageFile' not found"
}

Write-Host "Checking code coverage threshold (minimum: $MinimumCoveragePercent%)..."

# Use 'go tool cover -func' to get per-function coverage and the total line.
$output = & go tool cover "-func=$CoverageFile"
if ($LASTEXITCODE) {
    throw "go tool cover -func failed: $output"
}

# Find the "total:" summary line, which looks like: "total:  (statements)  48.3%"
$totalLine = $output | Where-Object { $_ -match '^\s*total:' } | Select-Object -Last 1

if (-not $totalLine) {
    throw "Could not find 'total:' line in 'go tool cover -func' output"
}

# Match both integer (100%) and decimal (48.3%) percentages using invariant culture
if ($totalLine -match '(\d+(?:\.\d+)?)%') {
    $coveragePercent = [double]::Parse($matches[1], [System.Globalization.CultureInfo]::InvariantCulture)
} else {
    throw "Could not parse coverage percentage from: $totalLine"
}

Write-Host "Total statement coverage: $coveragePercent%"

if ($coveragePercent -lt $MinimumCoveragePercent) {
    Write-Host "##vso[task.logissue type=error]Code coverage $coveragePercent% is below the minimum threshold of $MinimumCoveragePercent%."
    exit 1
}

Write-Host "Coverage $coveragePercent% meets the minimum threshold of $MinimumCoveragePercent%. ✓"

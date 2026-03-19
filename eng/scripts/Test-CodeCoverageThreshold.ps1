#!/usr/bin/env pwsh

<#
.SYNOPSIS
    Checks that code coverage meets a minimum threshold.

.DESCRIPTION
    Parses a Go coverage profile using 'go tool cover -func' to extract the
    total statement coverage percentage, then fails if it is below the
    specified minimum.

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
    [double]$MinimumCoveragePercent
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path $CoverageFile)) {
    throw "Coverage file '$CoverageFile' not found"
}

Write-Host "Checking code coverage threshold (minimum: $MinimumCoveragePercent%)..."

# Use 'go tool cover -func' to get per-function coverage and the total line.
$output = & go tool cover "-func=$CoverageFile" 2>&1
if ($LASTEXITCODE -ne 0) {
    throw "go tool cover -func failed: $output"
}

# The last line looks like: "total:    (statements)   48.3%"
$lastLine = ($output | Select-Object -Last 1).ToString()

if ($lastLine -match '(\d+\.\d+)%') {
    $coveragePercent = [double]$matches[1]
} else {
    throw "Could not parse total coverage from 'go tool cover -func' output: $lastLine"
}

Write-Host "Total statement coverage: $coveragePercent%"

if ($coveragePercent -lt $MinimumCoveragePercent) {
    Write-Host "##vso[task.logissue type=error]Code coverage $coveragePercent% is below the minimum threshold of $MinimumCoveragePercent%."
    Write-Host "##vso[task.complete result=Failed;]Coverage threshold not met."
    exit 1
}

Write-Host "Coverage $coveragePercent% meets the minimum threshold of $MinimumCoveragePercent%. ✓"

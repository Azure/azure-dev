#!/usr/bin/env pwsh

# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

<#
.SYNOPSIS
    Generates a markdown coverage diff between two Go coverage profiles.

.DESCRIPTION
    Compares a baseline coverage profile (typically from main) against a
    current branch profile and produces a markdown table showing per-package
    coverage changes, overall delta, and impact summary.

    The output includes a <!-- coverage-diff --> HTML comment tag so that
    Update-PRComment.ps1 can replace previous diff comments on re-runs.

    Coverage is computed by parsing Go text coverage profiles directly:
    each line represents a code block with statement count and hit count.
    Statements are aggregated per package (directory) and coverage is
    calculated as covered_statements / total_statements * 100.

    See cli/azd/docs/code-coverage-guide.md for context on coverage modes.

.PARAMETER BaselineFile
    Path to the baseline Go coverage profile (e.g. main's cover.out).

.PARAMETER CurrentFile
    Path to the current branch's coverage profile.

.PARAMETER OutputFile
    Write markdown to this file. If omitted, writes to stdout.

.PARAMETER TopN
    Maximum number of changed packages to show in the diff table.
    Packages are sorted by absolute delta descending. Default: 20.

.PARAMETER MinDelta
    Minimum absolute percentage-point change to include a package in
    the diff table. Default: 0.1.

.PARAMETER ModulePrefix
    Go module import path prefix to strip from package names for
    readability. Auto-detected from go.mod when possible.

.EXAMPLE
    # Compare two profiles and print to terminal
    ./Get-CoverageDiff.ps1 -BaselineFile cover-main.out -CurrentFile cover-local.out

.EXAMPLE
    # Write diff to a file for PR comment
    ./Get-CoverageDiff.ps1 -BaselineFile cover-main.out -CurrentFile cover-local.out -OutputFile diff.md

.EXAMPLE
    # Show top 10 changes with at least 1pp delta
    ./Get-CoverageDiff.ps1 -BaselineFile cover-main.out -CurrentFile cover-local.out -TopN 10 -MinDelta 1.0
#>

param(
    [Parameter(Mandatory = $true)]
    [string]$BaselineFile,

    [Parameter(Mandatory = $true)]
    [string]$CurrentFile,

    [string]$OutputFile,

    [int]$TopN = 20,

    [double]$MinDelta = 0.1,

    [string]$ModulePrefix = ''
)

$ErrorActionPreference = 'Stop'

# ---------------------------------------------------------------------------
# Validate inputs
# ---------------------------------------------------------------------------
if (-not (Test-Path $BaselineFile)) {
    throw "Baseline coverage file not found: $BaselineFile"
}
if (-not (Test-Path $CurrentFile)) {
    throw "Current coverage file not found: $CurrentFile"
}

# ---------------------------------------------------------------------------
# Auto-detect module prefix from go.mod if not provided
# ---------------------------------------------------------------------------
if (-not $ModulePrefix) {
    $goModCandidates = @(
        (Join-Path (Get-Location) 'go.mod'),
        (Join-Path $PSScriptRoot '../../cli/azd/go.mod')
    )
    foreach ($candidate in $goModCandidates) {
        if (Test-Path $candidate) {
            $modLine = Get-Content $candidate |
                Where-Object { $_ -match '^module\s+' } |
                Select-Object -First 1
            if ($modLine -match '^module\s+(.+)$') {
                $ModulePrefix = $Matches[1].Trim() + '/'
                break
            }
        }
    }
    if (-not $ModulePrefix) {
        $ModulePrefix = 'github.com/azure/azure-dev/cli/azd/'
    }
}

# ---------------------------------------------------------------------------
# Parse a Go text coverage profile into per-package stats.
#
# Each non-header line has the format:
#   file:startLine.startCol,endLine.endCol numStatements hitCount
#
# Returns a hashtable with:
#   Packages        — hashtable: packageName -> @{ Statements; Covered }
#   TotalStatements — int: sum of all statement counts
#   TotalCovered    — int: sum of statements in blocks with hitCount > 0
# ---------------------------------------------------------------------------
function Read-CoverageProfile {
    param([string]$FilePath)

    $lines = Get-Content $FilePath
    if ($lines.Count -eq 0) {
        throw "Coverage file is empty: $FilePath"
    }

    if ($lines[0] -notmatch '^mode:\s') {
        throw "Coverage file does not start with a mode line: $($lines[0])"
    }

    $packages = @{}
    $totalStatements = 0
    $totalCovered = 0

    for ($i = 1; $i -lt $lines.Count; $i++) {
        $line = $lines[$i]
        if ([string]::IsNullOrWhiteSpace($line)) { continue }

        # Match: filepath:startLine.startCol,endLine.endCol numStatements hitCount
        if ($line -notmatch '^(.+?):(\d+\.\d+),(\d+\.\d+)\s+(\d+)\s+(\d+)$') {
            continue
        }

        $filePath = $Matches[1]
        $stmts = [int]$Matches[4]
        $hits = [int]$Matches[5]

        # Derive package name by stripping module prefix and filename
        $pkg = ''
        if ($filePath.StartsWith($ModulePrefix)) {
            $relPath = $filePath.Substring($ModulePrefix.Length)
            $lastSlash = $relPath.LastIndexOf('/')
            $pkg = if ($lastSlash -ge 0) { $relPath.Substring(0, $lastSlash) } else { '.' }
        } else {
            $lastSlash = $filePath.LastIndexOf('/')
            $pkg = if ($lastSlash -ge 0) { $filePath.Substring(0, $lastSlash) } else { $filePath }
        }

        if (-not $packages.ContainsKey($pkg)) {
            $packages[$pkg] = @{ Statements = 0; Covered = 0 }
        }

        $packages[$pkg].Statements += $stmts
        if ($hits -gt 0) {
            $packages[$pkg].Covered += $stmts
        }

        $totalStatements += $stmts
        if ($hits -gt 0) {
            $totalCovered += $stmts
        }
    }

    return @{
        Packages        = $packages
        TotalStatements = $totalStatements
        TotalCovered    = $totalCovered
    }
}

# ---------------------------------------------------------------------------
# Parse both profiles
# ---------------------------------------------------------------------------
Write-Host "Parsing baseline: $BaselineFile"
$baseline = Read-CoverageProfile -FilePath $BaselineFile

Write-Host "Parsing current:  $CurrentFile"
$current = Read-CoverageProfile -FilePath $CurrentFile

# ---------------------------------------------------------------------------
# Compute overall totals
# ---------------------------------------------------------------------------
$inv = [System.Globalization.CultureInfo]::InvariantCulture

$baseTotal = if ($baseline.TotalStatements -gt 0) {
    [math]::Round(($baseline.TotalCovered / $baseline.TotalStatements) * 100, 1)
} else { 0.0 }

$currTotal = if ($current.TotalStatements -gt 0) {
    [math]::Round(($current.TotalCovered / $current.TotalStatements) * 100, 1)
} else { 0.0 }

$overallDelta = [math]::Round($currTotal - $baseTotal, 1)

Write-Host "  Baseline: $baseTotal% ($($baseline.TotalCovered)/$($baseline.TotalStatements) stmts)"
Write-Host "  Current:  $currTotal% ($($current.TotalCovered)/$($current.TotalStatements) stmts)"
Write-Host "  Delta:    $overallDelta pp"

# ---------------------------------------------------------------------------
# Compute per-package deltas
# ---------------------------------------------------------------------------
$allPackages = @(@($baseline.Packages.Keys) + @($current.Packages.Keys)) |
    Sort-Object -Unique

$allDiffs = [System.Collections.Generic.List[PSCustomObject]]::new()

foreach ($pkg in $allPackages) {
    $bPkg = $baseline.Packages[$pkg]
    $cPkg = $current.Packages[$pkg]

    $bStmts = if ($bPkg) { $bPkg.Statements } else { 0 }
    $bCov   = if ($bPkg) { $bPkg.Covered } else { 0 }
    $cStmts = if ($cPkg) { $cPkg.Statements } else { 0 }
    $cCov   = if ($cPkg) { $cPkg.Covered } else { 0 }

    $bPct = if ($bStmts -gt 0) {
        [math]::Round(($bCov / $bStmts) * 100, 1)
    } else { 0.0 }

    $cPct = if ($cStmts -gt 0) {
        [math]::Round(($cCov / $cStmts) * 100, 1)
    } else { 0.0 }

    $delta = [math]::Round($cPct - $bPct, 1)

    if ([math]::Abs($delta) -ge $MinDelta -and $delta -ne 0) {
        $allDiffs.Add([PSCustomObject]@{
            Package         = $pkg
            BaselinePercent = $bPct
            CurrentPercent  = $cPct
            Delta           = $delta
            CurrentStmts    = $cStmts
        })
    }
}

# Impact summary uses all changed packages; table shows top N
$changedPackageCount = $allDiffs.Count
$changedStmts = ($allDiffs | Measure-Object -Property CurrentStmts -Sum).Sum
if ($null -eq $changedStmts) { $changedStmts = 0 }
$totalStmts = $current.TotalStatements
$changedPct = if ($totalStmts -gt 0) {
    [math]::Round(($changedStmts / $totalStmts) * 100, 1)
} else { 0.0 }

# Sort by absolute delta descending, take top N for display
$tableDiffs = @(
    $allDiffs |
        Sort-Object { [math]::Abs($_.Delta) } -Descending |
        Select-Object -First $TopN
)

# ---------------------------------------------------------------------------
# Get branch name for header
# ---------------------------------------------------------------------------
$branchName = 'current'
try {
    $gitBranch = git rev-parse --abbrev-ref HEAD 2>$null
    if ($gitBranch) { $branchName = $gitBranch.Trim() }
} catch {
    # Ignore -- use default
}

# ---------------------------------------------------------------------------
# Generate markdown
# ---------------------------------------------------------------------------
$sb = [System.Text.StringBuilder]::new()

[void]$sb.AppendLine('<!-- coverage-diff -->')
[void]$sb.AppendLine("## Coverage Diff: ``main`` <- ``$branchName``")
[void]$sb.AppendLine()

# Overall line
$deltaSign = if ($overallDelta -ge 0) { '+' } else { '' }
[void]$sb.AppendLine(
    "**Overall**: $($baseTotal.ToString('F1', $inv))% -> $($currTotal.ToString('F1', $inv))% " +
    "($deltaSign$($overallDelta.ToString('F1', $inv)) pp)"
)
[void]$sb.AppendLine()

# Changed packages table
if ($tableDiffs.Count -gt 0) {
    [void]$sb.AppendLine('### Changed Packages')
    [void]$sb.AppendLine()
    [void]$sb.AppendLine('| Package | Before | After | Delta |')
    [void]$sb.AppendLine('|---------|--------|-------|-------|')

    foreach ($d in $tableDiffs) {
        $sign = if ($d.Delta -ge 0) { '+' } else { '' }
        $bold = if ([math]::Abs($d.Delta) -ge 1.0) { '**' } else { '' }
        [void]$sb.AppendLine(
            "| ``$($d.Package)`` " +
            "| $($d.BaselinePercent.ToString('F1', $inv))% " +
            "| $($d.CurrentPercent.ToString('F1', $inv))% " +
            "| $bold$sign$($d.Delta.ToString('F1', $inv))$bold |"
        )
    }

    if ($changedPackageCount -gt $TopN) {
        [void]$sb.AppendLine()
        $remaining = $changedPackageCount - $TopN
        [void]$sb.AppendLine("*... and $remaining more packages with smaller changes.*")
    }

    [void]$sb.AppendLine()
} else {
    [void]$sb.AppendLine("No packages changed by >= $($MinDelta.ToString('F1', $inv)) percentage points.")
    [void]$sb.AppendLine()
}

# Impact summary
$changedStmtsFmt = $changedStmts.ToString('N0', $inv)
$totalStmtsFmt = $totalStmts.ToString('N0', $inv)

[void]$sb.AppendLine('### Impact Summary')
[void]$sb.AppendLine("- **Packages changed**: $changedPackageCount of $($allPackages.Count)")
[void]$sb.AppendLine("- **Statements in changed packages**: $changedStmtsFmt of $totalStmtsFmt ($($changedPct.ToString('F1', $inv))%)")
[void]$sb.AppendLine("- **Weighted impact**: $deltaSign$($overallDelta.ToString('F1', $inv)) pp overall")

$markdown = $sb.ToString()

# ---------------------------------------------------------------------------
# Output
# ---------------------------------------------------------------------------
if ($OutputFile) {
    Set-Content -Path $OutputFile -Value $markdown -Encoding UTF8
    Write-Host ""
    Write-Host "Coverage diff written to: $OutputFile"
} else {
    Write-Host ""
    Write-Output $markdown
}

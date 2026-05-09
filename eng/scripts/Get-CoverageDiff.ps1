#!/usr/bin/env pwsh

# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

<#
.SYNOPSIS
    Computes a Go coverage diff between two coverage profiles and emits a
    plain-text report intended for CI build logs.

.DESCRIPTION
    Compares a baseline Go coverage profile (typically from a recent main
    build) against the current branch's profile and writes a plain-text
    report. The report is designed to be read directly in pipeline logs;
    no Markdown or HTML is emitted and no PR comment is posted.

    Two gates run when -FailOnGate is set:
      1. Per-package decrease beyond -MaxPackageDecrease percentage
         points (scoped to PR-touched packages in changed-file mode,
         all packages otherwise).
      2. Absolute floor: overall coverage must stay at or above
         -MinOverallCoverage.
    Either breach exits the script with code 2 so the CI stage fails
    and merge is blocked. The gate intentionally does NOT enforce a
    per-file floor or an overall-decrease tolerance — small, isolated
    drops in untouched packages are ignored, and a PR that only
    rebalances coverage between packages is fine.

    Two reporting modes are supported:

      * Changed-file mode (recommended for CI)
        Provide -ChangedFiles or -ChangedFilesFromFile. The script reports
        per-package deltas only for packages that contain a file touched
        by the PR (Go files, *_test.go and *.pb.go excluded). The
        per-package gate is scoped to those PR-touched packages.

      * Package mode (fallback / local exploration)
        Without -ChangedFiles, the script lists the most-changed packages
        across the whole module and the per-package gate considers every
        package.

    In both modes, the absolute floor gate runs identically.

    Status values used in package output:
      improved  — package coverage went up
      regress   — package coverage went down
      new       — package has no baseline entry
      ok        — package coverage unchanged (or below MinDelta)

    Exit codes:
      0 — success / no breach
      2 — at least one gate breached and -FailOnGate was set
      (any other non-zero exit indicates a script error)

    See cli/azd/docs/code-coverage-guide.md for context on coverage modes.

.PARAMETER BaselineFile
    Path to the baseline Go coverage profile (e.g. main's cover.out).

.PARAMETER CurrentFile
    Path to the current branch's coverage profile.

.PARAMETER ChangedFiles
    Array of file paths touched by the PR (newline- or comma-delimited
    when passed as a single string). Paths may be repo-relative
    (e.g. "cli/azd/pkg/foo/bar.go") or module-relative
    (e.g. "pkg/foo/bar.go"); the script tries both. Non-Go files,
    test files (*_test.go) and generated *.pb.go files are ignored.

.PARAMETER ChangedFilesFromFile
    Path to a plain newline-delimited file listing changed files.
    Equivalent to -ChangedFiles, but sourced from disk so callers
    (e.g. CI pipelines emitting a long file list from `gh api`) can
    avoid command-line length limits. Mutually compatible with
    -ChangedFiles — both may be supplied and the union is used.

.PARAMETER MaxPackageDecrease
    Maximum tolerated per-package coverage decrease in percentage points.
    For example, with the default 0.5, a package at 80.0% may drop to
    79.5% without failing; 79.4% (-0.6 pp) trips the gate. In
    changed-file mode this only considers PR-touched packages.
    Default: 0.5.

.PARAMETER MinOverallCoverage
    Absolute floor (in percent) for overall coverage. CI fails if the
    current overall is below this value. Default: 69.0.

.PARAMETER FailOnGate
    Exit with code 2 if any gate (per-package decrease or absolute
    floor) is breached. CI sets this explicitly so a regression blocks
    merge; local runs leave it off by default for advisory output.

.PARAMETER BaselineLabel
    Free-form label describing the baseline (e.g. "main build 123456").
    Printed in the report header. Default: "baseline".

.PARAMETER TopN
    In package mode, maximum number of changed packages to display.
    Default: 20.

.PARAMETER MinDelta
    In package mode, minimum absolute percentage-point change to include
    a package in the table. Default: 0.1.

.PARAMETER ModulePrefix
    Go module import path prefix to strip from package and file names
    for readability. Auto-detected from go.mod when possible.

.EXAMPLE
    # Local: package-level diff to terminal (advisory, no gate)
    ./Get-CoverageDiff.ps1 -BaselineFile cover-main.out -CurrentFile cover.out

.EXAMPLE
    # CI: per-package report scoped to changed files, fail on per-package regression or floor
    ./Get-CoverageDiff.ps1 `
        -BaselineFile cover-baseline.out `
        -CurrentFile cover.out `
        -ChangedFilesFromFile changed-files.txt `
        -BaselineLabel "main build 123456 / commit abcdef" `
        -MaxPackageDecrease 0.5 `
        -MinOverallCoverage 69 `
        -FailOnGate
#>

param(
    [Parameter(Mandatory = $true)]
    [string]$BaselineFile,

    [Parameter(Mandatory = $true)]
    [string]$CurrentFile,

    [string[]]$ChangedFiles,

    [string]$ChangedFilesFromFile,

    [ValidateScript({ $_ -ge 0 -and $_ -le 100 -or $_ -eq -1 }, ErrorMessage =
        '-MaxPackageDecrease must be between 0 and 100, or -1 to disable the gate.')]
    [double]$MaxPackageDecrease = 0.5,

    [ValidateScript({ $_ -ge 0 -and $_ -le 100 -or $_ -eq -1 }, ErrorMessage =
        '-MinOverallCoverage must be between 0 and 100, or -1 to disable the floor gate.')]
    [double]$MinOverallCoverage = 69.0,

    [switch]$FailOnGate,

    [string]$BaselineLabel = 'baseline',

    [ValidateRange(1, [int]::MaxValue)]
    [int]$TopN = 20,

    [ValidateRange(0, 100)]
    [double]$MinDelta = 0.1,

    [string]$ModulePrefix = ''
)

$ErrorActionPreference = 'Stop'
Set-StrictMode -Version 4

# ---------------------------------------------------------------------------
# Validate inputs
# ---------------------------------------------------------------------------
if (-not (Test-Path -LiteralPath $BaselineFile)) {
    throw "Baseline coverage file not found: $BaselineFile"
}
if (-not (Test-Path -LiteralPath $CurrentFile)) {
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
        if (Test-Path -LiteralPath $candidate) {
            $modLine = @(Get-Content -LiteralPath $candidate -Encoding UTF8) |
                Where-Object { $_ -match '^module\s+' } |
                Select-Object -First 1
            if ($modLine -match '^module\s+(.+)$') {
                $ModulePrefix = $Matches[1].Trim()
                break
            }
        }
    }
    if (-not $ModulePrefix) {
        $ModulePrefix = 'github.com/azure/azure-dev/cli/azd'
    }
}

# Normalize module prefix: forward slashes, single trailing slash. Applies to
# both auto-detected and user-supplied values so downstream StartsWith()
# comparisons strip cleanly without producing leading-slash module-relative
# keys.
$ModulePrefix = $ModulePrefix.Trim().Replace('\', '/')
if ($ModulePrefix -and -not $ModulePrefix.EndsWith('/')) {
    $ModulePrefix += '/'
}

# Repo-relative prefix for the Go module. When the user passes repo-relative
# paths (as `gh api .../files` returns), we strip this to recover the
# module-relative path used as the file key.
$repoRelativeModulePrefix = 'cli/azd/'

$inv = [System.Globalization.CultureInfo]::InvariantCulture

# ---------------------------------------------------------------------------
# Parse a Go text coverage profile.
#
# Each non-header line has the format:
#   file:startLine.startCol,endLine.endCol numStatements hitCount
#
# Returns a hashtable with:
#   Packages        — packageName -> @{ Statements; Covered }
#   Files           — moduleRelativeFilePath -> @{ Statements; Covered }
#   TotalStatements — int
#   TotalCovered    — int
# ---------------------------------------------------------------------------
function Read-CoverageProfile {
    param([string]$FilePath)

    # ReadAllLines is ~2-5x faster than Get-Content for large profiles
    # (45k+ lines) because it bypasses PowerShell's per-line pipeline overhead.
    $lines = [System.IO.File]::ReadAllLines($FilePath, [System.Text.Encoding]::UTF8)
    if ($lines.Length -eq 0) {
        throw "Coverage file is empty: $FilePath"
    }

    if ($lines[0] -notmatch '^mode:\s') {
        throw "Coverage file does not start with a mode line: $($lines[0])"
    }

    $packages = @{}
    $files = @{}
    $totalStatements = 0
    $totalCovered = 0
    $skippedLines = 0

    for ($i = 1; $i -lt $lines.Length; $i++) {
        $line = $lines[$i]
        if ([string]::IsNullOrWhiteSpace($line)) { continue }

        if ($line -notmatch '^(.+?):(\d+\.\d+),(\d+\.\d+)\s+(\d+)\s+(\d+)$') {
            $skippedLines++
            continue
        }

        $filePath = $Matches[1]
        $stmts = [int]$Matches[4]
        $hits = [int]$Matches[5]

        # Module-relative path: strip module prefix when present (case-insensitive
        # — go.mod casing may differ from the casing in user-supplied paths).
        $rel = if ($filePath.StartsWith($ModulePrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
            $filePath.Substring($ModulePrefix.Length)
        } else {
            $filePath
        }

        $lastSlash = $rel.LastIndexOf('/')
        $pkg = if ($lastSlash -ge 0) { $rel.Substring(0, $lastSlash) } else { '.' }

        if (-not $packages.ContainsKey($pkg)) {
            $packages[$pkg] = @{ Statements = 0; Covered = 0 }
        }
        $packages[$pkg].Statements += $stmts
        if ($hits -gt 0) { $packages[$pkg].Covered += $stmts }

        if (-not $files.ContainsKey($rel)) {
            $files[$rel] = @{ Statements = 0; Covered = 0 }
        }
        $files[$rel].Statements += $stmts
        if ($hits -gt 0) { $files[$rel].Covered += $stmts }

        $totalStatements += $stmts
        if ($hits -gt 0) { $totalCovered += $stmts }
    }

    if ($skippedLines -gt 0) {
        Write-Warning "${FilePath}: skipped $skippedLines line(s) that did not match the expected coverprofile format."
    }

    # Defend against silent passes: a profile with a valid mode line but every
    # entry malformed would otherwise produce zero statements and slip through
    # the gate as "no coverage data".
    if ($totalStatements -eq 0 -and $skippedLines -gt 0) {
        throw "Coverage file '$FilePath' contained no valid coverage entries (skipped $skippedLines malformed line(s))."
    }

    return @{
        Packages        = $packages
        Files           = $files
        TotalStatements = $totalStatements
        TotalCovered    = $totalCovered
    }
}

# ---------------------------------------------------------------------------
# Normalize a changed-file path to module-relative form.
# Returns $null for non-Go files, test files, and generated *.pb.go files.
# ---------------------------------------------------------------------------
function ConvertTo-ModuleRelative {
    param([string]$Path)

    if ([string]::IsNullOrWhiteSpace($Path)) { return $null }
    $p = $Path.Trim().Replace('\', '/')
    if (-not $p.EndsWith('.go',      [System.StringComparison]::OrdinalIgnoreCase)) { return $null }
    if ($p.EndsWith('_test.go',      [System.StringComparison]::OrdinalIgnoreCase)) { return $null }
    if ($p.EndsWith('.pb.go',        [System.StringComparison]::OrdinalIgnoreCase)) { return $null }

    if ($p.StartsWith($repoRelativeModulePrefix, [System.StringComparison]::OrdinalIgnoreCase)) {
        return $p.Substring($repoRelativeModulePrefix.Length)
    }
    return $p
}

# ---------------------------------------------------------------------------
# Compute coverage percentage as a raw double. Display sites format with
# {0:F1} for one-decimal output; gate comparisons use the raw value to
# avoid threshold false negatives at boundaries (e.g. 68.96% rounding up
# to 69.0% and passing a 69 floor, or a 0.54 pp drop rounding to 0.5 pp
# and passing a 0.5 tolerance).
# ---------------------------------------------------------------------------
function Get-Percent {
    param([int]$Covered, [int]$Statements)
    if ($Statements -le 0) { return 0.0 }
    return ($Covered / $Statements) * 100
}

# ---------------------------------------------------------------------------
# Format a fixed-width status table row.
# ---------------------------------------------------------------------------
function Format-FileRow {
    param(
        [string]$Path,
        [double]$Before,
        [double]$After,
        [double]$Delta,
        [string]$Status,
        [string]$Note
    )
    $sign = if ($Delta -ge 0) { '+' } else { '' }
    $beforeStr = if ($Before -lt 0) { '   -' } else { ('{0,5}%' -f $Before.ToString('F1', $script:inv)) }
    $afterStr  = ('{0,5}%' -f $After.ToString('F1', $script:inv))
    $deltaStr  = ('{0}{1} pp' -f $sign, $Delta.ToString('F1', $script:inv))
    $line = ('  {0,-60}  {1} -> {2}  ({3,9})  {4,-8}' -f $Path, $beforeStr, $afterStr, $deltaStr, $Status)
    if ($Note) { $line += "  $Note" }
    return $line
}

# ---------------------------------------------------------------------------
# Parse profiles
# ---------------------------------------------------------------------------
Write-Host "Parsing baseline: $BaselineFile"
$baseline = Read-CoverageProfile -FilePath $BaselineFile

Write-Host "Parsing current:  $CurrentFile"
$current = Read-CoverageProfile -FilePath $CurrentFile

$baseTotal = Get-Percent $baseline.TotalCovered $baseline.TotalStatements
$currTotal = Get-Percent $current.TotalCovered $current.TotalStatements
$overallDelta = [math]::Round($currTotal - $baseTotal, 1)

Write-Host ("  Baseline: {0}% ({1}/{2} stmts)" -f $baseTotal.ToString('F1', $inv), $baseline.TotalCovered, $baseline.TotalStatements)
Write-Host ("  Current:  {0}% ({1}/{2} stmts)" -f $currTotal.ToString('F1', $inv), $current.TotalCovered, $current.TotalStatements)
Write-Host ("  Delta:    {0} pp" -f $overallDelta.ToString('F1', $inv))

# ---------------------------------------------------------------------------
# Collect changed files (union of -ChangedFiles and -ChangedFilesFromFile)
# ---------------------------------------------------------------------------
$changedRaw = @()
if ($ChangedFiles) {
    foreach ($entry in $ChangedFiles) {
        if ($null -eq $entry) { continue }
        $changedRaw += ($entry -split "[`r`n,]")
    }
}
if ($ChangedFilesFromFile) {
    if (-not (Test-Path -LiteralPath $ChangedFilesFromFile)) {
        throw "Changed-files file not found: $ChangedFilesFromFile"
    }
    $changedRaw += @([System.IO.File]::ReadAllLines($ChangedFilesFromFile, [System.Text.Encoding]::UTF8))
}

$changedSet = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::OrdinalIgnoreCase)
foreach ($raw in $changedRaw) {
    $rel = ConvertTo-ModuleRelative -Path $raw
    if ($rel) { [void]$changedSet.Add($rel) }
}

# If the user explicitly supplied a changed-files input we enter
# "changed-file mode": the per-package report AND the per-package gate
# (-MaxPackageDecrease) are both scoped to packages that contain at least
# one touched file. The absolute floor gate (-MinOverallCoverage) is
# always computed against the full repository total — it is not affected
# by changed-file scope.
$changedFilesSupplied = ($null -ne $ChangedFiles -and $ChangedFiles.Count -gt 0) -or `
                       (-not [string]::IsNullOrWhiteSpace($ChangedFilesFromFile))
$useChangedFileMode = $changedFilesSupplied

# ---------------------------------------------------------------------------
# Build report
# ---------------------------------------------------------------------------
$sb = [System.Text.StringBuilder]::new()
$bar = '=' * 60

[void]$sb.AppendLine($bar)
[void]$sb.AppendLine('Coverage Report')
[void]$sb.AppendLine($bar)
[void]$sb.AppendLine("Baseline: $BaselineLabel")
[void]$sb.AppendLine()

$deltaSign = if ($overallDelta -ge 0) { '+' } else { '' }
[void]$sb.AppendLine(
    ('Overall: {0}% -> {1}% ({2}{3} pp)' -f `
        $baseTotal.ToString('F1', $inv), `
        $currTotal.ToString('F1', $inv), `
        $deltaSign, `
        $overallDelta.ToString('F1', $inv))
)
[void]$sb.AppendLine(
    ('  Tolerance: -{0} pp per package before failing the gate' -f $MaxPackageDecrease.ToString('F1', $inv))
)
[void]$sb.AppendLine(
    ('  Floor: overall coverage must stay >= {0}%' -f $MinOverallCoverage.ToString('F1', $inv))
)
[void]$sb.AppendLine()

# ---------------------------------------------------------------------------
# Determine which packages to report on.
# ---------------------------------------------------------------------------
function Get-PackageRow {
    param(
        [string]$Pkg,
        [hashtable]$Baseline,
        [hashtable]$Current,
        [int]$TouchedFileCount
    )

    $bPkg = $Baseline.Packages[$Pkg]
    $cPkg = $Current.Packages[$Pkg]

    $bStmts = if ($bPkg) { $bPkg.Statements } else { 0 }
    $bCov   = if ($bPkg) { $bPkg.Covered } else { 0 }
    $cStmts = if ($cPkg) { $cPkg.Statements } else { 0 }
    $cCov   = if ($cPkg) { $cPkg.Covered } else { 0 }

    $bPct = if ($bPkg) { Get-Percent $bCov $bStmts } else { -1.0 }
    $cPct = Get-Percent $cCov $cStmts
    $delta = if ($bPkg) { $cPct - $bPct } else { 0.0 }

    $status = 'ok'
    $note = if ($TouchedFileCount -gt 0) {
        "$TouchedFileCount file$(if ($TouchedFileCount -ne 1) { 's' }) touched"
    } else { '' }

    if (-not $bPkg)        { $status = 'new' }
    elseif (-not $cPkg)    { $status = 'deleted' }
    elseif ($delta -gt 0)  { $status = 'improved' }
    elseif ($delta -lt 0)  { $status = 'regress' }

    return [PSCustomObject]@{
        Package     = $Pkg
        Before      = $bPct
        After       = $cPct
        Delta       = $delta
        Status      = $status
        Note        = $note
        Stmts       = [math]::Max($bStmts, $cStmts)
        AbsDelta    = [math]::Abs($delta)
        StatusOrder = switch ($status) { 'regress' { 0 } 'improved' { 1 } 'new' { 2 } default { 3 } }
    }
}

$script:packageRowMap = @{}
if ($useChangedFileMode) {
    # -----------------------------------------------------------------------
    # Per-package mode scoped to packages containing PR-touched files.
    # -----------------------------------------------------------------------
    $touchedByPackage = @{}
    foreach ($rel in $changedSet) {
        $lastSlash = $rel.LastIndexOf('/')
        $pkg = if ($lastSlash -ge 0) { $rel.Substring(0, $lastSlash) } else { '.' }
        # Include the package if EITHER (a) this file appears in coverage data
        # directly, OR (b) the file's inferred package has any coverage entries
        # in either profile. (b) ensures touched-but-uncovered files (constants,
        # build-tagged, generated stubs) still count their package toward the
        # per-package gate when the package itself is tracked. Truly orphan
        # paths (no coverage anywhere in the package) are still skipped.
        if (-not $baseline.Files.ContainsKey($rel) -and `
            -not $current.Files.ContainsKey($rel) -and `
            -not $baseline.Packages.ContainsKey($pkg) -and `
            -not $current.Packages.ContainsKey($pkg)) {
            continue
        }
        if (-not $touchedByPackage.ContainsKey($pkg)) {
            $touchedByPackage[$pkg] = 0
        }
        $touchedByPackage[$pkg] += 1
    }

    if ($touchedByPackage.Count -eq 0) {
        [void]$sb.AppendLine('PR-touched packages: none with coverage data.')
        [void]$sb.AppendLine()
    } else {
        # Build rows once and cache by package — reused by the gate loop below.
        $rows = foreach ($pkg in ($touchedByPackage.Keys | Sort-Object)) {
            $row = Get-PackageRow -Pkg $pkg -Baseline $baseline -Current $current `
                -TouchedFileCount $touchedByPackage[$pkg]
            $script:packageRowMap[$pkg] = $row
            $row
        }

        [void]$sb.AppendLine(
            "PR-touched packages ($($rows.Count) package$(if ($rows.Count -ne 1) { 's' })):"
        )
        # Sort: regressions first by absolute delta, then improvements, then ok/new.
        # Uses precomputed StatusOrder/AbsDelta properties so each row is sorted by
        # property lookup rather than re-evaluating a scriptblock per comparison.
        $sorted = $rows | Sort-Object -Property StatusOrder, @{Expression='AbsDelta'; Descending=$true}, Package
        foreach ($row in $sorted) {
            [void]$sb.AppendLine((Format-FileRow `
                -Path $row.Package `
                -Before $row.Before `
                -After $row.After `
                -Delta $row.Delta `
                -Status $row.Status `
                -Note $row.Note))
        }
        [void]$sb.AppendLine()
    }
} else {
    # -----------------------------------------------------------------------
    # Package mode (no changed-file list): show top changed packages.
    # -----------------------------------------------------------------------
    $allPackages = @(@($baseline.Packages.Keys) + @($current.Packages.Keys)) |
        Sort-Object -Unique

    # Build rows once and cache by package — reused by the gate loop below.
    $allDiffs = [System.Collections.Generic.List[PSCustomObject]]::new()

    foreach ($pkg in $allPackages) {
        $row = Get-PackageRow -Pkg $pkg -Baseline $baseline -Current $current -TouchedFileCount 0
        $script:packageRowMap[$pkg] = $row
        if ($row.AbsDelta -ge $MinDelta -and $row.Delta -ne 0) {
            $allDiffs.Add($row)
        }
    }

    $changedPackageCount = $allDiffs.Count
    $tableDiffs = @($allDiffs |
        Sort-Object -Property @{Expression='AbsDelta'; Descending=$true} |
        Select-Object -First $TopN)

    if ($tableDiffs.Count -eq 0) {
        [void]$sb.AppendLine("No packages changed by >= $($MinDelta.ToString('F1', $inv)) percentage points.")
        [void]$sb.AppendLine()
    } else {
        [void]$sb.AppendLine("Top $([math]::Min($TopN, $changedPackageCount)) changed packages (of ${changedPackageCount}):")
        foreach ($d in $tableDiffs) {
            [void]$sb.AppendLine((Format-FileRow `
                -Path $d.Package `
                -Before $d.Before `
                -After $d.After `
                -Delta $d.Delta `
                -Status $d.Status `
                -Note ''))
        }
        if ($changedPackageCount -gt $TopN) {
            [void]$sb.AppendLine("  ... and $($changedPackageCount - $TopN) more packages with smaller changes.")
        }
        [void]$sb.AppendLine()
    }
}

# ---------------------------------------------------------------------------
# Multi-gate evaluation
# ---------------------------------------------------------------------------
# Two independent breach types — any one breach fails the build when
# -FailOnGate is set:
#   1. Overall coverage below -MinOverallCoverage absolute floor
#   2. Any package decrease beyond -MaxPackageDecrease
#
# Per-package gate scope: in changed-file mode we only consider PR-touched
# packages (developers shouldn't be blamed for unrelated package movement
# from baseline drift); in package mode we consider every package present
# in either profile (full scan).
$overallFloorBreached = ($MinOverallCoverage -ge 0) -and ($currTotal -lt $MinOverallCoverage)

# Determine per-package breach set. -MaxPackageDecrease < 0 disables the
# per-package gate (advisory output only); skip the loop entirely so a
# disabled gate can never report breaches.
$pkgGateScope = if ($MaxPackageDecrease -lt 0) {
    @()
} elseif ($useChangedFileMode) {
    @($touchedByPackage.Keys)
} else {
    @(@($baseline.Packages.Keys) + @($current.Packages.Keys)) | Sort-Object -Unique
}

$packageBreaches = [System.Collections.Generic.List[PSCustomObject]]::new()
foreach ($pkg in $pkgGateScope) {
    # Reuse the row computed during display rather than recomputing — avoids
    # the duplicate Get-PackageRow call per package that the original
    # implementation made (one for display, one for the gate).
    if ($script:packageRowMap.ContainsKey($pkg)) {
        $row = $script:packageRowMap[$pkg]
    } else {
        $row = Get-PackageRow -Pkg $pkg -Baseline $baseline -Current $current -TouchedFileCount 0
    }
    # Only existing packages with a real regression count toward the gate;
    # 'new' packages (no baseline) and packages absent from current with
    # 0 statements aren't comparable.
    if ($row.Status -ne 'regress') { continue }
    # Use raw delta (no rounding) to avoid boundary false negatives:
    # a 0.54 pp drop must NOT round down to 0.5 pp and pass a 0.5 tolerance.
    $pkgDecrease = -$row.Delta
    if ($pkgDecrease -gt $MaxPackageDecrease) {
        $packageBreaches.Add([PSCustomObject]@{
            Package  = $pkg
            Before   = $row.Before
            After    = $row.After
            Decrease = $pkgDecrease
        })
    }
}
$packageGateBreached = $packageBreaches.Count -gt 0

$gateBreached = $overallFloorBreached -or $packageGateBreached

[void]$sb.AppendLine($bar)

if ($gateBreached) {
    [void]$sb.AppendLine('RESULT: FAIL')
    [void]$sb.AppendLine($bar)
    [void]$sb.AppendLine('Breached gate(s):')
    if ($overallFloorBreached) {
        [void]$sb.AppendLine(
            ('  - Overall coverage {0}% is below floor of {1}%' -f $currTotal.ToString('F1', $inv), $MinOverallCoverage.ToString('F1', $inv))
        )
    }
    if ($packageGateBreached) {
        [void]$sb.AppendLine(
            ('  - {0} package(s) dropped more than {1} pp:' -f $packageBreaches.Count, $MaxPackageDecrease.ToString('F1', $inv))
        )
        $sortedBreaches = $packageBreaches | Sort-Object -Property Decrease -Descending
        foreach ($pb in $sortedBreaches) {
            [void]$sb.AppendLine(
                ('      {0}: {1}% -> {2}% (-{3} pp)' -f $pb.Package, $pb.Before.ToString('F1', $inv), $pb.After.ToString('F1', $inv), $pb.Decrease.ToString('F1', $inv))
            )
        }
    }
    [void]$sb.AppendLine($bar)
    [void]$sb.AppendLine('How to fix:')
    [void]$sb.AppendLine('  1. Add tests for the regressing packages listed above.')
    [void]$sb.AppendLine('  2. Re-run locally:  mage coverage:unit && mage coverage:diff')
    [void]$sb.AppendLine(
        ('  3. CI fails when overall falls below {0}% or any package drops more than {1} pp.' -f $MinOverallCoverage.ToString('F1', $inv), $MaxPackageDecrease.ToString('F1', $inv))
    )
} else {
    [void]$sb.AppendLine('RESULT: PASS')
    [void]$sb.AppendLine($bar)
}

$report = $sb.ToString()

Write-Host ""
Write-Output $report

# ---------------------------------------------------------------------------
# Exit code
# ---------------------------------------------------------------------------
if ($gateBreached -and $FailOnGate) {
    if ($overallFloorBreached) {
        Write-Host ('##vso[task.logissue type=error]Overall coverage {0}% is below floor of {1}%.' -f $currTotal.ToString('F1', $inv), $MinOverallCoverage.ToString('F1', $inv))
    }
    if ($packageGateBreached) {
        $sortedBreaches = $packageBreaches | Sort-Object -Property Decrease -Descending
        foreach ($pb in $sortedBreaches) {
            Write-Host ('##vso[task.logissue type=error]Package {0} dropped {1} pp (max allowed: -{2} pp).' -f $pb.Package, $pb.Decrease.ToString('F1', $inv), $MaxPackageDecrease.ToString('F1', $inv))
        }
    }
    exit 2
}

exit 0

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

    Two modes are supported:

      * Changed-file mode (recommended for CI)
        Provide -ChangedFiles or -ChangedFilesFromFile. The script reports per-
        file deltas only for files touched by the PR and enforces a per-
        file coverage floor (-MinFloor, default 60%). Any touched file
        whose coverage drops below the floor is marked FAIL and, if
        -FailOnFloorBreach is set, the script exits with code 2 so the
        CI stage fails and merge is blocked.

      * Package mode (fallback / local exploration)
        Without -ChangedFiles, the script lists the most-changed packages.
        No floor is enforced and the script always exits 0.

    Status values used in output:
      FAIL      — touched file is below the floor
      improved  — touched file coverage went up
      new       — touched file is new and meets / exceeds floor
      ok        — touched file is at or above floor and not changed enough
                  to call out otherwise

    Exit codes:
      0 — success / no breach
      2 — floor breach detected and -FailOnFloorBreach was set
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
    (e.g. "pkg/foo/bar.go"); the script tries both. Non-Go files and
    test files (*_test.go) are ignored.

.PARAMETER ChangedFilesFromFile
    Path to a plain newline-delimited file listing changed files.
    Equivalent to -ChangedFiles, but sourced from disk so callers
    (e.g. CI pipelines emitting a long file list from `gh api`) can
    avoid command-line length limits. Mutually compatible with
    -ChangedFiles — both may be supplied and the union is used.

.PARAMETER MinFloor
    Per-file coverage floor (percent). Touched files at or above this
    value pass; any file below is marked FAIL regardless of baseline.
    Default: 60.

.PARAMETER MinNewFileStatements
    A new file (no baseline coverage entry) is only checked against the
    floor when it has at least this many executable statements. Smaller
    files (e.g. tiny helpers, scaffolding, 3-line additions) are reported
    as "new" but never fail the build. Default: 10.

.PARAMETER FailOnFloorBreach
    Exit with code 2 if any touched file is marked FAIL. CI sets this
    explicitly so a regression blocks merge; local runs leave it off
    by default for advisory output.

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
    # Local: package-level diff to terminal (no floor check)
    ./Get-CoverageDiff.ps1 -BaselineFile cover-main.out -CurrentFile cover.out

.EXAMPLE
    # CI: per-file check against changed files, fail on floor breach
    ./Get-CoverageDiff.ps1 `
        -BaselineFile cover-baseline.out `
        -CurrentFile cover.out `
        -ChangedFilesFromFile changed-files.txt `
        -BaselineLabel "main build 123456 / commit abcdef" `
        -FailOnFloorBreach
#>

param(
    [Parameter(Mandatory = $true)]
    [string]$BaselineFile,

    [Parameter(Mandatory = $true)]
    [string]$CurrentFile,

    [string[]]$ChangedFiles,

    [string]$ChangedFilesFromFile,

    [ValidateRange(0, 100)]
    [double]$MinFloor = 60.0,

    [ValidateRange(0, [int]::MaxValue)]
    [int]$MinNewFileStatements = 10,

    [switch]$FailOnFloorBreach,

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

    $lines = @(Get-Content -LiteralPath $FilePath -Encoding UTF8)
    if ($lines.Count -eq 0) {
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

    for ($i = 1; $i -lt $lines.Count; $i++) {
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
# Compute coverage percentage with one decimal of precision.
# ---------------------------------------------------------------------------
function Get-Percent {
    param([int]$Covered, [int]$Statements)
    if ($Statements -le 0) { return 0.0 }
    return [math]::Round(($Covered / $Statements) * 100, 1)
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
    $beforeStr = if ($Before -lt 0) { '   -' } else { ('{0,5:F1}%' -f $Before) }
    $afterStr  = ('{0,5:F1}%' -f $After)
    $deltaStr  = ('{0}{1:F1}%' -f $sign, $Delta)
    $line = ('  {0,-60}  {1} -> {2}  ({3,8})  {4,-8}' -f $Path, $beforeStr, $afterStr, $deltaStr, $Status)
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

Write-Host "  Baseline: $baseTotal% ($($baseline.TotalCovered)/$($baseline.TotalStatements) stmts)"
Write-Host "  Current:  $currTotal% ($($current.TotalCovered)/$($current.TotalStatements) stmts)"
Write-Host "  Delta:    $overallDelta pp"

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
    $changedRaw += @(Get-Content -LiteralPath $ChangedFilesFromFile -Encoding UTF8)
}

$changedSet = [System.Collections.Generic.HashSet[string]]::new([System.StringComparer]::OrdinalIgnoreCase)
foreach ($raw in $changedRaw) {
    $rel = ConvertTo-ModuleRelative -Path $raw
    if ($rel) { [void]$changedSet.Add($rel) }
}

# If the user explicitly supplied a changed-files input we stay in file mode
# even when the filtered set is empty (e.g. PR only touched docs / test files).
# Falling back to package mode in that case would surface unrelated package
# noise that has nothing to do with the PR.
$changedFilesSupplied = ($null -ne $ChangedFiles -and $ChangedFiles.Count -gt 0) -or `
                       (-not [string]::IsNullOrWhiteSpace($ChangedFilesFromFile))
$useFileMode = $changedFilesSupplied

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
    ('Overall: {0:F1}% -> {1:F1}% ({2}{3:F1}%)' -f $baseTotal, $currTotal, $deltaSign, $overallDelta)
)
[void]$sb.AppendLine()

$failures = [System.Collections.Generic.List[PSCustomObject]]::new()

if ($useFileMode) {
    # -----------------------------------------------------------------------
    # Per-file mode: walk the changed-file set and check the floor.
    # -----------------------------------------------------------------------
    $rows = [System.Collections.Generic.List[PSCustomObject]]::new()

    foreach ($rel in ($changedSet | Sort-Object)) {
        $bFile = $baseline.Files[$rel]
        $cFile = $current.Files[$rel]

        # File was deleted in the current branch (no current entry); skip.
        if (-not $cFile) { continue }

        $bStmts = if ($bFile) { $bFile.Statements } else { 0 }
        $bCov   = if ($bFile) { $bFile.Covered } else { 0 }
        $cStmts = $cFile.Statements
        $cCov   = $cFile.Covered

        if ($cStmts -le 0) { continue }  # file has no executable statements

        $bPct = if ($bFile) { Get-Percent $bCov $bStmts } else { -1.0 }
        $cPct = Get-Percent $cCov $cStmts
        $delta = if ($bFile) { [math]::Round($cPct - $bPct, 1) } else { 0.0 }

        $isNew = -not $bFile
        $belowFloor = $cPct -lt $MinFloor
        $isSmallNewFile = $isNew -and ($cStmts -lt $MinNewFileStatements)

        $status = 'ok'
        $note = ''

        if ($isSmallNewFile) {
            $status = 'new'
            $note = "small file ($cStmts stmts)"
        } elseif ($belowFloor) {
            $status = 'FAIL'
            if ($isNew) {
                $note = "new file below floor (${MinFloor}%)"
            } else {
                $note = "below floor (${MinFloor}%)"
            }
            $failures.Add([PSCustomObject]@{ Path = $rel; Reason = $note })
        } elseif ($isNew) {
            $status = 'new'
        } elseif ($delta -gt 0) {
            $status = 'improved'
        }

        $rows.Add([PSCustomObject]@{
            Path   = $rel
            Before = $bPct
            After  = $cPct
            Delta  = $delta
            Status = $status
            Note   = $note
            Stmts  = $cStmts
        })
    }

    if ($rows.Count -eq 0) {
        [void]$sb.AppendLine("PR-touched Go files: none with coverage data.")
        [void]$sb.AppendLine()
    } else {
        [void]$sb.AppendLine("PR-touched files ($($rows.Count) file$(if ($rows.Count -ne 1) { 's' })):")
        # Sort: failures first, then improvements/regressions by abs delta, then ok.
        $sorted = $rows | Sort-Object `
            @{ Expression = { switch ($_.Status) { 'FAIL' { 0 } 'info' { 1 } 'improved' { 2 } 'new' { 3 } default { 4 } } } }, `
            @{ Expression = { -[math]::Abs($_.Delta) } }, `
            Path
        foreach ($row in $sorted) {
            [void]$sb.AppendLine((Format-FileRow `
                -Path $row.Path `
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

    $allDiffs = [System.Collections.Generic.List[PSCustomObject]]::new()

    foreach ($pkg in $allPackages) {
        $bPkg = $baseline.Packages[$pkg]
        $cPkg = $current.Packages[$pkg]

        $bStmts = if ($bPkg) { $bPkg.Statements } else { 0 }
        $bCov   = if ($bPkg) { $bPkg.Covered } else { 0 }
        $cStmts = if ($cPkg) { $cPkg.Statements } else { 0 }
        $cCov   = if ($cPkg) { $cPkg.Covered } else { 0 }

        $bPct = Get-Percent $bCov $bStmts
        $cPct = Get-Percent $cCov $cStmts
        $delta = [math]::Round($cPct - $bPct, 1)

        if ([math]::Abs($delta) -ge $MinDelta -and $delta -ne 0) {
            $allDiffs.Add([PSCustomObject]@{
                Package = $pkg
                Before  = $bPct
                After   = $cPct
                Delta   = $delta
                Stmts   = [math]::Max($bStmts, $cStmts)
            })
        }
    }

    $changedPackageCount = $allDiffs.Count
    $tableDiffs = @($allDiffs |
        Sort-Object { -[math]::Abs($_.Delta) } |
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
                -Status '' `
                -Note ''))
        }
        if ($changedPackageCount -gt $TopN) {
            [void]$sb.AppendLine("  ... and $($changedPackageCount - $TopN) more packages with smaller changes.")
        }
        [void]$sb.AppendLine()
    }
}

# ---------------------------------------------------------------------------
# Result banner
# ---------------------------------------------------------------------------
[void]$sb.AppendLine($bar)

if ($failures.Count -gt 0) {
    $msg = "RESULT: FAIL — $($failures.Count) touched file$(if ($failures.Count -ne 1) { 's' }) below ${MinFloor}% floor"
    [void]$sb.AppendLine($msg)
    [void]$sb.AppendLine($bar)
    [void]$sb.AppendLine('How to fix:')
    $i = 1
    foreach ($f in $failures) {
        [void]$sb.AppendLine("  $i. Add tests for $($f.Path) to cover at least ${MinFloor}% of statements,")
        [void]$sb.AppendLine("     or refactor it to remove untested code paths.")
        $i++
    }
    [void]$sb.AppendLine("  $i. Re-run locally:  mage coverage:unit && mage coverage:diff")
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
if ($failures.Count -gt 0 -and $FailOnFloorBreach) {
    # Surface a clickable error in the ADO PR check summary. Harmless under
    # GitHub Actions / local pwsh — the line is written verbatim and the
    # build-server-only renderer just treats it as plain text elsewhere.
    $failPaths = ($failures | ForEach-Object { $_.Path }) -join ', '
    Write-Host "##vso[task.logissue type=error]Coverage floor breach: $($failures.Count) PR-touched file(s) below ${MinFloor}%: $failPaths"
    exit 2
}
exit 0

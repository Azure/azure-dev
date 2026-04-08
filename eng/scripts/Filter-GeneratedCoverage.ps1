#!/usr/bin/env pwsh

<#
.SYNOPSIS
    Filters generated code from a Go coverage profile.

.DESCRIPTION
    Reads a Go coverage profile (cover.out) and removes entries for
    auto-generated files so that coverage percentages reflect only
    hand-written source code.

    This script is called automatically by both Get-LocalCoverageReport.ps1
    and Get-CICoverageReport.ps1 as part of the coverage pipeline. It can
    also be run standalone on any Go coverage profile.

    Detection uses file-name patterns (e.g. *.pb.go for protobuf) rather
    than reading source headers, so it works without file-system access to
    the original sources — important for CI where the coverage profile may
    have been merged from multiple platforms.

    See cli/azd/docs/code-coverage-guide.md for details on how generated
    code filtering fits into the overall coverage architecture.

.PARAMETER CoverageFile
    Path to the Go coverage profile to filter (typically cover.out).

.PARAMETER OutputFile
    Path to write the filtered profile. Defaults to overwriting
    CoverageFile in place.

.PARAMETER ExcludePatterns
    Array of filename glob patterns to exclude. Defaults to '*.pb.go'
    (protobuf-generated files), which is the only confirmed generated-file
    pattern in this repository.

    Patterns are matched against the filename portion of the Go import
    path (not the full path), using -like.

    Common generated-file patterns you might add for other projects:
      '*_generated.go'     — various code generators
      'zz_generated_*.go'  — Kubernetes code-gen
      '*_string.go'        — stringer tool output

.PARAMETER DryRun
    When set, prints statistics but does not write the output file.

.EXAMPLE
    # Filter in place
    ./Filter-GeneratedCoverage.ps1 -CoverageFile cover.out

.EXAMPLE
    # Write to a separate file
    ./Filter-GeneratedCoverage.ps1 -CoverageFile cover.out -OutputFile cover-filtered.out

.EXAMPLE
    # Preview the impact without writing
    ./Filter-GeneratedCoverage.ps1 -CoverageFile cover.out -DryRun

.EXAMPLE
    # Custom patterns
    ./Filter-GeneratedCoverage.ps1 -CoverageFile cover.out -ExcludePatterns @('*.pb.go', 'zz_generated_*.go')
#>

param(
    [Parameter(Mandatory = $true)]
    [string]$CoverageFile,

    [Parameter(Mandatory = $false)]
    [string]$OutputFile,

    [Parameter(Mandatory = $false)]
    [string[]]$ExcludePatterns = @(
        '*.pb.go'
    ),

    [switch]$DryRun
)

$ErrorActionPreference = 'Stop'

if (-not (Test-Path $CoverageFile)) {
    throw "Coverage file '$CoverageFile' not found"
}

if (-not $OutputFile) {
    $OutputFile = $CoverageFile
}

$lines = Get-Content $CoverageFile
if ($lines.Count -eq 0) {
    throw "Coverage file '$CoverageFile' is empty"
}

# First line is the mode directive (e.g. "mode: set") — always keep it.
$modeLine = $lines[0]
if ($modeLine -notmatch '^mode:\s') {
    throw "Coverage file does not start with a mode line: $modeLine"
}

$kept = [System.Collections.Generic.List[string]]::new($lines.Count)
$kept.Add($modeLine)

$totalEntries = 0
$excludedEntries = 0
$excludedFiles = [System.Collections.Generic.HashSet[string]]::new()

for ($i = 1; $i -lt $lines.Count; $i++) {
    $line = $lines[$i]
    if ([string]::IsNullOrWhiteSpace($line)) { continue }

    $totalEntries++

    # Each coverage line starts with "import/path/file.go:..."
    # Extract the filename by taking the portion before the first ':'
    # and then the last path segment.
    $colonIdx = $line.IndexOf(':')
    if ($colonIdx -le 0) {
        # Malformed line — keep it to avoid data loss.
        $kept.Add($line)
        continue
    }

    $filePath = $line.Substring(0, $colonIdx)
    $slashIdx = $filePath.LastIndexOf('/')
    $fileName = if ($slashIdx -ge 0) { $filePath.Substring($slashIdx + 1) } else { $filePath }

    $excluded = $false
    foreach ($pattern in $ExcludePatterns) {
        if ($fileName -like $pattern) {
            $excluded = $true
            $excludedFiles.Add($filePath) | Out-Null
            break
        }
    }

    if ($excluded) {
        $excludedEntries++
    } else {
        $kept.Add($line)
    }
}

$keptEntries = $totalEntries - $excludedEntries

Write-Host "Filter-GeneratedCoverage:"
Write-Host "  Coverage entries: $totalEntries total, $excludedEntries excluded, $keptEntries kept"
Write-Host "  Generated files excluded: $($excludedFiles.Count)"

if ($excludedFiles.Count -gt 0) {
    # Show a summary — group by package for readability.
    $byPackage = @{}
    foreach ($f in $excludedFiles) {
        $slashIdx = $f.LastIndexOf('/')
        $pkg = if ($slashIdx -ge 0) { $f.Substring(0, $slashIdx) } else { '.' }
        $name = if ($slashIdx -ge 0) { $f.Substring($slashIdx + 1) } else { $f }
        if (-not $byPackage.ContainsKey($pkg)) {
            $byPackage[$pkg] = [System.Collections.Generic.List[string]]::new()
        }
        $byPackage[$pkg].Add($name)
    }
    foreach ($pkg in ($byPackage.Keys | Sort-Object)) {
        $fileCount = $byPackage[$pkg].Count
        # Shorten well-known module prefix for readability
        $shortPkg = $pkg -replace 'github\.com/azure/azure-dev/cli/azd/', ''
        Write-Host "    $shortPkg ($fileCount files)"
    }
}

if (-not $DryRun) {
    Set-Content -Path $OutputFile -Value $kept -Encoding UTF8
    Write-Host "  Output written to: $OutputFile"
} else {
    Write-Host "  (DryRun — no file written)"
}

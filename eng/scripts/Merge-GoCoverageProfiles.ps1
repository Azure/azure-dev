#!/usr/bin/env pwsh

<#
.SYNOPSIS
    Merges multiple Go coverage profiles into a single coverage profile.

.DESCRIPTION
    This script takes multiple Go coverage profile files (coverage.out format) and merges them
    into a single unified coverage profile. It handles deduplication and combines coverage
    counts for the same code blocks across different test runs or platforms.

.PARAMETER InputFiles
    Array of paths to the input coverage profile files to merge

.PARAMETER OutputFile
    Path where the merged coverage profile should be written

.EXAMPLE
    ./Merge-GoCoverageProfiles.ps1 -InputFiles @("unit.out", "integration.out") -OutputFile "merged.out"
#>

param(
    [Parameter(Mandatory = $true)]
    [string[]]$InputFiles,
    
    [Parameter(Mandatory = $true)]
    [string]$OutputFile,
    
    [Parameter(Mandatory = $false)]
    [switch]$NormalizePaths
)

$ErrorActionPreference = 'Stop'

function Normalize-GoFilePath {
    param([string]$GoFilePath)
    
    # Handle Go module paths like "github.com/azure/azure-dev/cli/azd/pkg/test/file.go"
    # Convert to relative paths like "cli/azd/pkg/test/file.go"
    
    # Common Go module path patterns to normalize
    $modulePatterns = @(
        "github.com/azure/azure-dev/",
        "github.com/Azure/azure-dev/"
    )
    
    $normalizedPath = $GoFilePath
    
    foreach ($pattern in $modulePatterns) {
        if ($normalizedPath.StartsWith($pattern)) {
            $normalizedPath = $normalizedPath.Substring($pattern.Length)
            break
        }
    }
    
    # Ensure we use forward slashes for consistency
    return $normalizedPath.Replace('\', '/')
}

function Parse-CoverageProfile {
    param(
        [string]$FilePath,
        [bool]$NormalizePaths = $false
    )
    
    if (-not (Test-Path $FilePath)) {
        Write-Warning "Coverage file not found: $FilePath"
        return @{}
    }
    
    try {
        $lines = Get-Content $FilePath -ErrorAction Stop
        if ($lines.Count -eq 0) {
            Write-Warning "Empty coverage file: $FilePath"
            return @{}
        }
        
        $coverage = @{}
        
        # Skip the mode line (first line) and process coverage data
        for ($i = 1; $i -lt $lines.Count; $i++) {
            $line = $lines[$i].Trim()
            if ([string]::IsNullOrEmpty($line)) { continue }
            
            # Parse line format: file.go:startLine.startCol,endLine.endCol numStmt count
            if ($line -match '^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$') {
                $originalFile = $matches[1]
                $startLine = [int]$matches[2]
                $startCol = [int]$matches[3]
                $endLine = [int]$matches[4]
                $endCol = [int]$matches[5]
                $numStmt = [int]$matches[6]
                $count = [int]$matches[7]
                
                # Normalize file path if requested
                $file = if ($NormalizePaths) { 
                    Normalize-GoFilePath -GoFilePath $originalFile 
                } else { 
                    $originalFile 
                }
                
                # Create a unique key for this code block
                $blockKey = "${file}:${startLine}.${startCol},${endLine}.${endCol}"
                
                if ($coverage.ContainsKey($blockKey)) {
                    # Merge coverage counts for the same block
                    $coverage[$blockKey].count += $count
                } else {
                    $coverage[$blockKey] = @{
                        file = $file
                        startLine = $startLine
                        startCol = $startCol
                        endLine = $endLine
                        endCol = $endCol
                        numStmt = $numStmt
                        count = $count
                    }
                }
            } else {
                Write-Warning "Skipping invalid coverage line in $FilePath : $line"
            }
        }
        
        return $coverage
    }
    catch {
        Write-Error "Error reading coverage file $FilePath : $_"
        throw
    }
}

function Write-CoverageProfile {
    param(
        [hashtable]$Coverage,
        [string]$OutputPath
    )
    
    $outputLines = @("mode: set")
    
    # Sort blocks by file and then by line number for consistent output
    $sortedBlocks = $Coverage.GetEnumerator() | Sort-Object {
        $block = $_.Value
        "$($block.file):$($block.startLine.ToString().PadLeft(10, '0')):$($block.startCol.ToString().PadLeft(10, '0'))"
    }
    
    foreach ($entry in $sortedBlocks) {
        $block = $entry.Value
        $line = "$($block.file):$($block.startLine).$($block.startCol),$($block.endLine).$($block.endCol) $($block.numStmt) $($block.count)"
        $outputLines += $line
    }
    
    # Write to output file
    $outputLines | Out-File -FilePath $OutputPath -Encoding UTF8
}

# Main execution
Write-Host "Merging $($InputFiles.Count) Go coverage profile(s)..."

# Handle case where InputFiles might be passed as a single comma-separated string
if ($InputFiles.Count -eq 1 -and $InputFiles[0].Contains(',')) {
    $InputFiles = $InputFiles[0] -split ','
}

$mergedCoverage = @{}
$totalFiles = 0

foreach ($inputFile in $InputFiles) {
    $inputFile = $inputFile.Trim()
    if ([string]::IsNullOrEmpty($inputFile)) { continue }
    
    if (-not (Test-Path $inputFile)) {
        Write-Warning "Input file not found: $inputFile"
        continue
    }
    
    Write-Host "Processing: $inputFile"
    try {
        $fileCoverage = Parse-CoverageProfile -FilePath $inputFile -NormalizePaths $NormalizePaths.IsPresent
        
        if ($fileCoverage.Count -eq 0) {
            Write-Warning "No coverage data found in: $inputFile"
            continue
        }
        
        $totalFiles++
        
        # Merge this file's coverage into the overall coverage
        foreach ($blockKey in $fileCoverage.Keys) {
            if ($mergedCoverage.ContainsKey($blockKey)) {
                # Add coverage counts for the same block
                $mergedCoverage[$blockKey].count += $fileCoverage[$blockKey].count
            } else {
                # Copy the block data
                $mergedCoverage[$blockKey] = $fileCoverage[$blockKey].Clone()
            }
        }
        
        Write-Host "Successfully processed $inputFile with $($fileCoverage.Count) coverage blocks"
    }
    catch {
        Write-Error "Failed to process coverage file $inputFile : $_"
        exit 1
    }
}

if ($totalFiles -eq 0) {
    Write-Warning "No valid coverage files found. Creating empty coverage profile."
    try {
        @("mode: set") | Out-File -FilePath $OutputFile -Encoding UTF8
        Write-Host "Created empty coverage profile: $OutputFile"
    }
    catch {
        Write-Error "Failed to create empty coverage profile: $_"
        exit 1
    }
} else {
    Write-Host "Writing merged coverage profile to: $OutputFile"
    try {
        Write-CoverageProfile -Coverage $mergedCoverage -OutputPath $OutputFile
        
        # Display summary
        $totalBlocks = $mergedCoverage.Count
        $coveredBlocks = ($mergedCoverage.Values | Where-Object { $_.count -gt 0 }).Count
        $coveragePercentage = if ($totalBlocks -gt 0) { ($coveredBlocks / $totalBlocks) * 100 } else { 0 }
        
        Write-Host "Merge Summary:"
        Write-Host "  Input files processed: $totalFiles"
        Write-Host "  Total code blocks: $totalBlocks"
        Write-Host "  Covered blocks: $coveredBlocks"
        Write-Host "  Coverage: $($coveragePercentage.ToString('F2'))%"
    }
    catch {
        Write-Error "Failed to write merged coverage profile: $_"
        exit 1
    }
}

Write-Host "Coverage merge completed successfully!"
exit 0

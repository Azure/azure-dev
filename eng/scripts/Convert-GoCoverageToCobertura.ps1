#!/usr/bin/env pwsh

<#
.SYNOPSIS
    Converts Go coverage profile to Cobertura XML format for Azure DevOps.

.DESCRIPTION
    This script takes a Go coverage profile (cover.out) and converts it to Cobertura XML format
    that can be consumed by Azure DevOps for code coverage reporting. This replaces the need
    for external tools like gocov and gocov-xml.

.PARAMETER CoverageFile
    Path to the Go coverage profile file (typically cover.out)

.PARAMETER OutputFile
    Path where the Cobertura XML file should be written

.PARAMETER SourceRoot
    Root directory of the source code (used for relative path calculation)

.EXAMPLE
    ./Convert-GoCoverageToCobertura.ps1 -CoverageFile cover.out -OutputFile coverage.xml -SourceRoot .
#>

param(
    [Parameter(Mandatory = $true)]
    [string]$CoverageFile,
    
    [Parameter(Mandatory = $true)]
    [string]$OutputFile,
    
    [Parameter(Mandatory = $false)]
    [string]$SourceRoot = "."
)

$ErrorActionPreference = 'Stop'

function Get-RelativePath {
    param(
        [string]$Path,
        [string]$BasePath
    )
    
    $resolvedPath = Resolve-Path $Path -Relative
    return $resolvedPath.TrimStart('.', '\', '/')
}

function Normalize-GoFilePath {
    param(
        [string]$GoFilePath,
        [string]$SourceRoot
    )
    
    # Handle Go module paths like "github.com/azure/azure-dev/pkg/test/file.go"
    # Convert to relative paths like "pkg/test/file.go"
    
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
    $normalizedPath = $normalizedPath.Replace('\', '/')
    
    # Verify the file exists relative to source root
    $fullPath = Join-Path $SourceRoot $normalizedPath
    if (Test-Path $fullPath) {
        return $normalizedPath
    } else {
        # If the normalized path doesn't exist, try to find it relative to cli/azd
        $cliPath = $normalizedPath
        if ($normalizedPath.StartsWith("cli/azd/")) {
            $cliPath = $normalizedPath.Substring("cli/azd/".Length)
        }
        
        $cliFullPath = Join-Path $SourceRoot "cli/azd/$cliPath"
        if (Test-Path $cliFullPath) {
            return "cli/azd/$cliPath"
        }
    }
    
    # Return the normalized path even if file doesn't exist (for build artifacts)
    return $normalizedPath
}

function Parse-GoCoverageFile {
    param(
        [string]$FilePath,
        [string]$SourceRoot
    )
    
    $coverage = @{}
    $lines = Get-Content $FilePath
    
    # Skip the mode line (first line)
    for ($i = 1; $i -lt $lines.Count; $i++) {
        $line = $lines[$i].Trim()
        if ([string]::IsNullOrEmpty($line)) { continue }
        
        # Parse line format: file.go:startLine.startCol,endLine.endCol numStmt count
        if ($line -match '^(.+):(\d+)\.(\d+),(\d+)\.(\d+) (\d+) (\d+)$') {
            $originalFile = $matches[1]
            $startLine = [int]$matches[2]
            $endLine = [int]$matches[4]
            $numStmt = [int]$matches[6]
            $count = [int]$matches[7]
            
            # Normalize the file path to be relative to source root
            $file = Normalize-GoFilePath -GoFilePath $originalFile -SourceRoot $SourceRoot
            
            if (-not $coverage.ContainsKey($file)) {
                $coverage[$file] = @{}
            }
            
            # Mark lines as covered or uncovered
            for ($lineNum = $startLine; $lineNum -le $endLine; $lineNum++) {
                if (-not $coverage[$file].ContainsKey($lineNum)) {
                    $coverage[$file][$lineNum] = @{
                        hits = 0
                        statements = 0
                    }
                }
                $coverage[$file][$lineNum].hits += $count
                $coverage[$file][$lineNum].statements += $numStmt
            }
        }
    }
    
    return $coverage
}

function Generate-CoberturaXml {
    param(
        [hashtable]$Coverage,
        [string]$SourceRoot
    )
    
    $xml = [System.Xml.XmlDocument]::new()
    $declaration = $xml.CreateXmlDeclaration("1.0", "UTF-8", $null)
    $xml.AppendChild($declaration) | Out-Null
    
    # Create root coverage element
    $coverageElement = $xml.CreateElement("coverage")
    $coverageElement.SetAttribute("line-rate", "0.0")
    $coverageElement.SetAttribute("branch-rate", "0.0")
    $coverageElement.SetAttribute("lines-covered", "0")
    $coverageElement.SetAttribute("lines-valid", "0")
    $coverageElement.SetAttribute("branches-covered", "0")
    $coverageElement.SetAttribute("branches-valid", "0")
    $coverageElement.SetAttribute("complexity", "0.0")
    $coverageElement.SetAttribute("version", "1.0")
    $coverageElement.SetAttribute("timestamp", [DateTimeOffset]::Now.ToUnixTimeSeconds())
    $xml.AppendChild($coverageElement) | Out-Null
    
    # Create sources element
    $sourcesElement = $xml.CreateElement("sources")
    $sourceElement = $xml.CreateElement("source")
    $sourceElement.InnerText = $SourceRoot
    $sourcesElement.AppendChild($sourceElement) | Out-Null
    $coverageElement.AppendChild($sourcesElement) | Out-Null
    
    # Create packages element
    $packagesElement = $xml.CreateElement("packages")
    $coverageElement.AppendChild($packagesElement) | Out-Null
    
    $totalLinesCovered = 0
    $totalLinesValid = 0
    
    # Group files by package (directory)
    $packages = @{}
    foreach ($file in $Coverage.Keys) {
        $packageName = Split-Path $file -Parent
        if ([string]::IsNullOrEmpty($packageName)) {
            $packageName = "."
        }
        
        if (-not $packages.ContainsKey($packageName)) {
            $packages[$packageName] = @{}
        }
        $packages[$packageName][$file] = $Coverage[$file]
    }
    
    foreach ($packageName in $packages.Keys) {
        $packageElement = $xml.CreateElement("package")
        $packageElement.SetAttribute("name", $packageName)
        
        $packageLinesCovered = 0
        $packageLinesValid = 0
        
        # Create classes element for this package
        $classesElement = $xml.CreateElement("classes")
        $packageElement.AppendChild($classesElement) | Out-Null
        
        foreach ($file in $packages[$packageName].Keys) {
            $classElement = $xml.CreateElement("class")
            $fileName = Split-Path $file -Leaf
            $classElement.SetAttribute("name", $fileName)
            $classElement.SetAttribute("filename", $file)
            
            $fileLinesCovered = 0
            $fileLinesValid = 0
            
            # Create methods element (empty for Go files)
            $methodsElement = $xml.CreateElement("methods")
            $classElement.AppendChild($methodsElement) | Out-Null
            
            # Create lines element
            $linesElement = $xml.CreateElement("lines")
            $classElement.AppendChild($linesElement) | Out-Null
            
            foreach ($lineNum in ($packages[$packageName][$file].Keys | Sort-Object)) {
                $lineData = $packages[$packageName][$file][$lineNum]
                $lineElement = $xml.CreateElement("line")
                $lineElement.SetAttribute("number", $lineNum)
                $lineElement.SetAttribute("hits", $lineData.hits)
                $lineElement.SetAttribute("branch", "false")
                $linesElement.AppendChild($lineElement) | Out-Null
                
                $fileLinesValid++
                if ($lineData.hits -gt 0) {
                    $fileLinesCovered++
                }
            }
            
            $packageLinesCovered += $fileLinesCovered
            $packageLinesValid += $fileLinesValid
            
            # Set class coverage attributes
            $classLineRate = if ($fileLinesValid -gt 0) { $fileLinesCovered / $fileLinesValid } else { 0.0 }
            $classElement.SetAttribute("line-rate", $classLineRate.ToString("F4"))
            $classElement.SetAttribute("branch-rate", "0.0")
            $classElement.SetAttribute("complexity", "0.0")
            
            $classesElement.AppendChild($classElement) | Out-Null
        }
        
        $totalLinesCovered += $packageLinesCovered
        $totalLinesValid += $packageLinesValid
        
        # Set package coverage attributes
        $packageLineRate = if ($packageLinesValid -gt 0) { $packageLinesCovered / $packageLinesValid } else { 0.0 }
        $packageElement.SetAttribute("line-rate", $packageLineRate.ToString("F4"))
        $packageElement.SetAttribute("branch-rate", "0.0")
        $packageElement.SetAttribute("complexity", "0.0")
        
        $packagesElement.AppendChild($packageElement) | Out-Null
    }
    
    # Update overall coverage attributes
    $overallLineRate = if ($totalLinesValid -gt 0) { $totalLinesCovered / $totalLinesValid } else { 0.0 }
    $coverageElement.SetAttribute("line-rate", $overallLineRate.ToString("F4"))
    $coverageElement.SetAttribute("lines-covered", $totalLinesCovered)
    $coverageElement.SetAttribute("lines-valid", $totalLinesValid)
    
    return $xml
}

# Main execution
Write-Host "Converting Go coverage file '$CoverageFile' to Cobertura XML format..."

if (-not (Test-Path $CoverageFile)) {
    throw "Coverage file '$CoverageFile' not found"
}

# Parse the Go coverage file
$coverage = Parse-GoCoverageFile -FilePath $CoverageFile -SourceRoot $SourceRoot

# Generate Cobertura XML
$xml = Generate-CoberturaXml -Coverage $coverage -SourceRoot $SourceRoot

# Save the XML file
$outputPath = if (Test-Path $OutputFile -IsValid) { 
    if ([System.IO.Path]::IsPathRooted($OutputFile)) { 
        $OutputFile 
    } else { 
        Join-Path (Get-Location) $OutputFile 
    }
} else { 
    $OutputFile 
}
$xml.Save($outputPath)

Write-Host "Successfully converted coverage to Cobertura XML: '$OutputFile'"

# Display summary
$totalFiles = $coverage.Keys.Count
$totalLines = ($coverage.Values | ForEach-Object { $_.Keys.Count } | Measure-Object -Sum).Sum
$coveredLines = ($coverage.Values | ForEach-Object { 
    $_.Values | Where-Object { $_.hits -gt 0 } 
} | Measure-Object).Count

$coveragePercentage = if ($totalLines -gt 0) { ($coveredLines / $totalLines) * 100 } else { 0 }

Write-Host "Coverage Summary:"
Write-Host "  Files: $totalFiles"
Write-Host "  Lines: $coveredLines/$totalLines ($($coveragePercentage.ToString('F2'))%)"

<#
.SYNOPSIS
Parses a repoman generate results file, sets a variable that contains changed template repositories

.PARAMETER ResultsFile
Path to a repoman generate results file.

.PARAMETER OutputVariable
The output variable to set that contains the list of comma-separated templates
#>
param(
    [string]$ResultsFile,
    [string]$OutputVariable
)

if (-not (Test-Path -PathType Leaf $ResultsFile)) {
    Write-Host "No templates were changed."
    exit 0
}

$lines = Get-Content $ResultsFile
$templates = @()

foreach ($line in $lines) {
    if ($line -match "azd init -t (.+?) ") {
        $templateName = $Matches[1]
        $templates += $templateName
    }
}

if ($templates.Length -eq 0) {
    Write-Host "No templates were changed."
    exit 0
}

Write-Host "Following templates were changed:"

$templates | Format-List

$templatesCsv = $templates -join ","
Write-Host "##vso[task.setvariable variable=$OutputVariable;isOutput=true]$templatesCsv"
<#
.SYNOPSIS
Parses a repoman generate results file, sets a variable that contains changed template repositories

.PARAMETER ResultsFile
Path to a repoman generate results file.

.PARAMETER OutputTemplatesVariable
The output variable to set that contains the list of comma-separated templates

.PARAMETER OutputTemplateBranchVariable
The output variable to set that contains the template branch

#>
param(
    [string]$ResultsFile,
    [string]$OutputTemplatesVariable,
    [string]$OutputTemplateBranchVariable
)

if (-not (Test-Path -PathType Leaf $ResultsFile)) {
    Write-Host "No templates were changed."
    exit 0
}

$lines = Get-Content $ResultsFile
$templates = @()

foreach ($line in $lines) {
    if ($line -match "azd init -t (.+?) -b (.+?)(?:$|\s)") {
        $templateName = $Matches[1]
        $branchName = $Matches[2]
        $templates += $templateName
    }
}

if ($templates.Length -eq 0) {
    Write-Host "No templates were changed."
    exit 0
}

Write-Host "Following templates were changed on $($branchName):"

$templates | Format-List

$templatesCsv = $templates -join ","
Write-Host "##vso[task.setvariable variable=$OutputTemplatesVariable;isOutput=true]$templatesCsv"
Write-Host "##vso[task.setvariable variable=$OutputTemplateBranchVariable;isOutput=true]$branchName"
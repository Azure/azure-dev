<#
.SYNOPSIS
Generates a matrix of template test jobs.

.PARAMETER TemplateList
List of templates to run. By default, uses `azd template list` to run all templates.

.PARAMETER TemplateListFilter
Regex filter expression to filter templates. Examples: 'csharp', 'terraform', 'python-mongo'.

.PARAMETER JobVariablesDefinition
Job variables that will be set on each matrix job, in the format of a comma-delimited list of key=value.

.PARAMETER OutputMatrixVariable
The output variable that will contain the matrix job definitions.
The matrix job definition will contain:
- TemplateName - the name of the template being tested
- UseUpperCase - whether an upper-case version of the template name should be tested
- AzdContainerImage - the container image for the template test
- Any job variables defined in JobVariablesDefinition

#>
param (
    # This is a string and not a string[] to avoid issues with parameter passing in CI yaml.
    [string]$TemplateList = '(azd template list)',
    [string]$TemplateListFilter = '.*',
    [string]$OutputMatrixVariable = 'Matrix',
    [string]$JobVariablesDefinition = ''
)

function Get-JobVariables() {
    param(
        [string]$JobVariablesDefinition
    )
    $result = @{}
    $definitions = ($JobVariablesDefinition -split ',').Trim()
    if (-not $definitions) {
        return $result
    }

    foreach ($definition in $definitions) {
        $keyValue = ($definition -split '=').Trim()
        if ($keyValue.Length -ne 2) {
            throw "Invalid job variable definition: $definition"
        }

        $result[$keyValue[0]] = $keyValue[1]
    }

    return $result
}

$jobVariables = Get-JobVariables -JobVariablesDefinition $JobVariablesDefinition

$templateNames = @()

if ($TemplateList -eq '(azd template list)') {
    Write-Host "Using results of (azd template list --output json)"
    
    $templateNames += (azd template list --output json | ConvertFrom-Json).name
    if ($LASTEXITCODE -ne 0) {
        Write-Error "azd template list failed"
        exit 1
    }
} else {
    Write-Host "Using provided TemplateList value: $TemplateList"

    $templateNames += ($TemplateList -split ",").Trim()
}

if ($TemplateListFilter -ne '.*') {
    Write-Host "Filtering with TemplateListFilter regex: $TemplateListFilter"

    $templateNames = $templateNames -match $TemplateListFilter
}

$matrix = @{}
foreach ($template in $templateNames) {
    $jobName = $template.Replace('/', '_')
    $matrix[$jobName] = @{ TemplateName = $template }
}

# Adding extra test for capitals letters support. Using first template
$firstTemplate = $templateNames[0]
$capitalsTest = $firstTemplate.Replace('/', '_') + "-Upper-case-test"
$matrix[$capitalsTest] = @{ TemplateName = $firstTemplate; UseUpperCase = "true" }

foreach ($jobName in $matrix.Keys) {
    foreach ($key in $jobVariables.Keys) {
        $matrix[$jobName].Add($key, $jobVariables[$key]) | Out-Null
    }
}

Write-Host "Matrix:"
Write-Host ($matrix | ConvertTo-Json | Out-String)

$matrixJson = ConvertTo-Json $matrix -Depth 100 -Compress
Write-Host "##vso[task.setvariable variable=$OutputMatrixVariable;isOutput=true]$matrixJson"
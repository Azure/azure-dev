<#
.SYNOPSIS
Sets a matrix variable that contains all template test required variables.

.PARAMETER AzdVersion
The version of azd that the template tests will consume.

.PARAMETER AzdContainerImage
The container image the template tests will execute under (currently unused).

.PARAMETER AzureLocation
Azure location for templates to be deployed to.

.PARAMETER TemplateList
List of templates to run. By default, uses `azd template list` to run all templates.

.PARAMETER TemplateListFilter
Regex filter expression to filter templates. Examples: 'csharp', 'terraform', 'python-mongo'.

.PARAMETER TemplateBranchName
The template repository branch to test against.

.PARAMETER CleanupHoursDelay
The number of hours to delay cleanup.

.PARAMETER OutputMatrixVariable
The name of the variable that will contain the test matrix job definitions.

#>
param (
    [string]$AzdVersion = 'daily',
    [string]$AzdContainerImage = 'azdevcliextacr.azurecr.io/azure-dev:daily',
    [string]$AzureLocation = 'eastus2',
    [string[]]$TemplateList = $('(azd template list)'),
    [string]$TemplateListFilter = '.*',
    [string]$TemplateBranchName = 'main',
    [string]$CleanupHoursDelay = '0',

    [string]$OutputMatrixVariable = 'Matrix'
)

if ($TemplateList.Length -eq 1 -and ($TemplateList[0] -eq '(azd template list)')) {
    $templateNames = (azd template list --output json | ConvertFrom-Json).name
    if ($LASTEXITCODE -ne 0) {
        Write-Error "azd template list failed"
        exit 1
    }
} else {
    $templateNames = $TemplateList
}

if ($TemplateListFilter -ne '.*') {
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

foreach ($job in $matrix.Values) {
    $job.AzureLocation = $AzureLocation
    $job.TemplateBranchName = $TemplateBranchName

    $job.AzdContainerImage = $AzdContainerImage
    $job.AzdVersion = $AzdVersion
    $job.CleanupImmediate = $CleanupImmediate
    $job.CleanupHoursDelay = $CleanupHoursDelay
}

Write-Host "Matrix:"
Write-Host ($matrix | ConvertTo-Json | Out-String)

$matrixJson = ConvertTo-Json $matrix -Depth 100 -Compress
Write-Host "##vso[task.setvariable variable=$OutputMatrixVariable;isOutput=true]$matrixJson"
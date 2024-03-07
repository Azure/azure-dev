<#
.SYNOPSIS
Generates a matrix of template test jobs.

.PARAMETER TemplateList
List of templates to run. By default, uses `all` to run all templates.

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
    [string]$TemplateList = 'all',
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
        if ($keyValue.Length -eq 0) {
            continue
        }

        if ($keyValue.Length -ne 2) {
            throw "Invalid job variable definition: $definition"
        }

        $result[$keyValue[0]] = $keyValue[1]
    }

    return $result
}

function Copy-RandomJob([System.Collections.Hashtable]$JobMatrix) {
    $randIndex = Get-Random -Maximum $JobMatrix.Keys.Count #[0, Keys.Count]
    $i = 0
    $copyJob = @{}

    foreach ($jobName in $JobMatrix.Keys) {
        if ($i -eq $randIndex) {
            foreach ($jobProperty in $JobMatrix[$jobName].Keys) {
                $copyJob[$jobProperty] = $JobMatrix[$jobName][$jobProperty]
            }
        }
        $i++
    }

    return $copyJob
}

$JobVariablesDefinition = $JobVariablesDefinition.Trim()
$jobVariables = Get-JobVariables -JobVariablesDefinition $JobVariablesDefinition

$templateNames = @()

if ($TemplateList -eq 'all') {
    Write-Host "Running all templates "
    
    $officialTemplates = (azd template list --output json | ConvertFrom-Json).repositoryPath | ForEach-Object {
        if (!$_.StartsWith("Azure-Samples/")) {
            "Azure-Samples/" + $_
        }
        else {
            $_
        }
    }
    if ($LASTEXITCODE -ne 0) {
        Write-Error "azd template list failed"
        exit 1
    }

    # Other templates outside of `azd template list` can be added here.
    # To add a template, add the repository path {owner}/{repo} to the list below.
    $otherTemplates = @()

    $templateNames += $officialTemplates
    $templateNames += $otherTemplates
}
else {
    Write-Host "Using provided TemplateList value: $TemplateList"

    $templateNames += ($TemplateList -split ",").Trim()
}

if ($TemplateListFilter -ne '.*') {
    Write-Host "Filtering with TemplateListFilter regex: $TemplateListFilter"

    $templateNames = $templateNames -match $TemplateListFilter
}

if ($templateNames.Length -eq 0) {
    Write-Error "No matched templates found."
    exit 1
}

$matrix = @{}
foreach ($template in $templateNames) {
    $jobName = $template.Replace('/', '_')
    $matrix[$jobName] = @{ TemplateName = $template }
}

foreach ($jobName in $matrix.Keys) {
    foreach ($key in $jobVariables.Keys) {
        $matrix[$jobName].Add($key, $jobVariables[$key]) | Out-Null
    }
}

# When we only have a single template to test do not overload with APIM and UPPER case tests
if ($templateNames.Length -gt 1) {
    # Generated test cases from existing templates
    $upperTestCase = Copy-RandomJob -JobMatrix $matrix
    $upperTestCase.TEST_SCENARIO = 'UPPER' # Use UPPER case for env name
    $matrix[$upperTestCase.TemplateName.Replace('/', '_') + '-Upper-case-test'] = $upperTestCase

    if ($jobVariables.USE_APIM -ne 'true') {
        # If USE_APIM is specified, avoid creating a new job
        $apimEnabledTestCase = Copy-RandomJob -JobMatrix $matrix
        $apimEnabledTestCase.TEST_SCENARIO = 'apim'
        $apimEnabledTestCase.USE_APIM = 'true'
        $matrix[$apimEnabledTestCase.TemplateName.Replace('/', '_') + '-apim-enabled'] = $apimEnabledTestCase
    }
}

foreach ($jobName in $matrix.Keys) {
    $keyNames = @()
    $job = $matrix[$jobName]
    foreach ($key in $job.Keys) {
        $environmentVariableName = $key.ToUpper().Replace(".", "_")
        $keyNames += $environmentVariableName
    }

    # "%0A" is the URL-encoded value of \n
    # This escapes the newline and can be safely encoded in the JSON-string,
    # while Azure DevOps task runner will be able to decode the value into \n
    # which then can be used to import the list of variables we want.
    $job.VARIABLE_LIST = $keyNames -join "%0A"
}

Write-Host "Matrix:"
Write-Host ($matrix | ConvertTo-Json | Out-String)

$matrixJson = ConvertTo-Json $matrix -Depth 100 -Compress
Write-Host "##vso[task.setvariable variable=$OutputMatrixVariable;isOutput=true]$matrixJson"

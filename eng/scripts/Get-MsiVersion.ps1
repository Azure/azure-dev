param(
    [string] $CliVersion = $env:CLI_VERSION,
    [switch] $DevOpsOutput
)

. "$PSScriptRoot../../common/scripts/common.ps1"
. "$PSScriptRoot/azd-version-common.ps1"

Set-StrictMode -Version 4

$parsedVersion = getSemverParsedVersion $CliVersion

$patch = ($parsedVersion.Patch + 1) * 100
if ($parsedVersion.IsPrerelease -and $parsedVersion.HasValidPrereleaseLabel()) {
    $patch = $parsedVersion.Patch * 100 + $parsedVersion.PrereleaseNumber
}

$outputVersion = "$($parsedVersion.Major).$($parsedVersion.Minor).$patch"

if ($DevOpsOutput) {
    Write-Host "##vso[task.setvariable variable=MSI_VERSION]$outputVersion"
}

return $outputVersion

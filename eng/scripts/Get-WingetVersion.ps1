param(
    [string] $CliVersion = $env:CLI_VERSION,
    [switch] $DevOpsOutput
)

. "$PSScriptRoot../../common/scripts/common.ps1"
. "$PSScriptRoot/azd-version-common.ps1"

Set-StrictMode -Version 4

$parsedVersion = getSemverParsedVersion $CliVersion
$prereleaseNumber = 0
if ($parsedVersion.IsPrerelease -and $parsedVersion.HasValidPrereleaseLabel()) {
    $prereleaseNumber = $parsedVersion.PrereleaseNumber
}

$outputVersion = "$($parsedVersion.Major).$($parsedVersion.Minor).$($parsedVersion.Patch).$prereleaseNumber"

if ($DevOpsOutput) {
    Write-Host "##vso[task.setvariable variable=WinGetVersion]$outputVersion"
}

return $outputVersion

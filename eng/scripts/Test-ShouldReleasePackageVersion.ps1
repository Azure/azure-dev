param(
    [string] $CliVersion = $env:CLI_VERSION,
    [switch] $AllowPrerelease,
    [switch] $DevOpsOutput
)

. "$PSScriptRoot../../common/scripts/common.ps1"

Set-StrictMode -Version 4

$parsedVersion = [AzureEngSemanticVersion]::ParseVersionString($CliVersion)
if (!$parsedVersion) { 
    throw "Could not parse `"$CliVersion`""
}

$shouldRelease = $false 
if ($AllowPrerelease -and $parsedVersion.IsPrerelease) {
    $shouldRelease = $true
} elseif (!$parsedVersion.IsPrerelease) {
    $shouldRelease = $true
}

if ($DevOpsOutput) {
    Write-Host "##vso[task.setvariable variable=ShouldReleasePackage]$shouldRelease"
}

return $shouldRelease
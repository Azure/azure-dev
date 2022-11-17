<#

#>
param(
    [string] $BuildReason,
    [string] $BuildId
)

$PACKAGE_JSON_LOCATION = "$PSScriptRoot/../../ext/vscode/package.json"

$prereleaseCategory = ""

if ($BuildReason -eq "Manual") {
    Write-Host "Skipping release tagging for release build"
    exit 0
} elseif ($BuildReason -eq "PullRequest") {
    $prereleaseCategory = "pr"
} else {
    # This intentionally covers all scheduled/CI trigger definitions: IndividualCI, BatchedCI, Schedule.
    # For all other types such as ResourceTrigger, we default back to 'daily' as a sensible default.
    # See https://docs.microsoft.com/en-us/azure/devops/pipelines/build/variables?view=azure-devops&tabs=yaml#build-variables-devops-services for full list.
    $prereleaseCategory = "daily"
}

$prereleaseTag = ".$prereleaseCategory.$BuildId"

$packageJson = Get-Content -Raw $PACKAGE_JSON_LOCATION
$package = ConvertFrom-Json $packageJson
$package.version = "$($package.version)$prereleaseTag"
$outputContent = ConvertTo-Json $package -Depth 100
Set-Content -Path $PACKAGE_JSON_LOCATION -Value $outputContent

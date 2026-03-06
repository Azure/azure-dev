param(
  [string] $PreviousReleaseTag = (git describe --tags --match 'azure-dev-cli_*' --abbrev=0)
)

. "$PSScriptRoot../../common/scripts/common.ps1"

$previousVersionString = $PreviousReleaseTag.Substring("azure-dev-cli_".Length)
$previousVersion = [AzureEngSemanticVersion]::ParseVersionString($previousVersionString)
Write-Host "Previous release version: $previousVersion"

$commitDistance = git rev-list "$PreviousReleaseTag..HEAD" --count
Write-Host "Commit distance from last release: $commitDistance"

$version = "$($previousVersion.Major).$($previousVersion.Minor).$($previousVersion.Patch + $commitDistance)"
Write-Host "New dev version: $version"

return $version

<#
.PARAMETER CliVersionPath
Path to the CLI version file. This is the source of truth for the release version.

.PARAMETER ChangeLogPath
Path to the CLI changelog whose latest entry must match the release version and be ready to publish.

.PARAMETER AzdExtVersionPath
Path to the Go source file containing the azdext SDK version that must match the CLI release version.
#>
param(
    [string] $CliVersionPath = "$PSScriptRoot/../../cli/version.txt",
    [string] $ChangeLogPath = "$PSScriptRoot/../../cli/azd/CHANGELOG.md",
    [string] $AzdExtVersionPath = "$PSScriptRoot/../../cli/azd/pkg/azdext/version.go"
)

# Pull in the shared semantic-version and changelog helpers used by the SDK release pipelines.
. "$PSScriptRoot/../common/scripts/common.ps1"

Set-StrictMode -Version 4
$ErrorActionPreference = 'Stop'

$cliVersion = (Get-Content -Path $CliVersionPath -Raw).Trim()
if ($null -eq [AzureEngSemanticVersion]::ParseVersionString($cliVersion)) {
    throw "CLI version '$cliVersion' in '$CliVersionPath' is not valid semantic version metadata."
}

# The changelog parser preserves file order, so the first key is the release at the top of the file.
$changeLogEntries = Get-ChangeLogEntries -ChangeLogLocation $ChangeLogPath
$latestChangeLogVersion = $changeLogEntries.Keys | Select-Object -First 1
if ($latestChangeLogVersion -ne $cliVersion) {
    throw "CLI version '$cliVersion' does not match the latest changelog version '$latestChangeLogVersion'. " +
        "Run eng/scripts/Update-CliVersion.ps1 -NewVersion <version> before starting the release."
}

# Keep the changelog rules in one place. This checks the release date, content, and expected sections.
if (!(Confirm-ChangeLogEntry `
    -ChangeLogLocation $ChangeLogPath `
    -VersionString $cliVersion `
    -ForRelease $true)) {
    throw "Changelog entry for CLI version '$cliVersion' is not release-ready."
}

# The CLI and azdext SDK ship together; Update-CliVersion.ps1 is expected to update both versions.
$azdExtVersionContent = Get-Content -Path $AzdExtVersionPath -Raw
$azdExtVersionMatch = [regex]::Match($azdExtVersionContent, 'const Version = "(?<version>[^"]+)"')
if (!$azdExtVersionMatch.Success) {
    throw "Could not find the azdext SDK version in '$AzdExtVersionPath'."
}

$azdExtVersion = $azdExtVersionMatch.Groups['version'].Value
if ($azdExtVersion -ne $cliVersion) {
    throw "CLI version '$cliVersion' does not match azdext SDK version '$azdExtVersion'. " +
        "Run eng/scripts/Update-CliVersion.ps1 -NewVersion $cliVersion before starting the release."
}

Write-Host "Release metadata is ready for CLI version $cliVersion."

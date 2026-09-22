<#
.PARAMETER NewVersion
Version to set for the CLI release. When omitted, advances the current version to the next prerelease version.

.PARAMETER VersionTxtPath
Path to the CLI version file. Override this path when testing against temporary release metadata.

.PARAMETER ChangelogMdPath
Path to the CLI changelog updated with the release version and status.

.PARAMETER AzdExtVersionGoPath
Path to the Go source file containing the azdext SDK version that must stay in sync with the CLI version.
#>
param(
    [string] $NewVersion,
    [string] $VersionTxtPath = "$PSScriptRoot/../../cli/version.txt",
    [string] $ChangelogMdPath = "$PSScriptRoot/../../cli/azd/CHANGELOG.md",
    [string] $AzdExtVersionGoPath = "$PSScriptRoot/../../cli/azd/pkg/azdext/version.go"
)

. "$PSScriptRoot/../common/scripts/common.ps1"

Set-StrictMode -Version 4

function getVersion {
    $versionString = Get-Content $VersionTxtPath
    return [AzureEngSemanticVersion]::new($versionString)
}

$version = $NewVersion
$unreleased = $false
$replaceLatestEntryTitle = $true

if (!$version) {
    # Increment after release
    $version = getVersion

    if ($version.PrereleaseLabel -and $version.HasValidPrereleaseLabel()) {
        # 0.1.0-beta.1 -> 0.1.0-beta.2
        # 1.0.0-beta.1 -> 1.0.0-beta.2
        $version.PrereleaseNumber++
    } else {
        # Keep azd's release cadence independent of the shared SemVer type's special handling for 0.x versions.
        # 0.1.0 -> 0.2.0-beta.1
        # 1.0.0 -> 1.1.0-beta.1
        $version.Minor++
        $version.Patch = 0
        $version.PrereleaseLabel = 'beta'
        $version.PrereleaseNumber = 1
        $version.IsPrerelease = $true
    }

    $unreleased = $true
    $replaceLatestEntryTitle = $false
}

Set-Content -Path $VersionTxtPath -Value $version

# Also update the azdext SDK version to stay in sync with the CLI version
$azdExtVersionContent = Get-Content -Path $AzdExtVersionGoPath -Raw
$azdExtVersionContent -replace 'const Version = ".*?"', "const Version = `"$version`"" |
    Set-Content -Path $AzdExtVersionGoPath -Encoding utf8 -NoNewline

. "$PSScriptRoot/../common/scripts/Update-ChangeLog.ps1" `
    -Version $version.ToString() `
    -ChangeLogPath $ChangelogMdPath `
    -Unreleased $unreleased `
    -ReplaceLatestEntryTitle $replaceLatestEntryTitle

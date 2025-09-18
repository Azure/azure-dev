param(
    [string] $NewVersion
)

$CLI_VERSION_FILE = "$PSScriptRoot../../../cli/version.txt"
. "$PSScriptRoot../../common/scripts/common.ps1"

Set-StrictMode -Version 4

function getVersion {
    $versionString = Get-Content $CLI_VERSION_FILE
    return [AzureEngSemanticVersion]::new($versionString)
}

$version = $NewVersion
$unreleased = $false
$replaceLatestEntryTitle = $true

if (!$version) {
    # Increment after release
    $version = getVersion

    if ($version.IsPrerelease) {
        if ($version.HasValidPrereleaseLabel()) {
            # 0.1.0-beta.1 -> 0.1.0-beta.2
            # 1.0.0-beta.1 -> 1.0.0-beta.2
            $version.PrereleaseNumber++
        } else {
            # 0.1.0 -> 0.2.0-beta.1
            $version.Minor++
            $version.PrereleaseLabel = 'beta'
            $version.PrereleaseNumber = 1
        }
    } else {
        # 1.0.0 -> 1.1.0-beta.1
        $version.IncrementAndSetToPrerelease()
    }

    $unreleased = $true
    $replaceLatestEntryTitle = $false
}

Set-Content -Path $CLI_VERSION_FILE -Value $version

. "$PSScriptRoot/../common/scripts/Update-ChangeLog.ps1" `
    -Version $version.ToString() `
    -ChangeLogPath "$PSScriptRoot/../../cli/azd/CHANGELOG.md" `
    -Unreleased $unreleased `
    -ReplaceLatestEntryTitle $replaceLatestEntryTitle

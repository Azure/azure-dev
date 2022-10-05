param(
    [string] $NewVersion
)

$PACKAGE_JSON = "$PSScriptRoot../../../ext/vscode/package.json"
. "$PSScriptRoot../../common/scripts/common.ps1"

Set-StrictMode -Version 4

function getPackageJson {
    $packageJsonContent = Get-Content $PACKAGE_JSON -Raw
    $packageJson = ConvertFrom-Json $packageJsonContent
    return $packageJson
}

function getVersion {
    $versionString = (getPackageJson).version
    return [AzureEngSemanticVersion]::new($versionString)
}

function setVersion([string] $version) {
    $packageJson = getPackageJson
    $packageJson.version = $version

    $packageJsonString = ConvertTo-Json $packageJson -Depth 100
    Set-Content -Path $PACKAGE_JSON -Value $packageJsonString
}

function vscodeSemVerToString($version) {
    $output = "$($version.Major).$($version.Minor).$($version.Patch)"
    if ($version.IsPrerelease) {
        $output = "$output-$($version.PrereleaseLabel)"
    }

    return $output
}

$versionString = $NewVersion
$unreleased = $false
$replaceLatestEntryTitle = $true

if (!$versionString) {
    # Increment after release
    $currentVersion = getVersion

    if ($currentVersion.IsPrerelease) {
        # 0.1.0 -> 0.2.0-alpha
        $currentVersion.Minor++
        $currentVersion.PrereleaseLabel = 'alpha'
        $currentVersion.PrereleaseNumber = ''
    } else {
        # 1.0.0 -> 1.1.0-alpha
        $currentVersion.Minor++
        $currentVersion.PrereleaseLabel = 'alpha'
        $currentVersion.PrereleaseNumber = ''
        $currentVersion.IsPrerelease = $true
    }

    $unreleased = $true
    $replaceLatestEntryTitle = $false

    $versionString = vscodeSemVerToString $currentVersion
}

setVersion $versionString

. "$PSScriptRoot/../common/scripts/Update-ChangeLog.ps1" `
    -Version $versionString `
    -ChangeLogPath "$PSScriptRoot/../../ext/vscode/CHANGELOG.md" `
    -Unreleased $unreleased `
    -ReplaceLatestEntryTitle $replaceLatestEntryTitle

# This script returns a dev MSI version by finding the most recent release tag,
# then finding the first release tag that introduced the current minor version,
# and then calculating the "release distance" from that tag to the current
# commit.
#
# The MSI version is of the form: <major>.<minor>.<commit-distance-from-minor-intro-release>.
# This ensures a monotonically increasing version number for each build on the
# "main" branch.
#
# By default, the git history is assumed to be deepened by 6 months in the CI
# environment to ensure that a minor intro release is found.

. "$PSScriptRoot../../common/scripts/common.ps1"

Set-StrictMode -Version 4

$tagPrefix = 'azure-dev-cli_'

# Step 1: Most recent release tag
$mostRecentRelease = git describe --tags --match "${tagPrefix}*" --abbrev=0 HEAD
if ($LASTEXITCODE -ne 0 -or !$mostRecentRelease) {
    throw "Could not find a release tag matching '${tagPrefix}*'"
}

$mostRecentVersion = $mostRecentRelease.Substring($tagPrefix.Length)
$parsedMostRecent = [AzureEngSemanticVersion]::ParseVersionString($mostRecentVersion)
if (!$parsedMostRecent) {
    throw "Could not parse version from tag '$mostRecentRelease'"
}

Write-Host "Most recent release:    $mostRecentRelease (minor=$($parsedMostRecent.Minor))"

# Step 2: First release tag that introduced the current minor version
$currentRef = "$mostRecentRelease^"
$minorVersionRevRelease = $mostRecentRelease
while ($true) {
    $candidate = git describe --tags --match "${tagPrefix}*" --abbrev=0 $currentRef 2>$null
    if ($LASTEXITCODE -ne 0 -or !$candidate) {
      # The current tag introduces the minor version
      break
    }

    $versionCandidate = $candidate.Substring($tagPrefix.Length)
    $parsedCandidate = [AzureEngSemanticVersion]::ParseVersionString($versionCandidate)

    if (!$parsedCandidate) {
        throw "Could not parse version from tag '$candidate'"
    }

    if ($parsedCandidate.Minor -ne $parsedMostRecent.Minor) {
        break
    }

    $minorVersionRevRelease = $candidate
    $currentRef = "$candidate^"
}

Write-Host "Minor intro release:    $minorVersionRevRelease"

# Step 3: MSI version for minor rev release + commit distance
$minorRevVersion = $minorVersionRevRelease.Substring($tagPrefix.Length)
$msiVersionBase = & "$PSScriptRoot/Get-MsiVersion.ps1" -CliVersion $minorRevVersion

$commitDistance = [int](git rev-list --count "$minorVersionRevRelease..HEAD")

$parts = $msiVersionBase -split '\.'
$patch = [int]$parts[2] + $commitDistance

$devVersion = "$($parts[0]).$($parts[1]).$patch"

Write-Host "MSI version (base):     $msiVersionBase"
Write-Host "Commit distance:        $commitDistance"
Write-Host "Computed dev version:   $devVersion"

return $devVersion

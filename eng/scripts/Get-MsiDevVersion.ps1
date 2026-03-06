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

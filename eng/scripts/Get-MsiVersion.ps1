param(
    [string] $CliVersion = $env:CLI_VERSION
)

. "$PSScriptRoot../../common/scripts/common.ps1"

Set-StrictMode -Version 4

# Convert valid semver 
# 0.4.0-beta.2-pr.2021242 -> 0.4.0-beta.2
# 0.4.0-beta.2-daily.2026027 -> 0.4.0-beta.2
function getSemverParsedVersion($version) { 
    $parsedVersion = [AzureEngSemanticVersion]::ParseVersionString($version)
    if ($parsedVersion) {
        return $parsedVersion
    }

    # Splits by '-', joins the first two elements of the result
    # i.e. "1.2.3-beta.1-pr.123" -> "1.2.3-beta.1"
    $parsablePortion = ($version -split '-')[0,1] -join '-'

    $parsedVersion = [AzureEngSemanticVersion]::ParseVersionString($parsablePortion)
    if ($parsedVersion) { 
        return $parsedVersion
    }

    throw "Could not parse $version into valid format for creating an MSI version like 'x.y.z'"
}

$parsedVersion = getSemverParsedVersion $CliVersion

$patch = ($parsedVersion.Patch + 1) * 100
if ($parsedVersion.IsPrerelease) { 
    $patch = $parsedVersion.Patch * 100 + $parsedVersion.PrereleaseNumber
} 

return "$($parsedVersion.Major).$($parsedVersion.Minor).$patch"

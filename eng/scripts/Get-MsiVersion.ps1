param(
    [string] $CliVersion = $env:CLI_VERSION,
    [switch] $DevOpsOutput
)

. "$PSScriptRoot../../common/scripts/common.ps1"

Set-StrictMode -Version 4

function ensureValidParsedSemver($parsedVersion) { 
    if ($parsedVersion.IsPrerelease -and $parsedVersion.HasValidPrereleaseLabel()) {
        if ($parsedVersion.PrereleaseNumber -gt 99) { 
            throw "Version `"$($parsedVersion.ToString())`" is invalid. Prerelease number (e.g. '4' in '1.2.3-beta.4') must exist and be between 1 and 99"
        }
    } elseif ($parsedVersion.IsPrerelease) {
        # In the case of `1.2.3-beta` the prerelease number is `0` which will trip this condition
        if ($parsedVersion.PrereleaseNumber -lt 1) { 
            throw "Version `"$($parsedVersion.ToString())`" is invalid. Prerelease number (e.g. '4' in '1.2.3-beta.4') must exist and be between 1 and 99"
        }
    }
}

# Convert given semver to parseable semver
# 0.4.0-beta.2-pr.2021242 -> 0.4.0-beta.2
# 0.4.0-beta.2-daily.2026027 -> 0.4.0-beta.2
function getSemverParsedVersion($version) { 
    $parsedVersion = [AzureEngSemanticVersion]::ParseVersionString($version)
    if ($parsedVersion) {
        ensureValidParsedSemver $parsedVersion
        return $parsedVersion
    }

    # Splits by '-', joins the first two elements of the result
    # i.e. "1.2.3-beta.1-pr.123" -> "1.2.3-beta.1"
    $parsablePortion = ($version -split '-')[0,1] -join '-'

    $parsedVersion = [AzureEngSemanticVersion]::ParseVersionString($parsablePortion)
    if ($parsedVersion) { 
        ensureValidParsedSemver $parsedVersion
        return $parsedVersion
    }

    throw "Could not parse `"$version`" into valid format for creating an MSI version like 'x.y.z'"
}

$parsedVersion = getSemverParsedVersion $CliVersion

$patch = ($parsedVersion.Patch + 1) * 100
if ($parsedVersion.IsPrerelease -and $parsedVersion.HasValidPrereleaseLabel()) {
    $patch = $parsedVersion.Patch * 100 + $parsedVersion.PrereleaseNumber
}

$outputVersion = "$($parsedVersion.Major).$($parsedVersion.Minor).$patch"

if ($DevOpsOutput) {
    Write-Host "##vso[task.setvariable variable=MSI_VERSION]$outputVersion"
}

return $outputVersion

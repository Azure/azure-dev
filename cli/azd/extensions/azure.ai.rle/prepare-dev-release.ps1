<#
.SYNOPSIS
Builds RLE extension artifacts and updates the dedicated development registry.

.PARAMETER VersionBump
Increments the current semantic version by major, minor, or patch and preserves
its prerelease suffix. Updates version.txt and extension.yaml automatically.

.EXAMPLE
.\prepare-dev-release.ps1 -VersionBump patch

.EXAMPLE
.\prepare-dev-release.ps1 -VersionBump minor -BreakingChanges
#>
param(
    [string] $Version = (Get-Content "$PSScriptRoot/version.txt").Trim(),
    [ValidateSet("major", "minor", "patch")]
    [string] $VersionBump,
    [string] $Repository = "sujit-kamireddy/azure-dev",
    [string] $RepositoryBranch = "main",
    [string] $RegistryPath = (Join-Path $PSScriptRoot "..\registry.rle-dev.json"),
    [string] $OutputDirectory = (Join-Path $PSScriptRoot "artifacts\rle-dev"),
    [switch] $BreakingChanges
)

$ErrorActionPreference = "Stop"
$extensionId = "azure.ai.rle"
$versionPattern = "^\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?$"
$versionFilePath = Join-Path $PSScriptRoot "version.txt"
$manifestPath = Join-Path $PSScriptRoot "extension.yaml"
$currentVersion = (Get-Content -LiteralPath $versionFilePath -Raw).Trim()
$manifestVersionMatch = Select-String `
    -Path $manifestPath `
    -Pattern "^version:\s*(\S+)\s*$"

if (-not $manifestVersionMatch) {
    throw "Could not find the version in extension.yaml."
}

$manifestVersion = $manifestVersionMatch.Matches[0].Groups[1].Value

if ($VersionBump) {
    if ($PSBoundParameters.ContainsKey("Version")) {
        throw "Version and VersionBump cannot be specified together."
    }
    if ($currentVersion -notmatch $versionPattern) {
        throw "Version '$currentVersion' in version.txt is not a valid semantic version."
    }
    if ($manifestVersion -ne $currentVersion) {
        throw "Version '$currentVersion' in version.txt must match version '$manifestVersion' in extension.yaml."
    }

    $versionParts = [regex]::Match(
        $currentVersion,
        "^(?<major>\d+)\.(?<minor>\d+)\.(?<patch>\d+)(?<suffix>-[0-9A-Za-z.-]+)?$"
    )
    $major = [int64] $versionParts.Groups["major"].Value
    $minor = [int64] $versionParts.Groups["minor"].Value
    $patch = [int64] $versionParts.Groups["patch"].Value
    $suffix = $versionParts.Groups["suffix"].Value

    switch ($VersionBump) {
        "major" {
            $major++
            $minor = 0
            $patch = 0
        }
        "minor" {
            $minor++
            $patch = 0
        }
        "patch" {
            $patch++
        }
    }

    $Version = "$major.$minor.$patch$suffix"
    Set-Content -LiteralPath $versionFilePath -Value $Version -Encoding utf8NoBOM
    (Get-Content -LiteralPath $manifestPath -Raw) `
        -replace "(?m)^version:\s*\S+\s*$", "version: $Version" |
        Set-Content -LiteralPath $manifestPath -Encoding utf8NoBOM

    Write-Host "Version: $currentVersion -> $Version"
    $manifestVersion = $Version
}

if ($Version -notmatch $versionPattern) {
    throw "Version '$Version' is not a valid semantic version."
}

if (-not [Runtime.InteropServices.RuntimeInformation]::IsOSPlatform(
        [Runtime.InteropServices.OSPlatform]::Windows
    ) -or [Runtime.InteropServices.RuntimeInformation]::OSArchitecture -ne
        [Runtime.InteropServices.Architecture]::X64) {
    throw "This development release script currently supports only windows/amd64."
}

if ($manifestVersion -ne $Version) {
    throw "Version '$Version' must match the version in extension.yaml."
}

$resolvedOutputDirectory = Join-Path $OutputDirectory $Version
$buildDirectory = "bin"
$resolvedBuildDirectory = Join-Path $PSScriptRoot $buildDirectory
if (Test-Path $resolvedOutputDirectory) {
    Remove-Item -LiteralPath $resolvedOutputDirectory -Recurse -Force
}
if (Test-Path $resolvedBuildDirectory) {
    Remove-Item -LiteralPath $resolvedBuildDirectory -Recurse -Force
}
New-Item -ItemType Directory -Path $resolvedOutputDirectory | Out-Null
New-Item -ItemType Directory -Path $resolvedBuildDirectory | Out-Null

Push-Location $PSScriptRoot
try {
    azd x build --skip-install
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build RLE extension artifacts."
    }

    azd x pack --input $buildDirectory --output $resolvedOutputDirectory
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to package RLE extension artifacts."
    }

    $artifacts = @(
        Get-ChildItem -LiteralPath $resolvedOutputDirectory -File |
            Where-Object { $_.Name -match "\.(zip|tar\.gz)$" }
    )
    if ($artifacts.Count -ne 1 -or $artifacts[0].Name -ne "azure-ai-rle-windows-amd64.zip") {
        throw "Expected only azure-ai-rle-windows-amd64.zip."
    }

    $resolvedRegistryPath = [IO.Path]::GetFullPath($RegistryPath)
    if (-not (Test-Path $resolvedRegistryPath)) {
        $registryDirectory = Split-Path -Parent $resolvedRegistryPath
        New-Item -ItemType Directory -Path $registryDirectory -Force | Out-Null
        @{
            schemaVersion = "1.0"
            extensions = @()
        } |
            ConvertTo-Json -Depth 100 |
            Set-Content -LiteralPath $resolvedRegistryPath -Encoding utf8NoBOM
    }

    $existingBreakingChanges = @{}
    $existingRegistry = Get-Content -LiteralPath $resolvedRegistryPath -Raw | ConvertFrom-Json
    foreach ($existingExtension in @($existingRegistry.extensions)) {
        foreach ($existingVersion in @($existingExtension.versions)) {
            if ($null -ne $existingVersion.PSObject.Properties["breakingChanges"]) {
                $key = "$($existingExtension.id)|$($existingVersion.version)"
                $existingBreakingChanges[$key] = [bool]$existingVersion.breakingChanges
            }
        }
    }

    $artifactPattern = Join-Path $resolvedOutputDirectory "*.zip"

    azd x publish `
        --registry $resolvedRegistryPath `
        --artifacts $artifactPattern `
        --version $Version
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to update the RLE development registry."
    }
}
finally {
    Pop-Location
}

$registry = Get-Content -LiteralPath $resolvedRegistryPath -Raw | ConvertFrom-Json
foreach ($registryExtension in @($registry.extensions)) {
    foreach ($registryVersion in @($registryExtension.versions)) {
        $key = "$($registryExtension.id)|$($registryVersion.version)"
        if ($existingBreakingChanges.ContainsKey($key)) {
            $registryVersion | Add-Member `
                -NotePropertyName "breakingChanges" `
                -NotePropertyValue $existingBreakingChanges[$key] `
                -Force
        }
    }
}

$extension = @($registry.extensions | Where-Object { $_.id -eq $extensionId })
if ($extension.Count -ne 1) {
    throw "Expected one '$extensionId' entry in the registry, but found $($extension.Count)."
}

$versionEntry = @($extension[0].versions | Where-Object { $_.version -eq $Version })
if ($versionEntry.Count -ne 1) {
    throw "Expected one '$Version' entry in the registry, but found $($versionEntry.Count)."
}

if ($PSBoundParameters.ContainsKey("BreakingChanges")) {
    if ($BreakingChanges) {
        $versionEntry[0] | Add-Member `
            -NotePropertyName "breakingChanges" `
            -NotePropertyValue $true `
            -Force
    }
    else {
        $versionEntry[0].PSObject.Properties.Remove("breakingChanges")
    }
}

$repositoryRoot = (& git -C $PSScriptRoot rev-parse --show-toplevel).Trim()
if ($LASTEXITCODE -ne 0) {
    throw "Failed to locate the repository root."
}

$artifactPath = [IO.Path]::GetRelativePath(
    $repositoryRoot,
    [IO.Path]::GetFullPath($resolvedOutputDirectory)
).Replace("\", "/")
if ($artifactPath.StartsWith("../")) {
    throw "OutputDirectory must be inside the repository so its artifacts can be checked in."
}

$artifactBaseUrl = "https://raw.githubusercontent.com/$Repository/$RepositoryBranch/$artifactPath"
$artifactProperties = @($versionEntry[0].artifacts.PSObject.Properties)
if ($artifactProperties.Count -ne 1 -or $artifactProperties[0].Name -ne "windows/amd64") {
    throw "Expected only the windows/amd64 platform entry in the registry."
}

foreach ($artifactProperty in $artifactProperties) {
    $artifactName = Split-Path -Leaf $artifactProperty.Value.url
    $artifactProperty.Value.url = "$artifactBaseUrl/$artifactName"
}

$registry |
    ConvertTo-Json -Depth 100 |
    Set-Content -LiteralPath $resolvedRegistryPath -Encoding utf8NoBOM

Write-Host ""
Write-Host "RLE development release prepared."
Write-Host "Artifacts: $resolvedOutputDirectory"
Write-Host "Registry:  $resolvedRegistryPath"
if ($null -ne $versionEntry[0].PSObject.Properties["breakingChanges"]) {
    Write-Host "Breaking changes: $($versionEntry[0].breakingChanges)"
}
Write-Host ""
Write-Host "Include these files in the pull request:"
$artifacts | Sort-Object Name | ForEach-Object {
    Write-Host "  $($_.FullName)"
}

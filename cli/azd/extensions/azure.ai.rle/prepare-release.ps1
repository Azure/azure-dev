<#
.SYNOPSIS
Shared implementation for building and publishing an RLE release channel.

.PARAMETER Channel
Selects the internal rle-dev or customer rle-ext build channel.
The customer channel also prepares the same version for development.
Customer publication uses the public registry.rle.json distribution registry.
Packages are built once. Installed-source configuration selects the update
registry at runtime.

.PARAMETER VersionBump
Increments the current semantic version by major, minor, or patch and preserves
its prerelease suffix. Updates version.txt and extension.yaml automatically.

.EXAMPLE
.\prepare-release.ps1 -Channel rle-dev -VersionBump patch

.EXAMPLE
.\prepare-release.ps1 -Channel rle-ext -VersionBump patch -BreakingChanges
#>
param(
    [ValidateSet("major", "minor", "patch")]
    [string] $VersionBump = "patch",
    [switch] $BreakingChanges,
    [Parameter(Mandatory)]
    [ValidateSet("rle-dev", "rle-ext")]
    [string] $Channel,
    [string] $PublicRepositoryPath
)

$ErrorActionPreference = "Stop"

function Set-ContentUtf8NoBom {
    param(
        [string] $Path,
        [string] $Value
    )

    $encoding = New-Object -TypeName System.Text.UTF8Encoding -ArgumentList $false
    [System.IO.File]::WriteAllText($Path, "$Value$([Environment]::NewLine)", $encoding)
}

$extensionId = "azure.ai.rle"
$artifactPrefix = "azure-ai-rle"
$developmentRepository = & (Join-Path $PSScriptRoot "get-development-repository.ps1")
$outputDirectory = Join-Path $PSScriptRoot "artifacts\rle-dev"
$targets = @(@{
    channel = "rle-dev"
    description = "internal development"
    path = Join-Path $PSScriptRoot "..\registry.rle-dev.json"
    baseURL = "https://raw.githubusercontent.com/$developmentRepository/main"
    artifactRoot = "cli/azd/extensions/azure.ai.rle/artifacts/rle-dev"
})
if ($Channel -eq "rle-ext") {
    if (-not $PublicRepositoryPath) {
        throw "Use prepare-ext-release.ps1 to prepare releases in a separate public distribution checkout."
    }
    $targets += @{
        channel = "rle-ext"
        description = "external customer"
        path = Join-Path $PublicRepositoryPath "cli\azd\extensions\registry.rle.json"
        baseURL = "https://raw.githubusercontent.com/Azure/azure-dev/rle-releases"
        artifactRoot = "cli/azd/extensions/azure.ai.rle/artifacts"
    }
}
# Both channels ship the same platform matrix as build.ps1 and build.sh.
# azd x pack archives linux artifacts as .tar.gz and every other platform as .zip.
$expectedPlatforms = [ordered]@{
    "windows/amd64" = "$artifactPrefix-windows-amd64.zip"
    "windows/arm64" = "$artifactPrefix-windows-arm64.zip"
    "darwin/amd64"  = "$artifactPrefix-darwin-amd64.zip"
    "darwin/arm64"  = "$artifactPrefix-darwin-arm64.zip"
    "linux/amd64"   = "$artifactPrefix-linux-amd64.tar.gz"
    "linux/arm64"   = "$artifactPrefix-linux-arm64.tar.gz"
}
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

$version = "$major.$minor.$patch$suffix"

if ($Channel -eq "rle-ext") {
    $publicArtifactPath = Join-Path $PublicRepositoryPath "cli\azd\extensions\azure.ai.rle\artifacts\$version"
    if (Test-Path -LiteralPath $publicArtifactPath) {
        throw "Public artifacts for '$version' already exist; published versions must not be overwritten."
    }
    if (Test-Path -LiteralPath $targets[1].path) {
        $publicRegistry = Get-Content -LiteralPath $targets[1].path -Raw | ConvertFrom-Json
        $publishedVersion = @($publicRegistry.extensions | Where-Object id -eq $extensionId).versions |
            Where-Object version -eq $version
        if ($publishedVersion) {
            throw "External version '$version' is already registered."
        }
    }
}

if (-not (Get-Command go -ErrorAction SilentlyContinue)) {
    throw "Go is required to cross-compile the RLE extension for every supported platform."
}

$resolvedOutputDirectory = Join-Path $outputDirectory $version
$buildDirectory = "bin"
$resolvedBuildDirectory = Join-Path $PSScriptRoot $buildDirectory

Set-ContentUtf8NoBom -Path $versionFilePath -Value $version
# Get-Content -Raw keeps the file's trailing newline and the writer adds one of
# its own, so trim before writing to stop blank lines accruing at every bump.
$manifestContent = (Get-Content -LiteralPath $manifestPath -Raw) `
    -replace "(?m)^version:\s*\S+\s*$", "version: $version"
Set-ContentUtf8NoBom -Path $manifestPath -Value $manifestContent.TrimEnd()
Write-Host "Version: $currentVersion -> $version"

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
    azd x build --all --skip-install
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
    $artifactNames = @($artifacts | ForEach-Object { $_.Name } | Sort-Object)
    $expectedArtifactNames = @($expectedPlatforms.Values | Sort-Object)
    $missingArtifacts = @($expectedArtifactNames | Where-Object { $artifactNames -notcontains $_ })
    if ($missingArtifacts.Count -gt 0) {
        throw "Missing packaged artifacts: $($missingArtifacts -join ', ')."
    }
    $unexpectedArtifacts = @($artifactNames | Where-Object { $expectedArtifactNames -notcontains $_ })
    if ($unexpectedArtifacts.Count -gt 0) {
        throw "Unexpected packaged artifacts: $($unexpectedArtifacts -join ', ')."
    }

}
finally {
    Pop-Location
}

foreach ($target in $targets) {
    $resolvedRegistryPath = [IO.Path]::GetFullPath($target.path)
    $channelDescription = $target.description
    $artifactBaseUrl = "$($target.baseURL)/$($target.artifactRoot)/$version"
    $targetArtifactDirectory = $resolvedOutputDirectory
    if ($target.channel -eq "rle-ext") {
        $targetArtifactDirectory = $publicArtifactPath
        foreach ($file in $artifacts) {
            if ($file.Length -ge 100MB) {
                throw "Artifact '$($file.Name)' exceeds GitHub's Git file-size limit."
            }
        }
        New-Item -ItemType Directory -Path $targetArtifactDirectory -Force | Out-Null
        foreach ($file in $artifacts) {
            Copy-Item -LiteralPath $file.FullName -Destination $targetArtifactDirectory
        }
        foreach ($file in $artifacts) {
            $copiedFile = Join-Path $targetArtifactDirectory $file.Name
            if (-not (Test-Path -LiteralPath $copiedFile -PathType Leaf)) {
                throw "Copied artifact '$($file.Name)' is missing from '$targetArtifactDirectory'. Local build output was retained."
            }
            if ((Get-FileHash -LiteralPath $file.FullName -Algorithm SHA256).Hash -ne
                (Get-FileHash -LiteralPath $copiedFile -Algorithm SHA256).Hash) {
                throw "Copied artifact '$($file.Name)' failed checksum verification. Local build output was retained."
            }
        }
    }
    if (-not (Test-Path $resolvedRegistryPath)) {
        $registryDirectory = Split-Path -Parent $resolvedRegistryPath
        New-Item -ItemType Directory -Path $registryDirectory -Force | Out-Null
        $emptyRegistry = @{
            schemaVersion = "1.0"
            extensions = @()
        } | ConvertTo-Json -Depth 100
        Set-ContentUtf8NoBom -Path $resolvedRegistryPath -Value $emptyRegistry
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

    $artifactPattern = @(
        (Join-Path $resolvedOutputDirectory "*.zip"),
        (Join-Path $resolvedOutputDirectory "*.tar.gz")
    ) -join ","

    Push-Location $PSScriptRoot
    try {
        azd x publish `
            --registry $resolvedRegistryPath `
            --artifacts $artifactPattern `
            --version $version
        if ($LASTEXITCODE -ne 0) {
            throw "Failed to update the RLE $channelDescription registry."
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

    $versionEntry = @($extension[0].versions | Where-Object { $_.version -eq $version })
    if ($versionEntry.Count -ne 1) {
        throw "Expected one '$version' entry in the registry, but found $($versionEntry.Count)."
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

    $artifactProperties = @($versionEntry[0].artifacts.PSObject.Properties)
    $publishedPlatforms = @($artifactProperties | ForEach-Object { $_.Name } | Sort-Object)
    $expectedPlatformNames = @($expectedPlatforms.Keys | Sort-Object)
    $missingPlatforms = @($expectedPlatformNames | Where-Object { $publishedPlatforms -notcontains $_ })
    if ($missingPlatforms.Count -gt 0) {
        throw "Registry is missing platform entries: $($missingPlatforms -join ', ')."
    }
    $unexpectedPlatforms = @($publishedPlatforms | Where-Object { $expectedPlatformNames -notcontains $_ })
    if ($unexpectedPlatforms.Count -gt 0) {
        throw "Registry has unexpected platform entries: $($unexpectedPlatforms -join ', ')."
    }

    foreach ($artifactProperty in $artifactProperties) {
        $artifactName = Split-Path -Leaf $artifactProperty.Value.url
        $artifactProperty.Value.url = "$artifactBaseUrl/$artifactName"
    }

    $registryContent = $registry | ConvertTo-Json -Depth 100
    Set-ContentUtf8NoBom -Path $resolvedRegistryPath -Value $registryContent

    Write-Host ""
    Write-Host "RLE $channelDescription release prepared."
    Write-Host "Channel:   $($target.channel)"
    Write-Host "Artifacts: $targetArtifactDirectory"
    Write-Host "Registry:  $resolvedRegistryPath"
    if ($null -ne $versionEntry[0].PSObject.Properties["breakingChanges"]) {
        Write-Host "Breaking changes: $($versionEntry[0].breakingChanges)"
    }
}

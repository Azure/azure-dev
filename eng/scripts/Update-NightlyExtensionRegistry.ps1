<#
.SYNOPSIS
    Normalizes a nightly extension registry entry after `azd x publish --artifacts`.

.DESCRIPTION
    `azd x publish --artifacts` records the local file path of each artifact as its
    registry URL because local files have no remote URL. This script rewrites those
    URLs to the public Azure Storage location for the extension's always-latest
    nightly folder, and prunes the extension to only the current nightly version
    (the nightly storage folder is overwritten in place each run, so older registry
    entries would point at replaced blobs with mismatched checksums).

    Only the named extension is modified; other extensions in the registry are left
    untouched so concurrent nightly pipelines preserve each other's entries.

.PARAMETER RegistryPath
    Path to the registry.nightly.json file to update.

.PARAMETER ExtensionId
    The extension id (e.g. azure.ai.agents) that was just published.

.PARAMETER Version
    The nightly version that was just published (e.g. 1.2.3-nightly.98765).

.PARAMETER SanitizedExtensionId
    The dash-form extension id used in storage paths (e.g. azure-ai-agents).

.PARAMETER StaticHost
    The static web host base URL (e.g. https://azuresdkartifacts.z5.web.core.windows.net).
#>
param(
    [Parameter(Mandatory)][string] $RegistryPath,
    [Parameter(Mandatory)][string] $ExtensionId,
    [Parameter(Mandatory)][string] $Version,
    [Parameter(Mandatory)][string] $SanitizedExtensionId,
    [Parameter(Mandatory)][string] $StaticHost
)

Set-StrictMode -Version Latest
$ErrorActionPreference = 'Stop'

$registry = Get-Content -Path $RegistryPath -Raw | ConvertFrom-Json

$ext = $registry.extensions | Where-Object { $_.id -eq $ExtensionId }
if (-not $ext) {
    throw "Extension '$ExtensionId' not found in '$RegistryPath' after publish."
}

$versionEntry = $ext.versions | Where-Object { $_.version -eq $Version }
if (-not $versionEntry) {
    throw "Version '$Version' not found for extension '$ExtensionId' after publish."
}

# Trim a trailing slash from the host so the joined URL is well-formed.
$baseUrl = "$($StaticHost.TrimEnd('/'))/azd/extensions/$SanitizedExtensionId/nightly"

# Rewrite each artifact's local file path to its public storage URL. The file
# name is preserved so it matches what was uploaded to the nightly folder.
foreach ($prop in $versionEntry.artifacts.PSObject.Properties) {
    $artifact = $prop.Value
    $fileName = Split-Path -Path $artifact.url -Leaf
    $artifact.url = "$baseUrl/$fileName"
    Write-Host "  $($prop.Name) -> $($artifact.url)"
}

# Prune to only the current nightly version (storage is overwritten in place).
$ext.versions = @($versionEntry)

# 100 is deep enough for the nested artifacts -> os/arch -> metadata structure.
$json = $registry | ConvertTo-Json -Depth 100
Set-Content -Path $RegistryPath -Value $json

Write-Host "Updated nightly registry entry for '$ExtensionId' ($Version)."

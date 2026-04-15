<#
.SYNOPSIS
  Generates a per-extension registry entry JSON file.

.DESCRIPTION
  1. Computes checksums from signed release artifacts
  2. Reads extension.yaml for metadata (id, namespace, displayName, etc.)
  3. Loads the JSON template, replaces placeholders with actual values
  4. Writes the entry as a JSON file to the specified output path

  The script only produces the JSON file. Upload to storage is handled
  separately by the pipeline.

.PARAMETER SanitizedExtensionId
  Hyphenated extension id (e.g. azure-ai-agents)

.PARAMETER AzdExtensionId
  Dotted extension id (e.g. azure.ai.agents)

.PARAMETER Version
  Extension version from version.txt

.PARAMETER StorageBaseUrl
  Static storage host URL for daily artifacts

.PARAMETER OutputPath
  Local file path to write the registry entry JSON

.PARAMETER ReleasePath
  Path to the signed release artifacts

.PARAMETER ExtensionYamlPath
  Path to the extension.yaml file

.PARAMETER TemplatePath
  Path to the extension-registry-daily-template.json
#>

param(
    [Parameter(Mandatory)] [string] $SanitizedExtensionId,
    [Parameter(Mandatory)] [string] $AzdExtensionId,
    [Parameter(Mandatory)] [string] $Version,
    [Parameter(Mandatory)] [string] $StorageBaseUrl,
    [Parameter(Mandatory)] [string] $OutputPath,
    [Parameter(Mandatory)] [string] $TemplatePath,
    [string] $ReleasePath = "release",
    [Parameter(Mandatory)] [string] $ExtensionYamlPath
)

$ErrorActionPreference = 'Stop'

# Validate required files exist
$extYamlPath = $ExtensionYamlPath
if (!(Test-Path $extYamlPath)) {
    Write-Error "extension.yaml not found at $extYamlPath"
    exit 1
}
if (!(Test-Path $TemplatePath)) {
    Write-Error "Template not found at $TemplatePath"
    exit 1
}

# Compute checksums from signed artifacts
$checksums = @{}
$missingArtifacts = @()
$artifactFiles = @(
    @{ key = "DARWIN_AMD64";  file = "$SanitizedExtensionId-darwin-amd64.zip" },
    @{ key = "DARWIN_ARM64";  file = "$SanitizedExtensionId-darwin-arm64.zip" },
    @{ key = "LINUX_AMD64";   file = "$SanitizedExtensionId-linux-amd64.tar.gz" },
    @{ key = "LINUX_ARM64";   file = "$SanitizedExtensionId-linux-arm64.tar.gz" },
    @{ key = "WINDOWS_AMD64"; file = "$SanitizedExtensionId-windows-amd64.zip" },
    @{ key = "WINDOWS_ARM64"; file = "$SanitizedExtensionId-windows-arm64.zip" }
)

foreach ($artifact in $artifactFiles) {
    $filePath = Join-Path $ReleasePath $artifact.file
    if (Test-Path $filePath) {
        try {
            $hash = (Get-FileHash -Path $filePath -Algorithm SHA256).Hash.ToLower()
            $checksums[$artifact.key] = $hash
            Write-Host "Checksum $($artifact.key): $hash"
        } catch {
            Write-Error "Failed to compute checksum for ${filePath}: $_"
            exit 1
        }
    } else {
        $missingArtifacts += $artifact.file
    }
}

if ($missingArtifacts.Count -gt 0) {
    Write-Error "Missing release artifacts: $($missingArtifacts -join ', ')"
    exit 1
}

# Install powershell-yaml for proper YAML parsing
$psModuleHelpers = Join-Path $PSScriptRoot "../common/scripts/Helpers/PSModule-Helpers.ps1"
if (!(Test-Path $psModuleHelpers)) {
    $psModuleHelpers = Join-Path $PSScriptRoot "PSModule-Helpers.ps1"
}
if (!(Test-Path $psModuleHelpers)) {
    Write-Error "PSModule-Helpers.ps1 not found near $PSScriptRoot"
    exit 1
}
. $psModuleHelpers
Install-ModuleIfNotInstalled "powershell-yaml" "0.4.7" | Import-Module

# Parse extension.yaml
$extData = ConvertFrom-Yaml (Get-Content $extYamlPath -Raw)
if ($null -eq $extData) {
    Write-Error "Failed to parse extension.yaml at $extYamlPath — file may be empty or malformed"
    exit 1
}

$extMeta = @{}
foreach ($key in @('namespace', 'displayName', 'description', 'usage', 'requiredAzdVersion')) {
    if ($extData.ContainsKey($key)) {
        $extMeta[$key] = $extData[$key]
    }
}

$capabilities = if ($extData.ContainsKey('capabilities')) { @($extData['capabilities']) } else { @() }

$providers = @()
if ($extData.ContainsKey('providers')) {
    foreach ($p in $extData['providers']) {
        $provider = [ordered]@{}
        foreach ($k in $p.Keys) { $provider[$k] = $p[$k] }
        $providers += $provider
    }
}

# Validate required fields were parsed
$requiredFields = @('namespace', 'displayName', 'description', 'usage')
foreach ($field in $requiredFields) {
    if (-not $extMeta[$field] -or $extMeta[$field] -eq '') {
        Write-Error "Required field '$field' missing or empty in extension.yaml"
        exit 1
    }
}

Write-Host "Parsed extension metadata:"
Write-Host "  namespace: $($extMeta.namespace)"
Write-Host "  displayName: $($extMeta.displayName)"
Write-Host "  description: $($extMeta.description)"
Write-Host "  usage: $($extMeta.usage)"
Write-Host "  capabilities: $($capabilities -join ', ')"
Write-Host "  providers: $($providers.Count)"

# Load template and replace placeholders
# JSON-escape string values that are inserted into JSON string literals.
# This prevents characters like " \ and control chars from producing invalid JSON.
function ConvertTo-JsonSafeString([string]$value) {
    # Use ConvertTo-Json to get a properly escaped JSON string, then strip the surrounding quotes
    $escaped = $value | ConvertTo-Json
    return $escaped.Substring(1, $escaped.Length - 2)
}

$template = Get-Content $TemplatePath -Raw
$replacements = @{
    '${EXT_VERSION}'            = ConvertTo-JsonSafeString $Version
    '${REQUIRED_AZD_VERSION}'   = ConvertTo-JsonSafeString ($(if ($extMeta.requiredAzdVersion) { $extMeta.requiredAzdVersion } else { "" }))
    '${USAGE}'                  = ConvertTo-JsonSafeString $extMeta.usage
    '${SANITIZED_ID}'           = ConvertTo-JsonSafeString $SanitizedExtensionId
    '${STORAGE_BASE_URL}'       = ConvertTo-JsonSafeString $StorageBaseUrl
    '${CHECKSUM_DARWIN_AMD64}'  = $checksums["DARWIN_AMD64"]
    '${CHECKSUM_DARWIN_ARM64}'  = $checksums["DARWIN_ARM64"]
    '${CHECKSUM_LINUX_AMD64}'   = $checksums["LINUX_AMD64"]
    '${CHECKSUM_LINUX_ARM64}'   = $checksums["LINUX_ARM64"]
    '${CHECKSUM_WINDOWS_AMD64}' = $checksums["WINDOWS_AMD64"]
    '${CHECKSUM_WINDOWS_ARM64}' = $checksums["WINDOWS_ARM64"]
}

foreach ($placeholder in $replacements.Keys) {
    $template = $template.Replace($placeholder, $replacements[$placeholder])
}

# Verify all placeholders were replaced
if ($template -match '\$\{[A-Za-z0-9_]+\}') {
    Write-Error "Unreplaced placeholder found: $($matches[0])"
    exit 1
}

$versionEntry = $template | ConvertFrom-Json

# Add capabilities and providers (can't template arrays/objects easily)
$versionEntry.capabilities = $capabilities
if ($providers.Count -gt 0) {
    $versionEntry | Add-Member -NotePropertyName "providers" -NotePropertyValue $providers
}

# Build the per-extension entry wrapped in registry format
# azd ext source add -t url expects { "extensions": [...] }
$extEntry = [ordered]@{
    id          = $AzdExtensionId
    namespace   = $extMeta.namespace
    displayName = $extMeta.displayName
    description = $extMeta.description
    versions    = @($versionEntry)
}

$registry = [ordered]@{ extensions = @($extEntry) }

# Write registry entry and validate JSON
$outputDir = Split-Path $OutputPath -Parent
if ($outputDir -and !(Test-Path $outputDir)) {
    New-Item -ItemType Directory -Path $outputDir -Force | Out-Null
}
$registry | ConvertTo-Json -Depth 20 | Set-Content $OutputPath -Encoding utf8

try {
    $null = Get-Content $OutputPath -Raw | ConvertFrom-Json -Depth 20
} catch {
    Write-Error "Generated entry JSON is invalid: $_"
    exit 1
}

Write-Host "Registry entry written to $OutputPath"
Get-Content $OutputPath

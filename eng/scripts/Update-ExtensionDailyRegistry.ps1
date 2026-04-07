<#
.SYNOPSIS
  Writes a per-extension daily registry entry to Azure Storage.

.DESCRIPTION
  1. Computes checksums from signed release artifacts
  2. Reads extension.yaml for metadata (id, namespace, displayName, etc.)
  3. Loads the JSON template, replaces placeholders with actual values
  4. Uploads the entry as a standalone per-extension JSON blob

  Each extension writes its own file to avoid race conditions when multiple
  extension pipelines run concurrently. A separate unification script
  (Build-UnifiedDailyRegistry.ps1) combines all per-extension entries into
  the final registry-daily.json.

.PARAMETER SanitizedExtensionId
  Hyphenated extension id (e.g. azure-ai-agents)

.PARAMETER AzdExtensionId
  Dotted extension id (e.g. azure.ai.agents)

.PARAMETER Version
  Extension version from version.txt

.PARAMETER StorageBaseUrl
  Static storage host URL for daily artifacts

.PARAMETER RegistryEntryBlobPath
  Full blob path for the per-extension entry JSON
  (e.g. .../azd/extensions/daily-registry-entries/azure.ai.agents.json)

.PARAMETER ReleasePath
  Path to the signed release artifacts

.PARAMETER MetadataPath
  Path to the release-metadata directory containing extension.yaml

.PARAMETER TemplatePath
  Path to the extension-registry-daily-template.json
#>

param(
    [Parameter(Mandatory)] [string] $SanitizedExtensionId,
    [Parameter(Mandatory)] [string] $AzdExtensionId,
    [Parameter(Mandatory)] [string] $Version,
    [Parameter(Mandatory)] [string] $StorageBaseUrl,
    [Parameter(Mandatory)] [string] $RegistryEntryBlobPath,
    [Parameter(Mandatory)] [string] $TemplatePath,
    [string] $ReleasePath = "release",
    [string] $MetadataPath = "release-metadata"
)

$ErrorActionPreference = 'Stop'

# Validate required files exist
$extYamlPath = Join-Path $MetadataPath "extension.yaml"
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

# Read extension.yaml for metadata.
# Uses simple line-by-line regex parsing — handles top-level scalar fields,
# capabilities list, and providers list. This is intentionally not a full YAML
# parser. It works for the known extension.yaml schema where all values are
# single-line scalars or simple lists. If extension.yaml grows multi-line
# values or complex nesting, switch to powershell-yaml.
$extYaml = Get-Content $extYamlPath -Raw
$extMeta = @{}
foreach ($line in $extYaml -split "`n") {
    if ($line -match "^(\w[\w\-]*):\s*(.+)$") {
        $extMeta[$matches[1]] = $matches[2].Trim().Trim('"')
    }
}

# Parse capabilities list
$capabilities = @()
$inCapabilities = $false
foreach ($line in $extYaml -split "`n") {
    if ($line -match "^capabilities:") { $inCapabilities = $true; continue }
    if ($inCapabilities -and $line -match "^\s+-\s+(.+)$") {
        $capabilities += $matches[1].Trim()
    } elseif ($inCapabilities -and $line -match "^\S") {
        break
    }
}

# Parse providers list
$providers = @()
$inProviders = $false
$currentProvider = $null
foreach ($line in $extYaml -split "`n") {
    if ($line -match "^providers:") { $inProviders = $true; continue }
    if ($inProviders -and $line -match "^\s+-\s+name:\s*(.+)$") {
        if ($currentProvider) { $providers += $currentProvider }
        $currentProvider = [ordered]@{ name = $matches[1].Trim() }
    } elseif ($inProviders -and $currentProvider -and $line -match "^\s+(\w+):\s*(.+)$") {
        $currentProvider[$matches[1].Trim()] = $matches[2].Trim()
    } elseif ($inProviders -and $line -match "^\S") {
        break
    }
}
if ($currentProvider) { $providers += $currentProvider }

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
$template = Get-Content $TemplatePath -Raw
$replacements = @{
    '${EXT_VERSION}'            = $Version
    '${REQUIRED_AZD_VERSION}'   = if ($extMeta.requiredAzdVersion) { $extMeta.requiredAzdVersion } else { "" }
    '${USAGE}'                  = $extMeta.usage
    '${SANITIZED_ID}'           = $SanitizedExtensionId
    '${STORAGE_BASE_URL}'       = $StorageBaseUrl
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

$versionEntry = $template | ConvertFrom-Json

# Add capabilities and providers (can't template arrays/objects easily)
$versionEntry.capabilities = $capabilities
if ($providers.Count -gt 0) {
    $versionEntry | Add-Member -NotePropertyName "providers" -NotePropertyValue $providers
}

# Build the per-extension entry
$extEntry = [ordered]@{
    id          = $AzdExtensionId
    namespace   = $extMeta.namespace
    displayName = $extMeta.displayName
    description = $extMeta.description
    versions    = @($versionEntry)
}

# Write per-extension entry and validate JSON
$entryFile = "$AzdExtensionId.json"
$extEntry | ConvertTo-Json -Depth 20 | Set-Content $entryFile -Encoding utf8

try {
    $null = Get-Content $entryFile -Raw | ConvertFrom-Json -Depth 20
} catch {
    Write-Error "Generated entry JSON is invalid: $_"
    exit 1
}

Write-Host "Extension entry:"
Get-Content $entryFile

# Upload per-extension entry to storage
azcopy copy $entryFile $RegistryEntryBlobPath --overwrite=true
if ($LASTEXITCODE -ne 0) {
    Write-Error "Failed to upload entry to $RegistryEntryBlobPath (exit code $LASTEXITCODE)"
    exit 1
}

Write-Host "Entry uploaded to $RegistryEntryBlobPath"

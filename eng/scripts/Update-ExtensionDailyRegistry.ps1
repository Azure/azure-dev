<#
.SYNOPSIS
  Updates registry-daily.json on Azure Storage with the current extension's daily build entry.

.DESCRIPTION
  1. Computes checksums from signed release artifacts
  2. Reads extension.yaml for metadata (id, namespace, displayName, etc.)
  3. Loads the JSON template, replaces placeholders with actual values
  4. Downloads existing registry-daily.json from storage (or creates empty)
  5. Merges the new entry and uploads back

.PARAMETER SanitizedExtensionId
  Hyphenated extension id (e.g. azure-ai-agents)

.PARAMETER AzdExtensionId
  Dotted extension id (e.g. azure.ai.agents)

.PARAMETER Version
  Extension version from version.txt

.PARAMETER StorageBaseUrl
  Static storage host URL for daily artifacts

.PARAMETER RegistryBlobPath
  Full blob path for registry-daily.json

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
    [Parameter(Mandatory)] [string] $RegistryBlobPath,
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

# Read extension.yaml for metadata
# Simple line-by-line parsing for top-level scalar fields, capabilities list,
# and providers list. Sufficient for the known extension.yaml schema.
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
$requiredFields = @('namespace', 'displayName', 'description')
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
Write-Host "  capabilities: $($capabilities -join ', ')"
Write-Host "  providers: $($providers.Count)"

# Load template and replace placeholders
$template = Get-Content $TemplatePath -Raw
$replacements = @{
    '${EXT_VERSION}'            = $Version
    '${REQUIRED_AZD_VERSION}'   = if ($extMeta.requiredAzdVersion) { $extMeta.requiredAzdVersion } else { "" }
    '${USAGE}'                  = if ($extMeta.usage) { $extMeta.usage } else { "" }
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

# Build the full extension entry
$extEntry = [ordered]@{
    id          = $AzdExtensionId
    namespace   = $extMeta.namespace
    displayName = $extMeta.displayName
    description = $extMeta.description
    versions    = @($versionEntry)
}

# Download existing registry or create empty
# Use ErrorActionPreference Continue for azcopy since "not found" is expected on first run
$registryFile = "registry-daily.json"
$prevErrorPref = $ErrorActionPreference
$ErrorActionPreference = 'Continue'
$azcopyOutput = azcopy copy $RegistryBlobPath $registryFile 2>&1
$azcopyExitCode = $LASTEXITCODE
$ErrorActionPreference = $prevErrorPref

if ($azcopyExitCode -ne 0) {
    # Check if this is a "not found" (expected) vs an actual error
    $outputStr = $azcopyOutput | Out-String
    if ($outputStr -match "BlobNotFound|404|does not exist") {
        Write-Host "No existing registry found, creating new one"
    } else {
        Write-Warning "azcopy download failed (exit code $azcopyExitCode), creating new registry"
        Write-Warning $outputStr
    }
    [ordered]@{ extensions = @() } | ConvertTo-Json -Depth 10 | Set-Content $registryFile
}

$registry = Get-Content $registryFile -Raw | ConvertFrom-Json -Depth 20

# Merge: replace existing extension entry or add new
$found = $false
for ($i = 0; $i -lt $registry.extensions.Count; $i++) {
    if ($registry.extensions[$i].id -eq $AzdExtensionId) {
        $registry.extensions[$i] = $extEntry
        $found = $true
        Write-Host "Updated existing entry for $AzdExtensionId"
        break
    }
}
if (-not $found) {
    $registry.extensions += $extEntry
    Write-Host "Added new entry for $AzdExtensionId"
}

# Write registry and validate JSON round-trip before uploading
$registryJson = $registry | ConvertTo-Json -Depth 20
$registryJson | Set-Content $registryFile -Encoding utf8

# Validate the output is valid JSON
try {
    $null = Get-Content $registryFile -Raw | ConvertFrom-Json -Depth 20
} catch {
    Write-Error "Generated registry JSON is invalid: $_"
    exit 1
}

Write-Host "Registry contents:"
Get-Content $registryFile

# Upload to storage
azcopy copy $registryFile $RegistryBlobPath --overwrite=true
if ($LASTEXITCODE -ne 0) {
    Write-Error "Failed to upload registry to $RegistryBlobPath (exit code $LASTEXITCODE)"
    exit 1
}

Write-Host "Registry uploaded to $RegistryBlobPath"

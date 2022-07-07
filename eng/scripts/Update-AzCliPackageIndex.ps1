<#
.SYNOPSIS
Updates a given Azure CLI index.json with the latest entry for a given ExtensionName

.DESCRIPTION
Adds a new version of ExtensionName to a given index.json file. Optional WhlUrl
can be used to update the upload URL if not final (e.g. using GitHub releases)

Given an original index.json file (generally from
https://github.com/Azure/azure-cli-extensions/blob/main/src/index.json) add the
latest version (first element) specified in the `NewIndexJson` for a given
extension.

The `OriginalIndexJson` location may be the main index.json hosted by the
Azure CLI or it may be an index.json tracking private releases.

.PARAMETER OriginalIndexJson
Location of the original index.json file

.PARAMETER NewIndexJson
Location of an updated index.json file with outputs from a run of
`azdev extension publish --update-index`.

.PARAMETER WhlUrl
Specify a new URL for the WHL file. Optional. By default no changes are made.

If the file is going to be published in a different location (e.g. a GitHub
release) then one can run `azdev extension publish --update-index` with a
"temporary" storage account URL. The whl file will be published and index will
be updated with accurate metadata except the download location. Specifying this
parameter updates the download URL to use the given `WhlUrl`.

.PARAMETER ExtensionName
The name of the extension to update. Default is 'azure-dev'
#>

param(
    [string] $OriginalIndexJson,
    [string] $NewIndexJson,
    [string] $WhlUrl,
    [string] $ExtensionName = 'azure-dev'
)

$newIndexContent = Get-Content $NewIndexJson -Raw
$newIndex = ConvertFrom-Json $newIndexContent -AsHashtable

if (!$newIndex.extensions.$ExtensionName) {
    Write-Error "No entry for $ExtensionName in new index.json."
    exit 1
}

if ($WhlUrl) {
    Write-Host "Setting downloadUrl to $WhlUrl"
    $newIndex.extensions.$ExtensionName[0].downloadUrl = $WhlUrl
} else {
    Write-Host "Download URL not specified. No changes to download URL"
}

if (!(Test-Path $OriginalIndexJson)) {
    Write-Host "No original index.json, copying $NewIndexJson directly"
    $outputContent = ConvertTo-Json $newIndex -Depth 100
    Set-Content -Path $OriginalIndexJson -Value $outputContent
    exit 0
}

Write-Host "Adding entry to original index"
$newEntry = $newIndex.extensions.$ExtensionName[0]

$originalIndexContent = Get-Content $OriginalIndexJson -Raw
$originalIndex = ConvertFrom-Json $originalIndexContent -AsHashtable

if ($originalIndex.extensions.$ExtensionName) {
    $originalIndex.extensions.$ExtensionName = @($newEntry) + $originalIndex.extensions.$ExtensionName
} else {
    $originalIndex.extensions.$ExtensionName = @($newEntry)
}

$outputContent = ConvertTo-Json $originalIndex -Depth 100
Set-Content -Path $OriginalIndexJson -Value $outputContent

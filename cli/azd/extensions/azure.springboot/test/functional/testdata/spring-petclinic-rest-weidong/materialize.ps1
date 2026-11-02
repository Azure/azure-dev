# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

# Downloads source only. Never invokes template scripts, builds or Azure commands.
[CmdletBinding()]
param(
    [Parameter(Mandatory)]
    [string] $Destination
)

$ErrorActionPreference = 'Stop'
$manifest = Get-Content -LiteralPath (Join-Path $PSScriptRoot 'source-revision.json') -Raw |
    ConvertFrom-Json -AsHashtable
if ($manifest.repository -ne 'https://github.com/weidongxu-microsoft/petclinic-rest-azd' -or
    $manifest.commit -ne 'd8f601e5d3cdbd640069ffde58fc1370541fb0f4' -or
    $manifest.hashAlgorithm -ne 'git-blob-sha1' -or
    $manifest.overlays.Count -ne 1 -or $manifest.overlays[0] -ne 'azure.yaml') {
    throw 'Unexpected pinned template manifest.'
}
$root = [IO.Path]::GetFullPath($Destination)
if (Test-Path -LiteralPath $root) {
    if (-not (Test-Path -LiteralPath $root -PathType Container) -or
        @(Get-ChildItem -LiteralPath $root -Force).Count -ne 0) {
        throw 'Destination must be a new or empty directory.'
    }
}
New-Item -ItemType Directory -Force -Path $root | Out-Null
$download = Join-Path $root '.pinned-source-download'
New-Item -ItemType Directory -Path $download | Out-Null
try {
    $archive = Join-Path $download 'source.zip'
    $url = "https://codeload.github.com/weidongxu-microsoft/petclinic-rest-azd/zip/$($manifest.commit)"
    Invoke-WebRequest -Uri $url -OutFile $archive -TimeoutSec 120
    $expanded = Join-Path $download 'expanded'
    Expand-Archive -LiteralPath $archive -DestinationPath $expanded
    $directories = @(Get-ChildItem -LiteralPath $expanded -Directory)
    if ($directories.Count -ne 1) { throw 'Unexpected archive layout.' }
    $source = $directories[0].FullName
    $sourceFiles = @(Get-ChildItem -LiteralPath $source -Recurse -Force -File)
    if ($sourceFiles.Count -ne $manifest.files.Count) { throw 'Pinned source inventory mismatch.' }
    foreach ($file in $sourceFiles) {
        $relative = [IO.Path]::GetRelativePath($source, $file.FullName).Replace('\', '/')
        if (-not $manifest.files.ContainsKey($relative)) { throw 'Unexpected pinned source file.' }
        $bytes = [IO.File]::ReadAllBytes($file.FullName)
        $header = [Text.Encoding]::UTF8.GetBytes("blob $($bytes.Length)`0")
        $blob = [byte[]]::new($header.Length + $bytes.Length)
        [Array]::Copy($header, $blob, $header.Length)
        [Array]::Copy($bytes, 0, $blob, $header.Length, $bytes.Length)
        $actual = [Convert]::ToHexString([Security.Cryptography.SHA1]::HashData($blob)).ToLowerInvariant()
        if ($actual -cne $manifest.files[$relative]) {
            # Archives can apply .gitattributes export line endings. Recover exact Git blob bytes.
            $rawUrl = "https://raw.githubusercontent.com/weidongxu-microsoft/petclinic-rest-azd/$($manifest.commit)/$relative"
            Invoke-WebRequest -Uri $rawUrl -OutFile $file.FullName -TimeoutSec 120
            $bytes = [IO.File]::ReadAllBytes($file.FullName)
            $header = [Text.Encoding]::UTF8.GetBytes("blob $($bytes.Length)`0")
            $blob = [byte[]]::new($header.Length + $bytes.Length)
            [Array]::Copy($header, $blob, $header.Length)
            [Array]::Copy($bytes, 0, $blob, $header.Length, $bytes.Length)
            $actual = [Convert]::ToHexString([Security.Cryptography.SHA1]::HashData($blob)).ToLowerInvariant()
            if ($actual -cne $manifest.files[$relative]) { throw "Pinned source hash mismatch: $relative" }
        }
    }
    foreach ($item in Get-ChildItem -LiteralPath $source -Force) {
        Move-Item -LiteralPath $item.FullName -Destination $root
    }
    Copy-Item -LiteralPath (Join-Path $PSScriptRoot 'azure.yaml') -Destination (Join-Path $root 'azure.yaml')
    Write-Output "Materialized pinned Petclinic REST source at $root; only azure.yaml is overlaid."
} finally {
    Remove-Item -LiteralPath $download -Recurse -Force
}

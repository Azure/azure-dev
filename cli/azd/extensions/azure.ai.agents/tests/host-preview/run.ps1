# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

$ErrorActionPreference = 'Stop'
$extension = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$core = (Resolve-Path (Join-Path $extension '..\..')).Path
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("azd-foundry-preview-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $temp | Out-Null
$binary = Join-Path $temp 'preview-tests.exe'
$overlay = Join-Path $temp 'overlay.json'
$oldWork = $env:GOWORK
$oldToolchain = $env:GOTOOLCHAIN
$oldBinary = $env:AZD_PREVIEW_EXTENSION_TEST_BINARY
try {
    $env:GOWORK = 'off'
    $env:GOTOOLCHAIN = 'go1.26.4'
    Push-Location $extension
    try {
        go test -c -o $binary ./internal/project
        if ($LASTEXITCODE -ne 0) { throw 'Building preview extension test helper failed.' }
    } finally { Pop-Location }

    $replacement = @{}
    $replacement[(Join-Path $core 'internal\grpcserver\foundry_preview_extension_test.go')] =
        (Join-Path $PSScriptRoot 'host_preview_test.go.txt')
    @{ Replace = $replacement } | ConvertTo-Json -Depth 4 | Set-Content -Path $overlay
    $env:AZD_PREVIEW_EXTENSION_TEST_BINARY = $binary
    Push-Location $core
    try {
        go test -overlay $overlay ./internal/grpcserver -run '^TestFoundryExtensionPreviewAgainstMainHost$' -count=1
        if ($LASTEXITCODE -ne 0) { throw 'Preview integration against unchanged main host failed.' }
    } finally { Pop-Location }
} finally {
    $env:GOWORK = $oldWork
    $env:GOTOOLCHAIN = $oldToolchain
    $env:AZD_PREVIEW_EXTENSION_TEST_BINARY = $oldBinary
    foreach ($file in @($binary, $overlay)) {
        if (Test-Path -LiteralPath $file) { Remove-Item -LiteralPath $file }
    }
    Remove-Item -LiteralPath $temp
}

# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

$ErrorActionPreference = 'Stop'
$extension = (Resolve-Path (Join-Path $PSScriptRoot '..\..')).Path
$projects = (Resolve-Path (Join-Path $extension '..\azure.ai.projects')).Path
$core = (Resolve-Path (Join-Path $extension '..\..')).Path
$temp = Join-Path ([System.IO.Path]::GetTempPath()) ("azd-foundry-preview-" + [guid]::NewGuid())
New-Item -ItemType Directory -Path $temp | Out-Null
$binary = Join-Path $temp 'preview-tests.exe'
$projectBinary = Join-Path $temp 'project-preview-tests.exe'
$projectResult = Join-Path $temp 'project-preview-result.json'
$overlay = Join-Path $temp 'overlay.json'
$oldWork = $env:GOWORK
$oldToolchain = $env:GOTOOLCHAIN
$oldBinary = $env:AZD_PREVIEW_EXTENSION_TEST_BINARY
$oldProjectBinary = $env:AZD_PROJECT_PREVIEW_EXTENSION_TEST_BINARY
$oldProjectResult = $env:AZD_PROJECT_PREVIEW_TEST_RESULT
try {
    $env:GOWORK = 'off'
    $env:GOTOOLCHAIN = 'go1.26.4'
    Push-Location $extension
    try {
        go test -c -o $binary ./internal/project
        if ($LASTEXITCODE -ne 0) { throw 'Building preview extension test helper failed.' }
    } finally { Pop-Location }

    Push-Location $projects
    try {
        go test -c -o $projectBinary ./internal/cmd
        if ($LASTEXITCODE -ne 0) { throw 'Building project preview extension test helper failed.' }
    } finally { Pop-Location }

    $replacement = @{}
    $replacement[(Join-Path $core 'internal\grpcserver\foundry_preview_extension_test.go')] =
        (Join-Path $PSScriptRoot 'host_preview_test.go.txt')
    $replacement[(Join-Path $core 'internal\cmd\foundry_project_preview_test.go')] =
        (Join-Path $PSScriptRoot 'project_command_preview_test.go.txt')
    @{ Replace = $replacement } | ConvertTo-Json -Depth 4 | Set-Content -Path $overlay
    $env:AZD_PREVIEW_EXTENSION_TEST_BINARY = $binary
    $env:AZD_PROJECT_PREVIEW_EXTENSION_TEST_BINARY = $projectBinary
    $env:AZD_PROJECT_PREVIEW_TEST_RESULT = $projectResult
    Push-Location $core
    try {
        go test -overlay $overlay ./internal/grpcserver -run '^TestFoundry(Extension|Project)PreviewAgainstMainHost$' -count=1
        if ($LASTEXITCODE -ne 0) { throw 'Preview integration against unchanged main host failed.' }
        go test -overlay $overlay ./internal/cmd -run '^TestFoundryProjectPreviewCommandOutput$' -count=1
        if ($LASTEXITCODE -ne 0) { throw 'Project preview output against unchanged main command failed.' }
    } finally { Pop-Location }
} finally {
    $env:GOWORK = $oldWork
    $env:GOTOOLCHAIN = $oldToolchain
    $env:AZD_PREVIEW_EXTENSION_TEST_BINARY = $oldBinary
    $env:AZD_PROJECT_PREVIEW_EXTENSION_TEST_BINARY = $oldProjectBinary
    $env:AZD_PROJECT_PREVIEW_TEST_RESULT = $oldProjectResult
    foreach ($file in @($binary, $projectBinary, $projectResult, $overlay)) {
        if (Test-Path -LiteralPath $file) { Remove-Item -LiteralPath $file }
    }
    Remove-Item -LiteralPath $temp
}

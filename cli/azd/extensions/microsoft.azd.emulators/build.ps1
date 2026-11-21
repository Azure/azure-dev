# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

$ErrorActionPreference = 'Stop'
Push-Location $PSScriptRoot

try {
    $outputDir = if ($env:OUTPUT_DIR) { $env:OUTPUT_DIR } else { Join-Path $PSScriptRoot 'bin' }
    $extensionId = if ($env:EXTENSION_ID) { $env:EXTENSION_ID } else { 'microsoft.azd.emulators' }
    $binaryName = $extensionId -replace '\.', '-'
    New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

    $platforms = @('windows/amd64', 'windows/arm64', 'darwin/amd64', 'darwin/arm64', 'linux/amd64', 'linux/arm64')
    if ($env:EXTENSION_PLATFORM) {
        $platforms = @($env:EXTENSION_PLATFORM)
    }

    foreach ($platform in $platforms) {
        $os, $arch = $platform -split '/'
        $output = Join-Path $outputDir "$binaryName-$os-$arch"
        if ($os -eq 'windows') {
            $output += '.exe'
        }
        Write-Host "Building $platform..."
        $env:CGO_ENABLED = '0'
        $env:GOOS = $os
        $env:GOARCH = $arch
        go build -trimpath -o $output .
        if ($LASTEXITCODE -ne 0) {
            throw "Build failed for $platform."
        }
    }
}
finally {
    Pop-Location
}

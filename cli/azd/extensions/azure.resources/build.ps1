$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

$outputDirectory = if ($env:OUTPUT_DIR) { $env:OUTPUT_DIR } else { Join-Path $PSScriptRoot 'bin' }
$version = if ($env:EXTENSION_VERSION) { $env:EXTENSION_VERSION } else { (Get-Content 'version.txt').Trim() }
New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
$platforms = if ($env:EXTENSION_PLATFORM) {
    @($env:EXTENSION_PLATFORM)
} else {
    @('windows/amd64', 'windows/arm64', 'darwin/amd64', 'darwin/arm64', 'linux/amd64', 'linux/arm64')
}

$originalGoOS = $env:GOOS
$originalGoArch = $env:GOARCH
try {
    foreach ($platform in $platforms) {
        $env:GOOS, $env:GOARCH = $platform -split '/'
        $outputName = Join-Path $outputDirectory "azure-resources-${env:GOOS}-${env:GOARCH}"
        if ($env:GOOS -eq 'windows') {
            $outputName += '.exe'
        }
        go build "-ldflags=-X azure.resources/internal/version.Version=$version" -o $outputName .
        if ($LASTEXITCODE) {
            exit $LASTEXITCODE
        }
    }
}
finally {
    $env:GOOS = $originalGoOS
    $env:GOARCH = $originalGoArch
}

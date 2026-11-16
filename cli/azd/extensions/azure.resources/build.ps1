$ErrorActionPreference = 'Stop'
Set-Location $PSScriptRoot

$outputDirectory = if ($env:OUTPUT_DIR) { $env:OUTPUT_DIR } else { Join-Path $PSScriptRoot 'bin' }
$version = if ($env:EXTENSION_VERSION) { $env:EXTENSION_VERSION } else { (Get-Content 'version.txt').Trim() }
New-Item -ItemType Directory -Path $outputDirectory -Force | Out-Null
$commit = git rev-parse HEAD
if ($LASTEXITCODE) {
    throw "Resolving the source revision failed with exit code $LASTEXITCODE."
}
$buildDate = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")
$versionPath = 'azure.resources/internal/version'
$ldFlags = "-X '$versionPath.Version=$version' -X '$versionPath.Commit=$commit' -X '$versionPath.BuildDate=$buildDate'"

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
        go build "-ldflags=$ldFlags" -o $outputName .
        if ($LASTEXITCODE) {
            exit $LASTEXITCODE
        }
    }
}
finally {
    $env:GOOS = $originalGoOS
    $env:GOARCH = $originalGoArch
}

$ErrorActionPreference = 'Stop'

$extensionDir = Split-Path -Parent $MyInvocation.MyCommand.Path
Set-Location -Path $extensionDir

$extensionIdSafe = $env:EXTENSION_ID -replace '\.', '-'
$outputDir = if ($env:OUTPUT_DIR) { $env:OUTPUT_DIR } else { Join-Path $extensionDir 'bin' }
New-Item -ItemType Directory -Force -Path $outputDir | Out-Null

$platforms = if ($env:EXTENSION_PLATFORM) {
    @($env:EXTENSION_PLATFORM)
} else {
    @(
        'windows/amd64',
        'windows/arm64',
        'darwin/amd64',
        'darwin/arm64',
        'linux/amd64',
        'linux/arm64'
    )
}

$ldflags = "-X 'azurespringboot/internal/version.Version=$env:EXTENSION_VERSION'"
if ($env:POSTGRES_BOOTSTRAP_IMAGE) {
    if ($env:POSTGRES_BOOTSTRAP_IMAGE -notmatch
        '^mcr\.microsoft\.com/azure-dev/azd-spring-postgres-bootstrap@sha256:[0-9a-f]{64}$') {
        throw 'POSTGRES_BOOTSTRAP_IMAGE must be the digest-pinned Microsoft-owned MCR image.'
    }
    $ldflags += " -X 'azurespringboot/internal/spring.postgresBootstrapImage=$env:POSTGRES_BOOTSTRAP_IMAGE'"
}

foreach ($platform in $platforms) {
    $targetOS, $targetArch = $platform -split '/'
    $outputName = Join-Path $outputDir "$extensionIdSafe-$targetOS-$targetArch"
    if ($targetOS -eq 'windows') {
        $outputName += '.exe'
    }

    $env:GOOS = $targetOS
    $env:GOARCH = $targetArch
    go build `
        "-ldflags=$ldflags" `
        -o $outputName `
        .
    if ($LASTEXITCODE -ne 0) {
        throw "Failed to build azure.springboot for $platform"
    }
}

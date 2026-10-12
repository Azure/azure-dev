# Ensure script fails on any error
$ErrorActionPreference = 'Stop'

# Get the directory of the script
$EXTENSION_DIR = Split-Path -Parent $MyInvocation.MyCommand.Path

# Change to the script directory
Set-Location -Path $EXTENSION_DIR

# Create a safe version of EXTENSION_ID replacing dots with dashes
$EXTENSION_ID_SAFE = $env:EXTENSION_ID -replace '\.', '-'

# Define output directory
$OUTPUT_DIR = if ($env:OUTPUT_DIR) { $env:OUTPUT_DIR } else { Join-Path $EXTENSION_DIR "bin" }

# Create output directory if it doesn't exist
if (-not (Test-Path -Path $OUTPUT_DIR)) {
    New-Item -ItemType Directory -Path $OUTPUT_DIR | Out-Null
}

# Get Git commit hash and build date
$COMMIT = git rev-parse HEAD
if ($LASTEXITCODE -ne 0) {
    Write-Host "Error: Failed to get git commit hash"
    exit 1
}
$BUILD_DATE = (Get-Date).ToUniversalTime().ToString("yyyy-MM-ddTHH:mm:ssZ")

# List of OS and architecture combinations
if ($env:EXTENSION_PLATFORM) {
    $PLATFORMS = @($env:EXTENSION_PLATFORM)
}
else {
    $PLATFORMS = @(
        "windows/amd64",
        "windows/arm64",
        "darwin/amd64",
        "darwin/arm64",
        "linux/amd64",
        "linux/arm64"
    )
}

$APP_PATH = "$env:EXTENSION_ID/internal/cmd"
$REGISTRY_URL = if ($env:RLE_REGISTRY_URL) {
    $env:RLE_REGISTRY_URL
}
else {
    "https://raw.githubusercontent.com/sujit-kamireddy/azure-dev/main/cli/azd/extensions/registry.rle-dev.json"
}
$REGISTRY_SOURCE = if ($env:RLE_REGISTRY_SOURCE) { $env:RLE_REGISTRY_SOURCE } else { "rle-dev" }

# Loop through platforms and build
foreach ($PLATFORM in $PLATFORMS) {
    $OS, $ARCH = $PLATFORM -split '/'

    $OUTPUT_NAME = Join-Path $OUTPUT_DIR "$EXTENSION_ID_SAFE-$OS-$ARCH"

    if ($OS -eq "windows") {
        $OUTPUT_NAME += ".exe"
    }

    Write-Host "Building for $OS/$ARCH..."

    # Delete the output file if it already exists
    if (Test-Path -Path $OUTPUT_NAME) {
        Remove-Item -Path $OUTPUT_NAME -Force
    }

    # Set environment variables for Go build
    $env:GOOS = $OS
    $env:GOARCH = $ARCH

    go build `
        -ldflags="-X '$APP_PATH.Version=$env:EXTENSION_VERSION' -X '$APP_PATH.Commit=$COMMIT' -X '$APP_PATH.BuildDate=$BUILD_DATE' -X '$APP_PATH.RegistryURL=$REGISTRY_URL' -X '$APP_PATH.RegistrySource=$REGISTRY_SOURCE'" `
        -o $OUTPUT_NAME

    if ($LASTEXITCODE -ne 0) {
        Write-Host "An error occurred while building for $OS/$ARCH"
        exit 1
    }
}

Write-Host "Build completed successfully!"
Write-Host "Binaries are located in the $OUTPUT_DIR directory."
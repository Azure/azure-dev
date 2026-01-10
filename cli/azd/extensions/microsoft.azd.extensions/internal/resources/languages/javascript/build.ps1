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

# Check if the build type is specified
if (-not $env:EXTENSION_LANGUAGE) {
    Write-Host "Error: BUILD_TYPE environment variable is required (go or dotnet)"
    exit 1
}

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

    $ENTRY_FILE = "pkg-entry.js"
    $TARGET = "node16-$OS-x64"
    $EXPECTED_OUTPUT_NAME = "$EXTENSION_ID_SAFE-$OS-$ARCH"
    if ($OS -eq "windows") {
        $EXPECTED_OUTPUT_NAME += ".exe"
    }

    # Check Node.js and npm
    if (-not (Get-Command node -ErrorAction SilentlyContinue) -or -not (Get-Command npm -ErrorAction SilentlyContinue)) {
        Write-Host "Node.js or npm is not installed. Please install them from https://nodejs.org."
        exit 1
    }

    # Check npx
    if (-not (Get-Command npx -ErrorAction SilentlyContinue)) {
        Write-Host "npx is not available."
        exit 1
    }

    # Run npx pkg
    Write-Host "Ensuring pkg is available using npx..."
    npx --yes --package pkg echo "Ensured pkg is available"
    if ($LASTEXITCODE -ne 0) {
        Write-Host "Failed to download 'pkg' via npx."
        exit 1
    }

    Write-Host "Installing dependencies..."
    npm install
    if ($LASTEXITCODE -ne 0) {
        Write-Host "Failed to install npm dependencies."
        exit 1
    }

    Write-Host "Building JavaScript extension for $OS/$ARCH..."
    npx pkg $ENTRY_FILE -o $OUTPUT_DIR/$EXPECTED_OUTPUT_NAME --targets $TARGET --config package.json

    if ($LASTEXITCODE -ne 0) {
        Write-Host "An error occurred while building for $OS/$ARCH"
        exit 1
    }
}

Write-Host "Build completed successfully!"
Write-Host "Binaries are located in the $OUTPUT_DIR directory."

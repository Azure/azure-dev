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
$BUILD_DATE = (Get-Date -Format "yyyy-MM-ddTHH:mm:ssZ")

# Extract version from extension.yaml (single source of truth)
$EXTENSION_YAML = Join-Path $EXTENSION_DIR "extension.yaml"
if (Test-Path -Path $EXTENSION_YAML) {
    $YAML_CONTENT = Get-Content -Path $EXTENSION_YAML -Raw
    if ($YAML_CONTENT -match "version:\s*([\d\.]+)") {
        $VERSION = $matches[1]
        Write-Host "Extension Version: $VERSION"
    } else {
        $VERSION = "0.0.0"
        Write-Host "Warning: Version not found in extension.yaml, using default: $VERSION"
    }
} else {
    $VERSION = "0.0.0"
    Write-Host "Warning: extension.yaml not found, using default version: $VERSION"
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

# Create a version.py file with version information - this will be embedded in executable
$VERSION_PY = @"
# This file is auto-generated during build
VERSION = "$VERSION"
COMMIT = "$COMMIT"
BUILD_DATE = "$BUILD_DATE"
"@
Set-Content -Path (Join-Path $EXTENSION_DIR "version.py") -Value $VERSION_PY

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

    $PYTHON_MAIN_FILE = "main.py"

    Write-Host "Installing Python dependencies..."
    pip install -r requirements.txt

    $PYINSTALLER_NAME = "$EXTENSION_ID_SAFE-$OS-$ARCH"
    if ($OS -eq "windows") {
        $PYINSTALLER_NAME += ".exe"
    }

    Write-Host "Running PyInstaller for $OS/$ARCH..."
    python -m PyInstaller `
        --onefile `
        --add-data "generated_proto:generated_proto" `
        --add-data "version.py:." `
        --distpath $OUTPUT_DIR `
        --name $PYINSTALLER_NAME `
        $PYTHON_MAIN_FILE

    if ($LASTEXITCODE -ne 0) {
        Write-Host "An error occurred while building Python extension for $OS/$ARCH"
        exit 1
    }

    Rename-Item -Path (Join-Path $OUTPUT_DIR $PYINSTALLER_NAME) -NewName $OUTPUT_NAME
}

Write-Host "Build completed successfully!"
Write-Host "Binaries are located in the $OUTPUT_DIR directory."

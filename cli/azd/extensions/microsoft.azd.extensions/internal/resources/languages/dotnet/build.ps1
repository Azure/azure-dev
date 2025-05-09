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

    # Set runtime identifier for .NET
    $RUNTIME = if ($OS -eq "windows") { "win-$ARCH" } elseif ($OS -eq "darwin") { "osx-$ARCH" } else { "linux-$ARCH" }
    $PROJECT_FILE = "azd-extension.csproj"

    # Run dotnet publish for single file executable
    dotnet publish `
        -c Release `
        -r $RUNTIME `
        -o $OUTPUT_DIR `
        /p:PublishTrimmed=true `
        $PROJECT_FILE

    if ($LASTEXITCODE -ne 0) {
        Write-Host "An error occurred while building for $OS/$ARCH"
        exit 1
    }

    $EXPECTED_OUTPUT_NAME = $EXTENSION_ID_SAFE
    if ($OS -eq "windows") {
        $EXPECTED_OUTPUT_NAME += ".exe"
    }

    Rename-Item -Path "$OUTPUT_DIR/$EXPECTED_OUTPUT_NAME" -NewName $OUTPUT_NAME
}

Write-Host "Build completed successfully!"
Write-Host "Binaries are located in the $OUTPUT_DIR directory."

#!/bin/bash

# Get the directory of the script
EXTENSION_DIR="$(cd "$(dirname "$0")" && pwd)"

# Change to the script directory
cd "$EXTENSION_DIR" || exit

# Create a safe version of EXTENSION_ID replacing dots with dashes
EXTENSION_ID_SAFE="${EXTENSION_ID//./-}"

# Define output directory
OUTPUT_DIR="${OUTPUT_DIR:-$EXTENSION_DIR/bin}"

# Create output and target directories if they don't exist
mkdir -p "$OUTPUT_DIR"

# Get Git commit hash and build date
COMMIT=$(git rev-parse HEAD)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# List of OS and architecture combinations
if [ -n "$EXTENSION_PLATFORM" ]; then
    PLATFORMS=("$EXTENSION_PLATFORM")
else
    PLATFORMS=(
        "windows/amd64"
        "windows/arm64"
        "darwin/amd64"
        "darwin/arm64"
        "linux/amd64"
        "linux/arm64"
    )
fi

APP_PATH="github.com/azure/azure-dev/cli/azd/extensions/$EXTENSION_ID/internal/cmd"

# Check if the build type is specified
if [ -z "$EXTENSION_LANGUAGE" ]; then
    echo "Error: EXTENSION_LANGUAGE environment variable is required (go or dotnet)"
    exit 1
fi

# Loop through platforms and build
for PLATFORM in "${PLATFORMS[@]}"; do
    OS=$(echo "$PLATFORM" | cut -d'/' -f1)
    ARCH=$(echo "$PLATFORM" | cut -d'/' -f2)

    OUTPUT_NAME="$OUTPUT_DIR/$EXTENSION_ID_SAFE-$OS-$ARCH"

    if [ "$OS" = "windows" ]; then
        OUTPUT_NAME+='.exe'
    fi

    echo "Building for $OS/$ARCH..."

    # Delete the output file if it already exists
    [ -f "$OUTPUT_NAME" ] && rm -f "$OUTPUT_NAME"

    ENTRY_FILE="pkg-entry.js"
    TARGET="node16-$OS-x64"
    EXPECTED_OUTPUT_NAME="$EXTENSION_ID_SAFE-$OS-$ARCH"

    if [ "$OS" = "windows" ]; then
        EXPECTED_OUTPUT_NAME+='.exe'
    fi

    # Check Node.js and npm
    if ! command -v node &> /dev/null || ! command -v npm &> /dev/null
    then
        echo "Node.js or npm is not installed. Please install them from https://nodejs.org."
        exit 1
    fi

    # Check npx
    if ! command -v npx &> /dev/null
    then
        echo "npx is not available."
        exit 1
    fi

    # Run npx pkg
    echo "Ensuring pkg is available using npx..."
    npx --yes --package pkg echo "Ensured pkg is available"
    if [ $? -ne 0 ]; then
        echo "Failed to download 'pkg' via npx."
        exit 1
    fi
    
    echo "Installing dependencies..."
    npm install
    if [ $? -ne 0 ]; then
        echo "Failed to install npm dependencies."
        exit 1
    fi

    echo "Building JavaScript extension for $OS/$ARCH..."
    npx pkg "$ENTRY_FILE" --output "$OUTPUT_DIR/$EXPECTED_OUTPUT_NAME" --targets "$TARGET"

    if [ $? -ne 0 ]; then
        echo "An error occurred while building for $OS/$ARCH"
        exit 1
    fi
done

echo "Build completed successfully!"
echo "Binaries are located in the $OUTPUT_DIR directory."

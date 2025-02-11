#!/bin/bash

# Get the directory of the script
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Change to the script directory
cd "$SCRIPT_DIR" || exit

# Check for input parameter VERSION
if [ -z "$1" ]; then
    echo "Usage: $0 <VERSION>"
    exit 1
fi

# Define version from input parameter
VERSION="$1"

# Define application name
APP_NAME="azd-ext-demo"

# Define output directory
OUTPUT_DIR="$SCRIPT_DIR/bin"

# Define target directory in the user's home
TARGET_DIR="$HOME/.azd/extensions/microsoft.azd.demo"

# Create output and target directories if they don't exist
mkdir -p "$OUTPUT_DIR"
mkdir -p "$TARGET_DIR"

# Get Git commit hash and build date
COMMIT=$(git rev-parse HEAD)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)

# List of OS and architecture combinations
PLATFORMS=(
    "windows/amd64"
    "windows/arm64"
    "darwin/amd64"
    "darwin/arm64"
    "linux/amd64"
    "linux/arm64"
)

APP_PATH="github.com/azure/azure-dev/cli/azd/extensions/microsoft.azd.demo/internal/cmd"

# Loop through platforms and build
for PLATFORM in "${PLATFORMS[@]}"; do
    OS=$(echo "$PLATFORM" | cut -d'/' -f1)
    ARCH=$(echo "$PLATFORM" | cut -d'/' -f2)

    OUTPUT_NAME="$OUTPUT_DIR/$APP_NAME-$OS-$ARCH"
    TARGET_NAME="$TARGET_DIR/$APP_NAME-$OS-$ARCH"

    if [ "$OS" = "windows" ]; then
        OUTPUT_NAME+='.exe'
        TARGET_NAME+='.exe'
    fi

    echo "Building for $OS/$ARCH..."
    GOOS=$OS GOARCH=$ARCH go build \
        -ldflags="-X '$APP_PATH.Version=$VERSION' -X '$APP_PATH.Commit=$COMMIT' -X '$APP_PATH.BuildDate=$BUILD_DATE'" \
        -o "$OUTPUT_NAME"

    if [ $? -ne 0 ]; then
        echo "An error occurred while building for $OS/$ARCH"
        exit 1
    fi

    # Copy the build to the target directory
    cp "$OUTPUT_NAME" "$TARGET_NAME"
    echo "Copied $OUTPUT_NAME to $TARGET_NAME"
done

echo "Build completed successfully!"
echo "Binaries are located in the $OUTPUT_DIR directory and copied to $TARGET_DIR."

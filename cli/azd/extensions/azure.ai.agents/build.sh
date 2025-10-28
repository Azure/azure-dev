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

APP_PATH="$EXTENSION_ID/internal/cmd"

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

    # Set environment variables for Go build
    GOOS=$OS GOARCH=$ARCH go build \
        -ldflags="-X '$APP_PATH.Version=$EXTENSION_VERSION' -X '$APP_PATH.Commit=$COMMIT' -X '$APP_PATH.BuildDate=$BUILD_DATE'" \
        -o "$OUTPUT_NAME"

    if [ $? -ne 0 ]; then
        echo "An error occurred while building for $OS/$ARCH"
        exit 1
    fi
done

echo "Build completed successfully!"
echo "Binaries are located in the $OUTPUT_DIR directory."

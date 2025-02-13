#!/bin/bash

# Get the directory of the script
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Change to the script directory
cd "$SCRIPT_DIR" || exit

# Parse named input parameters: --app-name and --version
while [[ "$#" -gt 0 ]]; do
    case $1 in
        --app-name)
            APP_NAME="$2"
            shift 2
            ;;
        --version)
            VERSION="$2"
            shift 2
            ;;
        *)
            echo "Unknown parameter passed: $1"
            exit 1
            ;;
    esac
done

if [ -z "$APP_NAME" ]; then
    echo "Error: --app-name parameter is required"
    exit 1
fi

if [ -z "$VERSION" ]; then
    echo "Error: --version parameter is required"
    exit 1
fi

# Create a safe version of APP_NAME replacing dots with dashes
APP_NAME_SAFE="${APP_NAME//./-}"

# Define output directory
OUTPUT_DIR="$SCRIPT_DIR/bin"

# Create output and target directories if they don't exist
mkdir -p "$OUTPUT_DIR"

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

APP_PATH="github.com/azure/azure-dev/cli/azd/extensions/$APP_NAME/internal/cmd"

# Loop through platforms and build
for PLATFORM in "${PLATFORMS[@]}"; do
    OS=$(echo "$PLATFORM" | cut -d'/' -f1)
    ARCH=$(echo "$PLATFORM" | cut -d'/' -f2)

    OUTPUT_NAME="$OUTPUT_DIR/$APP_NAME_SAFE-$OS-$ARCH"

    if [ "$OS" = "windows" ]; then
        OUTPUT_NAME+='.exe'
    fi

    echo "Building for $OS/$ARCH..."
    GOOS=$OS GOARCH=$ARCH go build \
        -ldflags="-X '$APP_PATH.Version=$VERSION' -X '$APP_PATH.Commit=$COMMIT' -X '$APP_PATH.BuildDate=$BUILD_DATE'" \
        -o "$OUTPUT_NAME"

    if [ $? -ne 0 ]; then
        echo "An error occurred while building for $OS/$ARCH"
        exit 1
    fi
done

echo "Build completed successfully!"
echo "Binaries are located in the $OUTPUT_DIR directory."

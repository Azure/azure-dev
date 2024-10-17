#!/bin/bash

# Get the directory of the script
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Change to the script directory
cd "$SCRIPT_DIR" || exit

# Define application name
APP_NAME="azd-ext-test"

# Define output directory
OUTPUT_DIR="$SCRIPT_DIR/bin"

# Create output directory if it doesn't exist
mkdir -p "$OUTPUT_DIR"

# List of OS and architecture combinations
PLATFORMS=("windows/amd64" "darwin/amd64" "linux/amd64")

# Loop through platforms and build
for PLATFORM in "${PLATFORMS[@]}"; do
    OS=$(echo $PLATFORM | cut -d'/' -f1)
    ARCH=$(echo $PLATFORM | cut -d'/' -f2)

    OUTPUT_NAME="$OUTPUT_DIR/$APP_NAME-$OS-$ARCH"
    if [ "$OS" = "windows" ]; then
        OUTPUT_NAME+='.exe'
    fi

    echo "Building for $OS/$ARCH..."
    GOOS=$OS GOARCH=$ARCH go build -o "$OUTPUT_NAME"

    if [ $? -ne 0 ]; then
        echo "An error occurred while building for $OS/$ARCH"
        exit 1
    fi
done

echo "Build completed. Binaries are located in the $OUTPUT_DIR directory."

#!/bin/bash

# Get the directory of the script
SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"

# Change to the script directory
cd "$SCRIPT_DIR" || exit

# Define application name
APP_NAME="azd-ext-ai"

# Define output directory
OUTPUT_DIR="$SCRIPT_DIR/bin"

# Define target directory in the user's home
TARGET_DIR="$HOME/.azd/bin"

# Create output and target directories if they don't exist
mkdir -p "$OUTPUT_DIR"
mkdir -p "$TARGET_DIR"

# List of OS and architecture combinations
PLATFORMS=("windows/amd64" "darwin/amd64" "linux/amd64")

# Loop through platforms and build
for PLATFORM in "${PLATFORMS[@]}"; do
    OS=$(echo $PLATFORM | cut -d'/' -f1)
    ARCH=$(echo $PLATFORM | cut -d'/' -f2)

    OUTPUT_NAME="$OUTPUT_DIR/$APP_NAME-$OS-$ARCH"
    TARGET_NAME="$TARGET_DIR/$APP_NAME-$OS-$ARCH"

    if [ "$OS" = "windows" ]; then
        OUTPUT_NAME+='.exe'
        TARGET_NAME+='.exe'
    fi

    echo "Building for $OS/$ARCH..."
    GOOS=$OS GOARCH=$ARCH go build -o "$OUTPUT_NAME"

    if [ $? -ne 0 ]; then
        echo "An error occurred while building for $OS/$ARCH"
        exit 1
    fi

    # Copy the build to the target directory
    cp "$OUTPUT_NAME" "$TARGET_NAME"
    echo "Copied $OUTPUT_NAME to $TARGET_NAME"
done

echo "Build completed. Binaries are located in the $OUTPUT_DIR directory and copied to $TARGET_DIR."

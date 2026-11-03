#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")"
OUTPUT_DIR="${OUTPUT_DIR:-$PWD/bin}"
EXTENSION_VERSION="${EXTENSION_VERSION:-$(cat version.txt)}"
mkdir -p "$OUTPUT_DIR"

if [ -n "${EXTENSION_PLATFORM:-}" ]; then
    PLATFORMS=("$EXTENSION_PLATFORM")
else
    PLATFORMS=(windows/amd64 windows/arm64 darwin/amd64 darwin/arm64 linux/amd64 linux/arm64)
fi

for PLATFORM in "${PLATFORMS[@]}"; do
    OS="${PLATFORM%/*}"
    ARCH="${PLATFORM#*/}"
    OUTPUT_NAME="$OUTPUT_DIR/azure-resources-$OS-$ARCH"
    if [ "$OS" = windows ]; then
        OUTPUT_NAME+=".exe"
    fi
    GOOS="$OS" GOARCH="$ARCH" go build \
        -ldflags="-X azure.resources/internal/version.Version=$EXTENSION_VERSION" \
        -o "$OUTPUT_NAME" .
done

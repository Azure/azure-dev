#!/bin/bash
set -euo pipefail

cd "$(dirname "$0")"
OUTPUT_DIR="${OUTPUT_DIR:-$PWD/bin}"
EXTENSION_VERSION="${EXTENSION_VERSION:-$(cat version.txt)}"
mkdir -p "$OUTPUT_DIR"

COMMIT=$(git rev-parse HEAD)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
APP_PATH="azure.resources/internal/version"

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
        -ldflags="-X '$APP_PATH.Version=$EXTENSION_VERSION' -X '$APP_PATH.Commit=$COMMIT' -X '$APP_PATH.BuildDate=$BUILD_DATE'" \
        -o "$OUTPUT_NAME" .
done

#!/bin/bash
#
# Builds the RLE extension for this machine and drops the binary straight into
# the azd extension directory.
#
# This is the loop to use while working on the extension. The dev registry
# (prepare-dev-release.ps1) serves artifacts from raw.githubusercontent.com, so
# nothing it publishes is installable until those artifacts are committed and
# pushed -- which makes it the wrong tool for testing a change you have not
# pushed yet. This script skips the registry entirely and overwrites the binary
# azd already resolves, so `azd ai rle ...` picks the build up immediately.
#
# Usage: ./install-local.sh
#
set -euo pipefail

EXTENSION_DIR="$(cd "$(dirname "$0")" && pwd)"
EXTENSION_ID="azure.ai.rle"
EXTENSION_ID_SAFE="${EXTENSION_ID//./-}"

OS=$(go env GOOS)
ARCH=$(go env GOARCH)
BINARY_NAME="$EXTENSION_ID_SAFE-$OS-$ARCH"
[ "$OS" = "windows" ] && BINARY_NAME+=".exe"

INSTALL_DIR="${AZD_CONFIG_DIR:-$HOME/.azd}/extensions/$EXTENSION_ID"
if [ ! -d "$INSTALL_DIR" ]; then
    echo "error: $EXTENSION_ID is not installed at $INSTALL_DIR." >&2
    echo "Install it once from a registry, then use this script to iterate." >&2
    exit 1
fi

VERSION="$(tr -d '[:space:]' < "$EXTENSION_DIR/version.txt")"
COMMIT=$(git -C "$EXTENSION_DIR" rev-parse HEAD)
BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
APP_PATH="$EXTENSION_ID/internal/cmd"

echo "Building $EXTENSION_ID $VERSION for $OS/$ARCH..."
cd "$EXTENSION_DIR"
go build \
    -ldflags="-X '$APP_PATH.Version=$VERSION' -X '$APP_PATH.Commit=$COMMIT' -X '$APP_PATH.BuildDate=$BUILD_DATE'" \
    -o "$INSTALL_DIR/$BINARY_NAME"

# azd reads the manifest and the command metadata from the install directory,
# not from the binary, so a build whose commands changed is only half installed
# until these are refreshed too. `metadata` writes the document to stdout.
cp "$EXTENSION_DIR/extension.yaml" "$INSTALL_DIR/extension.yaml"
"$INSTALL_DIR/$BINARY_NAME" metadata > "$INSTALL_DIR/metadata.json"

echo "Installed to $INSTALL_DIR/$BINARY_NAME"
echo "Verify with: azd ai rle version"

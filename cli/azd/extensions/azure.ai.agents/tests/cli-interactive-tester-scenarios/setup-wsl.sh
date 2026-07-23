#!/usr/bin/env bash
# setup-wsl.sh — Build and install native Linux azd + extension for WSL testing.
#
# Run this from inside WSL (or via `wsl bash setup-wsl.sh` from Windows) after
# making local code changes. It compiles native Linux/amd64 binaries from the
# repo source so the cli-interactive-tester drives your dev build directly.
#
# Prerequisites:
#   - Go toolchain installed in WSL (or accessible via PATH)
#   - Git installed in WSL
#   - sudo access (for installing azd to /usr/local/bin)
#
# Usage:
#   cd cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios
#   bash setup-wsl.sh
#
# What it does:
#   1. Builds azd core (linux/amd64) → /usr/local/bin/azd
#   2. Ensures the azd extensions dev kit (microsoft.azd.extensions) is installed
#   3. Builds + packages + installs the azure.ai.agents extension from source
#   4. Verifies the dev version is running

set -euo pipefail

# Resolve paths relative to this script
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXTENSION_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
AZD_DIR="$(cd "$EXTENSION_DIR/../.." && pwd)"
REPO_ROOT="$(cd "$AZD_DIR/../.." && pwd)"

echo "=== setup-wsl.sh ==="
echo "  Repo root:     $REPO_ROOT"
echo "  azd source:    $AZD_DIR"
echo "  Extension src: $EXTENSION_DIR"
echo ""

# --- Prerequisites ---
if ! command -v go &>/dev/null; then
    echo "ERROR: Go toolchain not found. Install Go in WSL first." >&2
    exit 1
fi

if ! sudo -n true 2>/dev/null; then
    echo "NOTE: sudo access is needed to install azd to /usr/local/bin."
    echo "      You may be prompted for your password."
fi

# --- Step 1: Build azd core ---
echo "▸ Building azd core (linux/amd64)..."

COMMIT=$(cd "$REPO_ROOT" && git rev-parse HEAD 2>/dev/null || echo "0000000000000000000000000000000000000000")
VERSION="0.0.0-dev.0"
LDFLAGS="-X 'github.com/azure/azure-dev/cli/azd/internal.Version=${VERSION} (commit ${COMMIT})'"

(cd "$AZD_DIR" && GOOS=linux GOARCH=amd64 go build \
    -ldflags="$LDFLAGS" \
    -o /tmp/azd-dev-build \
    .)

sudo install -m 755 /tmp/azd-dev-build /usr/local/bin/azd
rm -f /tmp/azd-dev-build

echo "  ✓ Installed /usr/local/bin/azd"
echo ""

# --- Step 2: Ensure microsoft.azd.extensions is available ---
echo "▸ Checking for azd extensions dev kit (microsoft.azd.extensions)..."

if azd x version &>/dev/null; then
    echo "  ✓ microsoft.azd.extensions is already installed"
else
    echo "  → Installing microsoft.azd.extensions from registry..."
    azd extension install microsoft.azd.extensions --no-prompt
    echo "  ✓ Installed microsoft.azd.extensions"
fi
echo ""

# --- Step 3: Build extension from source ---
echo "▸ Building azure.ai.agents extension (linux/amd64)..."
azd x build -C "$EXTENSION_DIR"
echo "  ✓ Extension built"
echo ""

# --- Step 4: Package as bundle ---
echo "▸ Packaging extension bundle..."
azd x pack --bundle -C "$EXTENSION_DIR"

# Find the generated bundle zip
EXT_VERSION=$(cat "$EXTENSION_DIR/version.txt" 2>/dev/null || echo "0.0.0-dev")
BUNDLE_ZIP="$EXTENSION_DIR/azure-ai-agents_${EXT_VERSION}.zip"

if [ ! -f "$BUNDLE_ZIP" ]; then
    echo "ERROR: Expected bundle not found at $BUNDLE_ZIP" >&2
    echo "  Check the output above for packaging errors." >&2
    exit 1
fi

echo "  ✓ Bundle created: $BUNDLE_ZIP"
echo ""

# --- Step 5: Install from bundle ---
echo "▸ Installing extension from bundle..."
azd extension install "$BUNDLE_ZIP" --force --no-prompt
echo "  ✓ Extension installed and registered"
echo ""

# Clean up the bundle zip
rm -f "$BUNDLE_ZIP"

# --- Step 6: Verify ---
echo "▸ Verifying installation..."

AZD_VER=$(azd version 2>&1 | head -1)
echo "  azd version: $AZD_VER"

if ! echo "$AZD_VER" | grep -q "$VERSION"; then
    echo "ERROR: azd version does not contain expected dev version '$VERSION'" >&2
    echo "  Got: $AZD_VER" >&2
    echo "  This suggests the dev build was not installed correctly." >&2
    exit 1
fi

EXT_VER=$(azd ai agent version 2>&1)
echo "  extension:   $EXT_VER"

if ! echo "$EXT_VER" | grep -qi "version"; then
    echo "ERROR: Failed to get extension version. Is it properly registered?" >&2
    echo "  Got: $EXT_VER" >&2
    echo "  Try 'azd extension list' to check installed extensions." >&2
    exit 1
fi

echo ""
echo "=== Done. WSL is ready for scenario testing. ==="

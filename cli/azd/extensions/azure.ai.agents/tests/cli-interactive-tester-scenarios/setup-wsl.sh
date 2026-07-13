#!/usr/bin/env bash
# setup-wsl.sh — Build and install native Linux azd + extension for WSL testing.
#
# Run this from inside WSL (or via `wsl bash setup-wsl.sh` from Windows) after
# making local code changes. It cross-compiles native Linux/amd64 binaries from
# the repo source so the cli-interactive-tester drives your dev build directly.
#
# Prerequisites:
#   - Go toolchain installed in WSL (or accessible via PATH)
#   - Git installed in WSL
#
# Usage:
#   cd cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios
#   bash setup-wsl.sh
#
# What it does:
#   1. Builds azd core (linux/amd64) → /usr/local/bin/azd
#   2. Builds the azure.ai.agents extension (linux/amd64) → ~/.azd/extensions/
#   3. Prints version confirmation

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

# --- Step 1: Build azd core ---
echo "▸ Building azd core (linux/amd64)..."

COMMIT=$(cd "$REPO_ROOT" && git rev-parse --short HEAD 2>/dev/null || echo "unknown")
VERSION="0.0.0-dev.0"
LDFLAGS="-X 'github.com/azure/azure-dev/cli/azd/internal.Version=${VERSION} (commit ${COMMIT})'"

GOOS=linux GOARCH=amd64 go build \
    -ldflags="$LDFLAGS" \
    -o /usr/local/bin/azd \
    "$AZD_DIR"

echo "  ✓ Installed /usr/local/bin/azd"
echo ""

# --- Step 2: Build the extension ---
echo "▸ Building azure.ai.agents extension (linux/amd64)..."

EXTENSION_INSTALL_DIR="$HOME/.azd/extensions/azure.ai.agents"
mkdir -p "$EXTENSION_INSTALL_DIR"

EXT_COMMIT=$(cd "$EXTENSION_DIR" && git rev-parse HEAD 2>/dev/null || echo "unknown")
EXT_BUILD_DATE=$(date -u +%Y-%m-%dT%H:%M:%SZ)
EXT_VERSION=$(cat "$EXTENSION_DIR/version.txt" 2>/dev/null || echo "0.0.0-dev")
VERSION_PATH="azureaiagent/internal/version"

GOOS=linux GOARCH=amd64 go build \
    -ldflags="-X '${VERSION_PATH}.Version=${EXT_VERSION}' -X '${VERSION_PATH}.Commit=${EXT_COMMIT}' -X '${VERSION_PATH}.BuildDate=${EXT_BUILD_DATE}'" \
    -o "$EXTENSION_INSTALL_DIR/azure-ai-agents-linux-amd64" \
    "$EXTENSION_DIR"

# Copy extension.yaml (azd needs it to discover the extension)
cp "$EXTENSION_DIR/extension.yaml" "$EXTENSION_INSTALL_DIR/extension.yaml"

echo "  ✓ Installed $EXTENSION_INSTALL_DIR/azure-ai-agents-linux-amd64"
echo ""

# --- Step 3: Verify ---
echo "▸ Verifying installation..."
echo "  azd version: $(azd version 2>&1 | head -1)"
echo "  extension:   $(azd ai agent version 2>&1 | grep -i version | head -1)"
echo ""
echo "=== Done. WSL is ready for scenario testing. ==="

#!/usr/bin/env bash
# setup-wsl.sh — Build and install native Linux azd + extension for WSL testing.
#
# Run this from inside WSL (or via `wsl bash setup-wsl.sh` from Windows) after
# making local code changes. It compiles native Linux binaries from the
# repo source so the cli-interactive-tester drives your dev build directly.
#
# Prerequisites:
#   - Git installed in WSL
#   - curl or wget, plus awk, grep, tar, sha256sum, and uname
#   - sudo access (for installing Go and azd under /usr/local)
#
# Usage:
#   cd cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios
#   bash setup-wsl.sh
#
# What it does:
#   1. Installs the Go version pinned by cli/azd/go.mod when needed
#   2. Builds azd core for the native Linux architecture -> /usr/local/bin/azd
#   3. Ensures the azd extensions dev kit (microsoft.azd.extensions) is installed
#   4. Builds + packages + installs the azure.ai.agents extension from source
#   5. Verifies the dev version is running

set -euo pipefail

# Resolve paths relative to this script
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
EXTENSION_DIR="$(cd "$SCRIPT_DIR/../.." && pwd)"
AZD_DIR="$(cd "$EXTENSION_DIR/../.." && pwd)"
REPO_ROOT="$(cd "$AZD_DIR/../.." && pwd)"

TEMP_DIR="$(mktemp -d)"
BUNDLE_ZIP=""
cleanup() {
    rm -rf "$TEMP_DIR"
    if [ -n "$BUNDLE_ZIP" ]; then
        rm -f "$BUNDLE_ZIP"
    fi
}
trap cleanup EXIT

echo "=== setup-wsl.sh ==="
echo "  Repo root:     $REPO_ROOT"
echo "  azd source:    $AZD_DIR"
echo "  Extension src: $EXTENSION_DIR"
echo ""

# --- Prerequisites ---
for cmd in git awk grep tar sha256sum sudo uname; do
    if ! command -v "$cmd" &>/dev/null; then
        echo "ERROR: Required command '$cmd' was not found in WSL." >&2
        exit 1
    fi
done

if ! grep -qi microsoft /proc/sys/kernel/osrelease 2>/dev/null; then
    echo "ERROR: setup-wsl.sh must only be run inside WSL." >&2
    exit 1
fi

if command -v curl &>/dev/null; then
    download() {
        curl --fail --location --silent --show-error --output "$2" "$1"
    }
elif command -v wget &>/dev/null; then
    download() {
        wget --quiet --output-document="$2" "$1"
    }
else
    echo "ERROR: curl or wget is required to download the pinned Go toolchain." >&2
    exit 1
fi

if ! sudo -n true 2>/dev/null; then
    echo "NOTE: sudo access is needed to install Go and azd under /usr/local."
    echo "      You may be prompted for your password."
fi

# --- Step 1: Ensure the repository-pinned Go toolchain ---
GO_VERSION=$(awk '$1 == "go" { sub(/\r$/, "", $2); print $2; exit }' "$AZD_DIR/go.mod")
if [[ ! "$GO_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+([a-z0-9.-]+)?$ ]]; then
    echo "ERROR: Could not read a valid Go version from $AZD_DIR/go.mod." >&2
    exit 1
fi

case "$(uname -m)" in
    x86_64|amd64)
        GO_ARCH="amd64"
        ;;
    aarch64|arm64)
        GO_ARCH="arm64"
        ;;
    *)
        echo "ERROR: Unsupported WSL architecture '$(uname -m)'." >&2
        exit 1
        ;;
esac

export PATH="/usr/local/go/bin:$PATH"
export GOTOOLCHAIN=local
EXPECTED_GO_VERSION="go${GO_VERSION}"
EXPECTED_GO_PLATFORM="linux/${GO_ARCH}"
CURRENT_GO_VERSION=""
CURRENT_GO_PLATFORM=""

if command -v go &>/dev/null; then
    read -r _ _ CURRENT_GO_VERSION CURRENT_GO_PLATFORM < <(go version 2>/dev/null || true) || true
fi

if [ "$CURRENT_GO_VERSION" = "$EXPECTED_GO_VERSION" ] &&
    [ "$CURRENT_GO_PLATFORM" = "$EXPECTED_GO_PLATFORM" ]; then
    echo "▸ Using repository-pinned Go toolchain: $CURRENT_GO_VERSION $CURRENT_GO_PLATFORM"
else
    GO_ARCHIVE="go${GO_VERSION}.linux-${GO_ARCH}.tar.gz"
    GO_METADATA="$TEMP_DIR/go-releases.json"
    GO_ARCHIVE_PATH="$TEMP_DIR/$GO_ARCHIVE"

    echo "▸ Installing repository-pinned Go toolchain: $EXPECTED_GO_VERSION $EXPECTED_GO_PLATFORM"
    download "https://go.dev/dl/?mode=json&include=all" "$GO_METADATA"

    GO_SHA256=$(awk -v target="$GO_ARCHIVE" '
        index($0, "\"filename\"") && index($0, "\"" target "\"") {
            found = 1
            next
        }
        found && index($0, "\"sha256\"") {
            value = $0
            sub(/^.*"sha256"[[:space:]]*:[[:space:]]*"/, "", value)
            sub(/".*$/, "", value)
            print value
            exit
        }
    ' "$GO_METADATA")

    if [[ ! "$GO_SHA256" =~ ^[0-9a-f]{64}$ ]]; then
        echo "ERROR: Could not resolve the official SHA-256 for $GO_ARCHIVE." >&2
        exit 1
    fi

    download "https://go.dev/dl/$GO_ARCHIVE" "$GO_ARCHIVE_PATH"
    if ! printf '%s  %s\n' "$GO_SHA256" "$GO_ARCHIVE_PATH" | sha256sum --check --status; then
        echo "ERROR: SHA-256 verification failed for $GO_ARCHIVE." >&2
        exit 1
    fi

    tar -C "$TEMP_DIR" -xzf "$GO_ARCHIVE_PATH"
    read -r _ _ CANDIDATE_GO_VERSION CANDIDATE_GO_PLATFORM < <("$TEMP_DIR/go/bin/go" version)
    if [ "$CANDIDATE_GO_VERSION" != "$EXPECTED_GO_VERSION" ] ||
        [ "$CANDIDATE_GO_PLATFORM" != "$EXPECTED_GO_PLATFORM" ]; then
        echo "ERROR: Downloaded Go archive verification failed." >&2
        echo "  Expected: $EXPECTED_GO_VERSION $EXPECTED_GO_PLATFORM" >&2
        echo "  Got:      $CANDIDATE_GO_VERSION $CANDIDATE_GO_PLATFORM" >&2
        exit 1
    fi

    sudo rm -rf /usr/local/go
    sudo mv "$TEMP_DIR/go" /usr/local/go
    hash -r

    read -r _ _ CURRENT_GO_VERSION CURRENT_GO_PLATFORM < <(go version)
    if [ "$CURRENT_GO_VERSION" != "$EXPECTED_GO_VERSION" ] ||
        [ "$CURRENT_GO_PLATFORM" != "$EXPECTED_GO_PLATFORM" ]; then
        echo "ERROR: Go installation verification failed." >&2
        echo "  Expected: $EXPECTED_GO_VERSION $EXPECTED_GO_PLATFORM" >&2
        echo "  Got:      $CURRENT_GO_VERSION $CURRENT_GO_PLATFORM" >&2
        exit 1
    fi

    echo "  Installed and verified $CURRENT_GO_VERSION $CURRENT_GO_PLATFORM"
fi
echo ""

# --- Step 2: Build azd core ---
echo "▸ Building azd core ($EXPECTED_GO_PLATFORM)..."

COMMIT=$(cd "$REPO_ROOT" && git rev-parse HEAD 2>/dev/null || echo "0000000000000000000000000000000000000000")
VERSION="0.0.0-dev.0"
LDFLAGS="-X 'github.com/azure/azure-dev/cli/azd/internal.Version=${VERSION} (commit ${COMMIT})'"

(cd "$AZD_DIR" && GOOS=linux GOARCH="$GO_ARCH" go build \
    -ldflags="$LDFLAGS" \
    -o "$TEMP_DIR/azd-dev-build" \
    .)

sudo install -m 755 "$TEMP_DIR/azd-dev-build" /usr/local/bin/azd

echo "  ✓ Installed /usr/local/bin/azd"
echo ""

# --- Step 3: Ensure microsoft.azd.extensions is available ---
echo "▸ Checking for azd extensions dev kit (microsoft.azd.extensions)..."

if azd x version &>/dev/null; then
    echo "  ✓ microsoft.azd.extensions is already installed"
else
    echo "  → Installing microsoft.azd.extensions from registry..."
    azd extension install microsoft.azd.extensions --force --no-prompt
    echo "  ✓ Installed microsoft.azd.extensions"
fi
echo ""

# --- Step 4: Build extension from source ---
echo "▸ Building azure.ai.agents extension ($EXPECTED_GO_PLATFORM)..."
azd x build -C "$EXTENSION_DIR"
echo "  ✓ Extension built"
echo ""

# --- Step 5: Package as bundle ---
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

# --- Step 6: Install from bundle ---
echo "▸ Installing extension from bundle..."
azd extension install "$BUNDLE_ZIP" --force --no-prompt
echo "  ✓ Extension installed and registered"
echo ""

# --- Step 7: Verify ---
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

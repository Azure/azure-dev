#!/usr/bin/env bash
# cspell:ignore osrelease OPTOUT
# setup-wsl.sh — Build and install native Linux azd + extension for WSL testing.
#
# Run this from inside WSL (or via `wsl bash setup-wsl.sh` from Windows) after
# making local code changes. It compiles native Linux binaries from the
# repo source so the cli-interactive-tester drives your dev build directly.
#
# Prerequisites:
#   - Git installed in WSL
#   - curl or wget, plus awk, grep, tar, sha256sum, sha512sum, and uname
#   - sudo access (for installing Go, .NET, and azd under /usr/local)
#
# Usage:
#   cd cli/azd/extensions/azure.ai.agents/tests/cli-interactive-tester-scenarios
#   bash setup-wsl.sh
#
# What it does:
#   1. Installs the Go version pinned by cli/azd/go.mod when needed
#   2. Installs the .NET SDK pinned by dotnet-sdk.version when needed
#   3. Builds azd core for the native Linux architecture -> /usr/local/bin/azd
#   4. Ensures the azd extensions dev kit supports bundle packaging
#   5. Builds + packages + installs the azure.ai.agents extension from source
#   6. Verifies the dev version is running

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
for cmd in git awk grep tar sha256sum sha512sum sudo uname; do
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
    echo "ERROR: curl or wget is required to download the pinned Go and .NET toolchains." >&2
    exit 1
fi

has_bundle_capable_dev_kit() {
    local pack_help
    pack_help=$(azd x pack --help 2>/dev/null) || return 1
    grep -q -- "--bundle" <<<"$pack_help"
}

if ! sudo -n true 2>/dev/null; then
    echo "NOTE: sudo access is needed to install Go, .NET, and azd under /usr/local."
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
        DOTNET_ARCH="x64"
        ;;
    aarch64|arm64)
        GO_ARCH="arm64"
        DOTNET_ARCH="arm64"
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

# --- Step 2: Ensure the scenario-pinned .NET SDK ---
DOTNET_VERSION_FILE="$SCRIPT_DIR/dotnet-sdk.version"
DOTNET_SDK_VERSION=$(awk 'NF { sub(/\r$/, "", $1); print $1; exit }' "$DOTNET_VERSION_FILE")
if [[ ! "$DOTNET_SDK_VERSION" =~ ^[0-9]+\.[0-9]+\.[0-9]+$ ]]; then
    echo "ERROR: Could not read a valid .NET SDK version from $DOTNET_VERSION_FILE." >&2
    exit 1
fi

DOTNET_CHANNEL="${DOTNET_SDK_VERSION%.*}"
DOTNET_RID="linux-${DOTNET_ARCH}"
DOTNET_ROOT="/usr/local/dotnet"
export DOTNET_ROOT
export DOTNET_CLI_TELEMETRY_OPTOUT=1
export DOTNET_NOLOGO=1
export PATH="$DOTNET_ROOT:$PATH"

has_pinned_dotnet_sdk() {
    [ "$("$1" --version 2>/dev/null)" = "$DOTNET_SDK_VERSION" ]
}

if [ -x "$DOTNET_ROOT/dotnet" ] && has_pinned_dotnet_sdk "$DOTNET_ROOT/dotnet"; then
    echo "▸ Using scenario-pinned .NET SDK: $DOTNET_SDK_VERSION $DOTNET_RID"
    sudo ln -sfn "$DOTNET_ROOT/dotnet" /usr/local/bin/dotnet
else
    DOTNET_METADATA="$TEMP_DIR/dotnet-releases.json"
    DOTNET_ARCHIVE_PATH="$TEMP_DIR/dotnet-sdk-${DOTNET_SDK_VERSION}-${DOTNET_RID}.tar.gz"

    echo "▸ Installing scenario-pinned .NET SDK: $DOTNET_SDK_VERSION $DOTNET_RID"
    download \
        "https://dotnetcli.blob.core.windows.net/dotnet/release-metadata/${DOTNET_CHANNEL}/releases.json" \
        "$DOTNET_METADATA"

    read -r DOTNET_URL DOTNET_SHA512 < <(
        awk -v expected_version="$DOTNET_SDK_VERSION" -v expected_rid="$DOTNET_RID" '
            function json_value(line, value) {
                value = line
                sub(/^.*:[[:space:]]*"/, "", value)
                sub(/".*$/, "", value)
                return value
            }
            /"sdk"[[:space:]]*:/ {
                in_sdk = 1
                matching_sdk = 0
                in_files = 0
                next
            }
            in_sdk && !matching_sdk && /"version"[[:space:]]*:/ {
                if (json_value($0) == expected_version) {
                    matching_sdk = 1
                } else {
                    in_sdk = 0
                }
                next
            }
            matching_sdk && /"files"[[:space:]]*:/ {
                in_files = 1
                next
            }
            in_files && /"rid"[[:space:]]*:/ {
                rid = json_value($0)
                next
            }
            in_files && /"url"[[:space:]]*:/ {
                url = json_value($0)
                next
            }
            in_files && /"hash"[[:space:]]*:/ {
                hash = json_value($0)
                if (rid == expected_rid) {
                    print url, hash
                    exit
                }
            }
        ' "$DOTNET_METADATA"
    ) || true

    DOTNET_SHA512=${DOTNET_SHA512,,}
    if [[ ! "$DOTNET_URL" =~ ^https:// ]] || [[ ! "$DOTNET_SHA512" =~ ^[0-9a-f]{128}$ ]]; then
        echo "ERROR: Could not resolve the official .NET SDK asset for" \
            "$DOTNET_SDK_VERSION $DOTNET_RID." >&2
        exit 1
    fi

    download "$DOTNET_URL" "$DOTNET_ARCHIVE_PATH"
    if ! printf '%s  %s\n' "$DOTNET_SHA512" "$DOTNET_ARCHIVE_PATH" |
        sha512sum --check --status; then
        echo "ERROR: SHA-512 verification failed for .NET SDK $DOTNET_SDK_VERSION $DOTNET_RID." >&2
        exit 1
    fi

    mkdir "$TEMP_DIR/dotnet"
    tar -C "$TEMP_DIR/dotnet" -xzf "$DOTNET_ARCHIVE_PATH"
    if ! has_pinned_dotnet_sdk "$TEMP_DIR/dotnet/dotnet"; then
        echo "ERROR: Downloaded .NET SDK archive verification failed." >&2
        exit 1
    fi

    sudo rm -rf "$DOTNET_ROOT"
    sudo mv "$TEMP_DIR/dotnet" "$DOTNET_ROOT"
    sudo ln -sfn "$DOTNET_ROOT/dotnet" /usr/local/bin/dotnet
    hash -r

    if ! has_pinned_dotnet_sdk dotnet; then
        echo "ERROR: .NET SDK installation verification failed." >&2
        exit 1
    fi

    echo "  Installed and verified .NET SDK $DOTNET_SDK_VERSION $DOTNET_RID"
fi
echo ""

# --- Step 3: Build azd core ---
echo "▸ Building azd core ($EXPECTED_GO_PLATFORM)..."

EXPECTED_AZD_VERSION="0.0.0-dev.0 (commit 0000000000000000000000000000000000000000)"

(cd "$AZD_DIR" && GOOS=linux GOARCH="$GO_ARCH" go build \
    -o "$TEMP_DIR/azd-dev-build" \
    .)

sudo install -m 755 "$TEMP_DIR/azd-dev-build" /usr/local/bin/azd

echo "  ✓ Installed /usr/local/bin/azd"
echo ""

# --- Step 4: Ensure microsoft.azd.extensions supports bundle packaging ---
echo "▸ Checking azd extensions dev kit bundle support (microsoft.azd.extensions)..."

if has_bundle_capable_dev_kit; then
    echo "  ✓ microsoft.azd.extensions supports bundle packaging"
else
    echo "  → Installing or upgrading microsoft.azd.extensions from registry..."
    azd extension install microsoft.azd.extensions --source azd --force --no-prompt
    if ! has_bundle_capable_dev_kit; then
        echo "ERROR: microsoft.azd.extensions does not support 'azd x pack --bundle' after installation." >&2
        exit 1
    fi
    echo "  ✓ Installed a bundle-capable microsoft.azd.extensions"
fi
echo ""

# --- Step 5: Build extension from source ---
echo "▸ Building azure.ai.agents extension ($EXPECTED_GO_PLATFORM)..."
azd x build -C "$EXTENSION_DIR"
echo "  ✓ Extension built"
echo ""

# --- Step 6: Package as bundle ---
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

# --- Step 7: Install from bundle ---
echo "▸ Installing extension from bundle..."
azd extension install "$BUNDLE_ZIP" --force --no-prompt
echo "  ✓ Extension installed and registered"
echo ""

# --- Step 8: Verify ---
echo "▸ Verifying installation..."

AZD_VER=$(azd version 2>&1 | head -1)
echo "  azd version: $AZD_VER"

if ! echo "$AZD_VER" | grep -Fq "$EXPECTED_AZD_VERSION"; then
    echo "ERROR: azd version does not contain expected dev version '$EXPECTED_AZD_VERSION'" >&2
    echo "  Got: $AZD_VER" >&2
    echo "  This suggests the dev build was not installed correctly." >&2
    exit 1
fi

EXT_VER=$(azd ai agent version 2>&1)
echo "  extension:   $EXT_VER"
echo "  .NET SDK:   $DOTNET_SDK_VERSION"

if ! echo "$EXT_VER" | grep -qi "version"; then
    echo "ERROR: Failed to get extension version. Is it properly registered?" >&2
    echo "  Got: $EXT_VER" >&2
    echo "  Try 'azd extension list' to check installed extensions." >&2
    exit 1
fi

echo ""
echo "=== Done. WSL is ready for scenario testing. ==="

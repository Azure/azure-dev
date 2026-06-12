#!/usr/bin/env bash
#
# Updates the pinned Go toolchain version across the repository to keep all
# go.mod files, the ADO pipeline template, Dockerfiles, and the devcontainer
# Go feature in sync.
#
# Usage:
#   eng/scripts/Update-GoVersion.sh <new-version>
#   eng/scripts/Update-GoVersion.sh 1.26.4
#
# This is the bash equivalent of eng/scripts/Update-GoVersion.ps1; keep the
# two scripts in sync when changing the update logic.

set -euo pipefail

if [ "$#" -ne 1 ] || [ -z "${1:-}" ]; then
    echo "Usage: $0 <new-version>" >&2
    echo "Example: $0 1.26.4" >&2
    exit 1
fi

export NV="$1"

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/../../" && pwd)"

# ADO pipeline template that pins the Go toolchain version
ADO_SETUP_GO="$REPO_ROOT/eng/pipelines/templates/steps/setup-go.yml"

# devcontainer Go feature version
DEVCONTAINER="$REPO_ROOT/.devcontainer/devcontainer.json"

updated=()
skipped=()

# Applies a perl substitution to a whole file in-place, preserving the original
# byte layout (including the trailing newline), and records whether it changed.
# Args: <file> <perl-expression>
replace_in_file() {
    local file="$1"
    local expr="$2"
    local rel="${file#"$REPO_ROOT"}"

    local before after
    before="$(md5sum "$file" | awk '{ print $1 }')"
    perl -0777 -i -pe "$expr" "$file"
    after="$(md5sum "$file" | awk '{ print $1 }')"

    if [ "$before" != "$after" ]; then
        updated+=("$rel")
    else
        skipped+=("$rel")
    fi
}

# --- Update go.mod files (including testdata samples) ---
while IFS= read -r -d '' file; do
    if grep -qE '^go[[:space:]]+\S+' "$file"; then
        replace_in_file "$file" 's/^go\s+\S+/go $ENV{NV}/mg'
    fi
done < <(find "$REPO_ROOT/cli/azd" -name 'go.mod' -print0)

# --- Update ADO pipeline template ---
if [ -f "$ADO_SETUP_GO" ]; then
    replace_in_file "$ADO_SETUP_GO" 's/^(\s+GoVersion:\s+)\S+/$1$ENV{NV}/mg'
fi

# --- Update Dockerfiles referencing golang:<version> base images ---
while IFS= read -r -d '' file; do
    if grep -qE 'golang:[0-9]+\.[0-9]+' "$file"; then
        replace_in_file "$file" 's/golang:\d+[\d.]*/golang:$ENV{NV}/g'
    fi
done < <(find "$REPO_ROOT/cli/azd" -name 'Dockerfile' -print0)

# --- Update devcontainer.json Go feature version ---
if [ -f "$DEVCONTAINER" ]; then
    replace_in_file "$DEVCONTAINER" \
        's{("ghcr\.io/devcontainers/features/go:\d+":\s*\{\s*"version":\s*")[\d.]+(")}{$1$ENV{NV}$2}'
fi

# --- Report ---
echo ""
if [ "${#updated[@]}" -gt 0 ]; then
    echo "Updated ${#updated[@]} file(s) to Go $NV:"
    for f in "${updated[@]}"; do echo "  $f"; done
else
    echo "No files needed updating."
fi

if [ "${#skipped[@]}" -gt 0 ]; then
    echo ""
    echo "Already at Go $NV (${#skipped[@]} file(s)):"
    for f in "${skipped[@]}"; do echo "  $f"; done
fi

echo ""
echo "Done. GitHub Actions workflows read the version from cli/azd/go.mod automatically."
echo "Run 'git diff' to review changes before committing."

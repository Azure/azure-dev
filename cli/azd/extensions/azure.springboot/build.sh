#!/usr/bin/env bash
set -euo pipefail

extension_dir="$(cd "$(dirname "$0")" && pwd)"
cd "$extension_dir"

extension_id_safe="${EXTENSION_ID//./-}"
output_dir="${OUTPUT_DIR:-$extension_dir/bin}"
mkdir -p "$output_dir"

if [[ -n "${EXTENSION_PLATFORM:-}" ]]; then
    platforms=("$EXTENSION_PLATFORM")
else
    platforms=(
        "windows/amd64"
        "windows/arm64"
        "darwin/amd64"
        "darwin/arm64"
        "linux/amd64"
        "linux/arm64"
    )
fi

for platform in "${platforms[@]}"; do
    target_os="${platform%/*}"
    target_arch="${platform#*/}"
    output_name="$output_dir/$extension_id_safe-$target_os-$target_arch"
    if [[ "$target_os" == "windows" ]]; then
        output_name+=".exe"
    fi

    GOOS="$target_os" GOARCH="$target_arch" go build \
        -ldflags="-X 'azurespringboot/internal/version.Version=$EXTENSION_VERSION'" \
        -o "$output_name" \
        .
done

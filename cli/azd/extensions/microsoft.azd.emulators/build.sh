#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

set -euo pipefail
cd "$(dirname "$0")"

output_dir="${OUTPUT_DIR:-$PWD/bin}"
extension_id="${EXTENSION_ID:-microsoft.azd.emulators}"
binary_name="${extension_id//./-}"
mkdir -p "$output_dir"

platforms=("windows/amd64" "windows/arm64" "darwin/amd64" "darwin/arm64" "linux/amd64" "linux/arm64")
if [[ -n "${EXTENSION_PLATFORM:-}" ]]; then
    platforms=("$EXTENSION_PLATFORM")
fi

for platform in "${platforms[@]}"; do
    os="${platform%/*}"
    arch="${platform#*/}"
    output="$output_dir/$binary_name-$os-$arch"
    if [[ "$os" == "windows" ]]; then
        output+=".exe"
    fi
    echo "Building $platform..."
    CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -o "$output" .
done

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

ldflags="-X 'azurespringboot/internal/version.Version=$EXTENSION_VERSION'"
if [[ -n "${POSTGRES_BOOTSTRAP_IMAGE:-}" ]]; then
    if [[ ! "$POSTGRES_BOOTSTRAP_IMAGE" =~ ^mcr\.microsoft\.com/azure-dev/azd-spring-postgres-bootstrap@sha256:[0-9a-f]{64}$ ]]; then
        echo "POSTGRES_BOOTSTRAP_IMAGE must be the digest-pinned Microsoft-owned MCR image." >&2
        exit 1
    fi
    ldflags+=" -X 'azurespringboot/internal/spring.postgresBootstrapImage=$POSTGRES_BOOTSTRAP_IMAGE'"
fi

for platform in "${platforms[@]}"; do
    target_os="${platform%/*}"
    target_arch="${platform#*/}"
    output_name="$output_dir/$extension_id_safe-$target_os-$target_arch"
    if [[ "$target_os" == "windows" ]]; then
        output_name+=".exe"
    fi

    GOOS="$target_os" GOARCH="$target_arch" go build \
        -ldflags="$ldflags" \
        -o "$output_name" \
        .
done

#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

# Run from any directory. Build the real package and require the specific missing-method error.
set -euo pipefail
package_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
module_dir="$(cd -- "$package_dir/../.." && pwd)"
temp_dir="$(mktemp -d "$module_dir/internal/envkeys-compile-fail.XXXXXX")"
trap 'rm -rf -- "$temp_dir"' EXIT
module="$(cd -- "$module_dir" && go list -m)"

cat > "$temp_dir/bad.go" <<GO
package bad
import (
    "context"
    "$module/internal/envkeys"
)
func bad(s envkeys.Store) error {
    return envkeys.ContainerRegistryResourceID.Set(context.Background(), s, "x")
}
GO

if output="$(cd -- "$temp_dir" && go build . 2>&1)"; then
    echo "FAIL: Input.Set compiled" >&2
    exit 1
fi
if ! grep -Fq 'ContainerRegistryResourceID.Set undefined' <<< "$output"; then
    printf 'FAIL: compilation failed for an unexpected reason:\n%s\n' "$output" >&2
    exit 1
fi
echo "OK: Input.Set does not compile"

#!/usr/bin/env bash
# Copyright (c) Microsoft Corporation. All rights reserved.
# Licensed under the MIT License.

# Ratchet quoted Foundry keys lexically, including test fixtures and quoted comments.
# Run from any directory. --counts prints KEY=COUNT lines for baseline maintenance.
set -euo pipefail
script_dir="$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)"
extensions_dir="$(cd -- "$script_dir/.." && pwd)"
baseline="$script_dir/check_envkey_literals.baseline"

if [[ $# -gt 1 || ( $# -eq 1 && "$1" != "--counts" ) ]]; then
    echo "Usage: $0 [--counts]" >&2
    exit 2
fi
if ! command -v rg > /dev/null; then
    echo "ERROR: ripgrep (rg) is required" >&2
    exit 2
fi

declare -A counts
keys=()
cd -- "$extensions_dir"
for extension in azure.ai.agents azure.ai.projects; do
    for prefix in AZURE_AI_ AZD_FOUNDRY_ FOUNDRY_ AI_PROJECT_ AGENT_ AZURE_CONTAINER_REGISTRY_; do
        key="${extension}:${prefix}"
        keys+=("$key")
        if matches="$(rg --no-config --no-ignore --hidden --no-heading --only-matching \
            --glob '*.go' \
            --glob '!**/k8s/**' \
            --glob '!**/internal/envkeys/**' \
            --glob '!**/vendor/**' \
            "[\x22\x60]${prefix}[A-Z0-9_]*[\x22\x60]" \
            "$extension")"; then
            counts["$key"]="$(printf '%s\n' "$matches" | wc -l | tr -d '[:space:]')"
        else
            status=$?
            if [[ $status -ne 1 ]]; then
                echo "ERROR: scanning $extension failed (rg exit $status)" >&2
                exit "$status"
            fi
            counts["$key"]=0
        fi
    done
done

if [[ "${1:-}" == "--counts" ]]; then
    for key in "${keys[@]}"; do
        printf '%s=%s\n' "$key" "${counts[$key]}"
    done
    exit 0
fi

declare -A limits
while IFS= read -r line || [[ -n "$line" ]]; do
    [[ -z "$line" || "$line" == \#* ]] && continue
    if [[ ! "$line" =~ ^([^=]+)=([0-9]+)$ ]]; then
        echo "ERROR: invalid baseline line: $line" >&2
        exit 2
    fi
    key="${BASH_REMATCH[1]}"
    if [[ -z "${counts[$key]+exists}" || -n "${limits[$key]+exists}" ]]; then
        echo "ERROR: unknown or duplicate baseline key: $key" >&2
        exit 2
    fi
    limits["$key"]="${BASH_REMATCH[2]}"
done < "$baseline"

failed=0
for key in "${keys[@]}"; do
    if [[ -z "${limits[$key]+exists}" ]]; then
        echo "ERROR: missing baseline count for $key" >&2
        exit 2
    fi
    actual="${counts[$key]}"
    limit="${limits[$key]}"
    if (( actual > limit )); then
        echo "FAIL: $key increased from $limit to $actual; use internal/envkeys" >&2
        failed=1
    elif (( actual < limit )); then
        echo "NOTE: $key decreased from $limit to $actual; the baseline can be lowered"
    fi
done
if (( failed )); then
    exit 1
fi
echo "OK: Foundry environment-key literal counts are within baseline"

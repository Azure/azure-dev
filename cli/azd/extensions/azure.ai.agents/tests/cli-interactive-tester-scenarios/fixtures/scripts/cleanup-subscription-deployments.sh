#!/usr/bin/env bash

set -u

values="$(azd env get-values 2>/dev/null)" || {
    echo "WARNING: Could not read the azd environment; skipping subscription deployment cleanup."
    exit 0
}

subscription_id="$(printf '%s\n' "$values" | sed -n 's/^AZURE_SUBSCRIPTION_ID="\([^"]*\)"$/\1/p')"
environment_name="$(printf '%s\n' "$values" | sed -n 's/^AZURE_ENV_NAME="\([^"]*\)"$/\1/p')"
if [ -z "$subscription_id" ] || [ -z "$environment_name" ]; then
    echo "WARNING: Subscription or environment is unavailable; skipping subscription deployment cleanup."
    exit 0
fi

script_dir="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
command_dir="$(cd "$script_dir/../../../../../.." && pwd)/test/cmd/cleanup-subscription-deployments"
if ! (cd "$command_dir" && go run . --subscription "$subscription_id" --environment "$environment_name"); then
    echo "WARNING: Could not request subscription deployment cleanup."
fi

exit 0

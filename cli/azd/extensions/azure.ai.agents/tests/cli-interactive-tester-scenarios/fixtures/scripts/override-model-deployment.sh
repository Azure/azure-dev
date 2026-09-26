#!/usr/bin/env bash

set -euo pipefail

model="${1-}"
model_version="${2-}"
model_sku="${3-}"

if [ -z "$model" ] || [ -z "$model_sku" ]; then
  echo "Skipping managed model deployment override because model or SKU is empty."
  exit 0
fi

deployment_name="$(azd env get-value AZURE_AI_MODEL_DEPLOYMENT_NAME)" || {
  echo "ERROR: Could not resolve AZURE_AI_MODEL_DEPLOYMENT_NAME." >&2
  exit 1
}
if [ -z "$deployment_name" ]; then
  echo "ERROR: AZURE_AI_MODEL_DEPLOYMENT_NAME is empty." >&2
  exit 1
fi

deployment_args=(
  --model "$model"
  --name "$deployment_name"
  --sku "$model_sku"
)
if [ -n "$model_version" ]; then
  deployment_args+=(--version "$model_version")
fi

result="$(azd ai project deployment add "${deployment_args[@]}" \
  --force --no-prompt --output json)" || {
  echo "ERROR: Could not replace managed model deployment $deployment_name." >&2
  exit 1
}

python3 - "$deployment_name" "$model" "$model_version" "$model_sku" "$result" <<'PY'
import json
import sys

try:
    data = json.loads(sys.argv[5])
    actual = (
        data["deploymentName"],
        data["model"]["name"],
        data["sku"]["name"],
    )
    resolved_version = data["model"]["version"]
    mutation = data["mutation"]
except (json.JSONDecodeError, KeyError, TypeError) as error:
    print("ERROR: Invalid deployment command output: %s" % error, file=sys.stderr)
    sys.exit(1)

expected = (sys.argv[1], sys.argv[2], sys.argv[4])
if actual != expected:
    print(
        "ERROR: Deployment command output mismatch: expected %r, got %r"
        % (expected, actual),
        file=sys.stderr,
    )
    sys.exit(1)

expected_version = sys.argv[3]
if not isinstance(resolved_version, str) or not resolved_version:
    print("ERROR: Deployment command output has no model version.", file=sys.stderr)
    sys.exit(1)
if expected_version and resolved_version != expected_version:
    print(
        "ERROR: Deployment command output version mismatch: expected %r, got %r"
        % (expected_version, resolved_version),
        file=sys.stderr,
    )
    sys.exit(1)
if mutation not in ("replaced", "unchanged"):
    print(
        "ERROR: Deployment command output has unexpected mutation %r." % mutation,
        file=sys.stderr,
    )
    sys.exit(1)
PY

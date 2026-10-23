#!/usr/bin/env bash
# Fail if any .go file under this module imports azd core or extension packages.
set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT"

# Match quoted import paths only so go.mod/doc comments mentioning cli/azd are ignored.
if grep -RIn --include='*.go' -E '"github\.com/azure/azure-dev/cli/azd("|/)' .; then
  echo "::error::cli/shared/foundry must not import github.com/azure/azure-dev/cli/azd or cli/azd/extensions/"
  exit 1
fi

echo "Import boundary check passed: no cli/azd imports under cli/shared/foundry."

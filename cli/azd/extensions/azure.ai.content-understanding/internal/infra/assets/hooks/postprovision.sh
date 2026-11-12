#!/usr/bin/env sh
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT license.

# Product policy lives in cu-cli-core. This launcher delegates to the azd extension.
set -e

if [ -z "${AZURE_ENV_NAME:-}" ]; then
  echo "Postprovision requires AZURE_ENV_NAME from the provisioned azd environment." >&2
  exit 1
fi

if command -v azd >/dev/null 2>&1; then
  exec azd ai cu infra _postprovision-v1 --environment "$AZURE_ENV_NAME"
fi

echo "Postprovision requires azd with the Azure Content Understanding extension." >&2
exit 1

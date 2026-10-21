#!/usr/bin/env sh
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT license.

# Product policy lives in cu-cli-core. This launcher delegates to the azd extension.
set -e

if command -v azd >/dev/null 2>&1; then
  exec azd ai cu infra _postprovision-v1
fi

echo "Postprovision requires azd with the Azure Content Understanding extension." >&2
exit 1

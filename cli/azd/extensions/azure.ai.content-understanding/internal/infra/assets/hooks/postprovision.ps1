#!/usr/bin/env pwsh
# Copyright (c) Microsoft Corporation.
# Licensed under the MIT license.

# Product policy lives in cu-cli-core. This launcher delegates to the azd extension.
$ErrorActionPreference = 'Stop'

if ([string]::IsNullOrEmpty($env:AZURE_ENV_NAME)) {
  Write-Error 'Postprovision requires AZURE_ENV_NAME from the provisioned azd environment.'
  exit 1
}

if (Get-Command azd -ErrorAction SilentlyContinue) {
  & azd ai cu infra _postprovision-v1 --environment $env:AZURE_ENV_NAME
  exit $LASTEXITCODE
}

Write-Error 'Postprovision requires azd with the Azure Content Understanding extension.'
exit 1

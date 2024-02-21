# GitHub Action for installing the Azure Developer CLI (`azd`)

This GitHub Action allows you to provision resources and deploy your application on Azure with [Azure Developer CLI](https://github.com/azure/setup-azd) commands.

The action installs the Azure Developer CLI on a user-defined Azure Developer CLI version. If the user does not specify a version, latest CLI version is used. Read more about various Azure Developer CLI versions [here](https://github.com/Azure/azure-dev/releases).

- `version` – **Optional** Example: 1.0.1, Default: set to latest azd cli version.

The definition of this GitHub Action is in [action.yml](https://github.com/azure/setup-azd/blob/main/action.yml).

## Sample workflow install latest `azd` version

```yaml
# File: .github/workflows/azure-dev.yml

on: [push]

jobs:

  build:
    runs-on: ubuntu-latest
    steps:
      - name: Install azd
        uses: Azure/setup-azd@v0.1.0
```

## Sample workflow install a specific `azd` version

Select a specific `azd` version [here](https://github.com/Azure/azure-dev/releases) and use it in `version`.

```yaml
# File: .github/workflows/azure-dev.yml

on: [push]
jobs:
  build:
    runs-on: ubuntu-latest
    steps:
      - name: Install azd
        uses: Azure/setup-azd@v0.1.0
        with:
          version: '<version-number>'
```

## Usage

See [action.yml](action.yml).

```yaml
name: 'setup-azd'
description: 'This action downloads and installs azd'
author: 'Azure Developer CLI Team'
inputs:
  version:
    required: false
    description: 'The version of azd to install (default: latest)'
    default: 'latest'
runs:
  using: 'node20'
  main: 'dist/index.js'
```

## Getting help for Azure Developer CLI issues

If you encounter an issue related to the Azure Developer CLI commands executed in your script, you can file an issue directly on the [Azure Developer CLI repository](https://github.com/Azure/azure-dev/issues/new/choose).

## Contributing

This project welcomes contributions and suggestions.  Most contributions require you to agree to a Contributor License Agreement (CLA) declaring that you have the right to, and actually do, grant us the rights to use your contribution. For details, visit https://cla.opensource.microsoft.com.

When you submit a pull request, a CLA bot will automatically determine whether you need to provide a CLA and decorate the PR appropriately (e.g., status check, comment). Simply follow the instructions provided by the bot. You will only need to do this once across all repos using our CLA.

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/). For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.

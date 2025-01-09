# Azure Developer CLI

Latest builds:

| Artifact  | Version | Download |
| ------- | ------- | -------- |
| azd | ![azd version](https://img.shields.io/endpoint?url=https%3A%2F%2Fazuresdkreleasepreview.blob.core.windows.net%2Fazd%2Fstandalone%2Flatest%2Fshield.json) | [Windows](https://azuresdkreleasepreview.blob.core.windows.net/azd/standalone/latest/azd-windows-amd64.zip) &vert; [Linux](https://azuresdkreleasepreview.blob.core.windows.net/azd/standalone/latest/azd-linux-amd64.tar.gz) &vert; [Mac](https://azuresdkreleasepreview.blob.core.windows.net/azd/standalone/latest/azd-darwin-amd64.zip) |
| vscode extension | ![vscode extension version](https://img.shields.io/endpoint?url=https%3A%2F%2Fazuresdkreleasepreview.blob.core.windows.net%2Fazd%2Fvscode%2Flatest%2Fshield.json) | [VSIX](https://marketplace.visualstudio.com/items?itemName=ms-azuretools.azure-dev) |

The Azure Developer CLI (`azd`) is a developer-centric command-line interface (CLI) tool for creating Azure applications. The goals of the CLI are to:

- reduce the time required for a developer to be productive
- demonstrate opinionated best practices for Azure development
- help developers understand core Azure development constructs

To take full advantage of the CLI, code repositories need to conform to a well defined set of conventions that will be recognized by the tooling. Please checkout the [wiki](https://github.com/Azure/azure-dev/wiki) for more information and to get started. Use [discussions](https://github.com/Azure/azure-dev/discussions) to participate in the conversation, ask questions, and see the latest announcements.

## Install/Upgrade Azure Developer CLI

Install and upgrade using the following scripts. Re-running the script will install the latest available version.

For advanced install scenarios see [Azure Developer CLI Installer Scripts](cli/installer/README.md).

### Windows

#### Windows Package Manager (winget)

```powershell
winget install microsoft.azd
```

#### Chocolatey

```powershell
choco install azd
```

#### Install script

```powershell
powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/install-azd.ps1' | Invoke-Expression"
```
### MacOS

#### Homebrew

```bash
brew tap azure/azd && brew install azd
```

If using `brew` to upgrade `azd` from a version not installed using `brew`, remove the existing version of `azd` using the uninstall script (if installed to the default location) or by deleting the `azd` binary manually.

### Linux

```
curl -fsSL https://aka.ms/install-azd.sh | bash
```

## Set Up Shell Completion

The CLI supports shell completion for `bash`, `zsh`, `fish` and `powershell`.

To learn how to install shell completion for the CLI for your shell, run `azd completion [bash | zsh | fish | powershell] --help`.
For example, to get the instructions for `bash` run `azd completion bash --help`

## Uninstall Azure Developer CLI

### Windows

#### Uninstalling 0.5.0-beta.1 and later

The Azure Developer CLI uses MSI to install on Windows. Use the "Add or remove programs" dialog in Windows to remove the "Azure Developer CLI" application. If installed using a package manager like winget or choco, uninstall using the package manager's uninstall command.

#### Uninstalling version 0.4.0-beta.1 and earlier

Use this PowerShell script to uninstall Azure Developer CLI 0.4.0-beta.1 and earlier.

```powershell
powershell -ex AllSigned -c "Invoke-RestMethod 'https://aka.ms/uninstall-azd.ps1' | Invoke-Expression"
```

### Linux/MacOS

If installed using the script, uninstall using this script.

```
curl -fsSL https://aka.ms/uninstall-azd.sh | bash
```

If installed using a package manager, uninstall using the package manager's uninstall command.

## Data Collection

The software may collect information about you and your use of the software and send it to Microsoft. Microsoft may use this information to provide services and improve our products and services. You may turn off the telemetry as described in the repository. There are also some features in the software that may enable you and Microsoft to collect data from users of your applications. If you use these features, you must comply with applicable law, including providing appropriate notices to users of your applications together with a copy of Microsoft's privacy statement. Our privacy statement is located at https://go.microsoft.com/fwlink/?LinkId=521839. You can learn more about data collection and use in the help documentation and our privacy statement. Your use of the software operates as your consent to these practices.

### Telemetry Configuration

Telemetry collection is on by default.

To opt out, set the environment variable `AZURE_DEV_COLLECT_TELEMETRY` to `no` in your environment.

## Contributing

This project welcomes contributions and suggestions.  Most contributions require you to agree to a
Contributor License Agreement (CLA) declaring that you have the right to, and actually do, grant us
the rights to use your contribution. For details, visit https://cla.microsoft.com.

When you submit a pull request, a CLA-bot will automatically determine whether you need to provide
a CLA and decorate the PR appropriately (e.g., label, comment). Simply follow the instructions
provided by the bot. You will only need to do this once across all repos using our CLA.

Please see our [contributing guide](cli/azd/CONTRIBUTING.md) for complete instructions on how you can contribute to the Azure Developer CLI.

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/).
For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or
contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.

### Contributing as Microsoft template author

Microsoft employees and partners who want to contribute templates to our official collections, must follow the standardization guidelines for template scaffolding and validation published [here](https://github.com/Azure-Samples/azd-template-artifacts)

*Important Disclaimer*: The standardization artifacts, definitions, and recommendations are frequently updated. Please make sure to visit the site often to follow the latest recommended practices.

## Trademark Notice

Trademarks This project may contain trademarks or logos for projects, products, or services. Authorized use of Microsoft trademarks or logos is subject to and must follow Microsoft’s Trademark & Brand Guidelines. Use of Microsoft trademarks or logos in modified versions of this project must not cause confusion or imply Microsoft sponsorship. Any use of third-party trademarks or logos are subject to those third-party’s policies.

## License - Azure Developer CLI Templates Trust Notice
Learn more about running third-party code on [our DevHub](https://learn.microsoft.com/azure/developer/azure-developer-cli/azd-templates#guidelines-for-using-azd-templates)

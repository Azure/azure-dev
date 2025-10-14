# `azd coding-agent` Extension

This azd extension makes it easy to take an existing GitHub repo and add in support for the Copilot Coding Agent to access Azure.

This includes:

- Creating an Azure managed identity, making it simple to enable selective Azure permissions for the Copilot coding agent.
- Updating the Github `copilot` environment to use the created credential.
- Creating a copilot-setup-steps.yml that sets up the Copilot Coding Agent environment.
- Creating required pull requests and guiding you towards any final configuration needed.

## Prerequisites

- The Azure Dev CLI (azd) installed. See here for instructions on installing it: [azdev installation instructions](https://github.com/richardpark-msft/azure-dev/blob/main/README.md#installupgrade-azure-developer-cli).
- An Azure subscription where you have permissions to create resource groups and managed identities.
- A GitHub repository, where you have permissions to:
  - Update the `copilot` Github environment.
  - Update Copilot coding agent settings.
  - Push changes made to the .github/workflows folder.

## Installing

Assuming 'azd' is in your path, run the following commands to install the extension for the first time:

```shell
# install the coding-agent
azd extension install azure.coding-agent
```

Or, if you already the `azure.coding-agent` extension installed, and you want to upgrade to the latest version:

```shell
azd extension upgrade azure.coding-agent
```

## Usage

You can start the installation process, by typing the following in your terminal:

```
cd <azure dev project directory>
azd coding-agent config
```

## Troubleshooting

### The managed identity doesn't have permissions to do 'x'

By default, the `coding-agent` command assigns the Reader role to the created managed identity, scoped to the resource group that was created (or chosen). If you want to add more roles, or expand the scope to more resources you'll need to update the managed identity's assigned roles.

Some further resources:

- [Using the Azure portal to assign roles](https://learn.microsoft.com/azure/role-based-access-control/role-assignments-portal-managed-identity)
- [Using the Azure CLI to assign roles](https://learn.microsoft.com/azure/role-based-access-control/role-assignments-cli)
- [Azure built-in roles](https://learn.microsoft.com/azure/role-based-access-control/built-in-roles)

### An internal command is failing, but there's no command output

Use the `--debug` command line option. This will make it so each command (and it's output) is printed to the console, which can give you a better idea of where the overall process is failing.

## Data Collection

The software may collect information about you and your use of the software and send it to Microsoft. Microsoft may use this information to provide services and improve our products and services. You may turn off the telemetry as described in the repository. There are also some features in the software that may enable you and Microsoft to collect data from users of your applications. If you use these features, you must comply with applicable law, including providing appropriate notices to users of your applications together with a copy of Microsoft's privacy statement. Our privacy statement is located at https://go.microsoft.com/fwlink/?LinkId=521839. You can learn more about data collection and use in the help documentation and our privacy statement. Your use of the software operates as your consent to these practices.

## Contributing

This project welcomes contributions and suggestions. Most contributions require you to agree to a
Contributor License Agreement (CLA) declaring that you have the right to, and actually do, grant us
the rights to use your contribution. For details, visit https://cla.microsoft.com.

When you submit a pull request, a CLA-bot will automatically determine whether you need to provide
a CLA and decorate the PR appropriately (e.g., label, comment). Simply follow the instructions
provided by the bot. You will only need to do this once across all repos using our CLA.

Please see our [contributing guide](../../../../cli/azd/CONTRIBUTING.md) for complete instructions on how you can contribute to the Azure Developer CLI.

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/).
For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or
contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.

## Trademark Notice

Trademarks This project may contain trademarks or logos for projects, products, or services. Authorized use of Microsoft trademarks or logos is subject to and must follow Microsoft’s Trademark & Brand Guidelines. Use of Microsoft trademarks or logos in modified versions of this project must not cause confusion or imply Microsoft sponsorship. Any use of third-party trademarks or logos are subject to those third-party’s policies.

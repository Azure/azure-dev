# Azure Developer CLI Visual Studio Code extension

This extension makes it easier to run, create Azure Resources, and deploy Azure applications with the Azure Developer CLI.

## Features

### 🚀 Deployment Commands

- **Initialize** (`azd init`) - Scaffold a new application from a template
- **Provision** (`azd provision`) - Create Azure infrastructure resources
- **Deploy** (`azd deploy`) - Deploy your application code to Azure
- **Up** (`azd up`) - Provision and deploy in one command
- **Monitor** (`azd monitor`) - View Application Insights for your deployed app
- **Down** (`azd down`) - Delete Azure resources and deployments

### 📝 Enhanced azure.yaml Editing

Intelligent editing support for your `azure.yaml` configuration files:


- **Auto-Completion** - Smart suggestions for service properties, host types, and lifecycle hooks
- **Hover Documentation** - Inline help with examples for all azure.yaml properties
- **Quick Fixes** - One-click solutions for common issues:
  - Create missing project folders
  - Add missing language or host properties
  - Fix invalid configurations
- **Validation** - Real-time diagnostics for:
  - Missing or invalid project paths
  - Invalid host types
  - Missing recommended properties
  - Configuration best practices

### 🌲 View Panels

- **My Project** - View your azure.yaml configuration and services
- **Environments** - Manage development, staging, and production environments
- **Template Tools** - Discover and initialize projects from templates
  - **Quick Start** (shown when no azure.yaml exists) - Initialize from existing code or create minimal project
  - **Browse by Category** - Explore templates organized by type (AI, Web Apps, APIs, Containers, Databases, Functions)
  - **AI Templates** - Quick access to AI-focused templates from [aka.ms/aiapps](https://aka.ms/aiapps)
  - **Search Templates** - Find templates by name, description, or tags
  - **Template Gallery** - Open the full [awesome-azd](https://aka.ms/awesome-azd) gallery in browser
- **Extensions** - Browse and manage Azure Developer CLI extensions
- **Help and Feedback** - Quick access to documentation and support

### 🔄 Environment Management

- Create, select, and delete environments
- View environment variables
- Refresh environment configuration from deployments
- Compare environments (coming soon)

### 🔗 Azure Integration

- Navigate directly to Azure resources from VS Code
- Open resources in Azure Portal
- View resource connection strings
- Integration with Azure Resources extension

## What It Does

For more information about Azure Developer CLI and this VS Code extension, please [see the documentation](https://aka.ms/azure-dev/vscode).

## Getting Started

1. Install the [Azure Developer CLI](https://aka.ms/azure-dev/install)
2. Open a folder containing an `azure.yaml` file, or create a new project with `azd init`
3. Right-click `azure.yaml` and select deployment commands from the context menu
4. Use the Azure Developer CLI view panel for quick access to all features

## Requirements

- [Azure Developer CLI](https://aka.ms/azure-dev/install) version 1.0.0 or higher
- [VS Code](https://code.visualstudio.com/) version 1.90.0 or higher

## Extension Settings

This extension contributes the following settings:

- `azure-dev.maximumAppsToDisplay`: Maximum number of Azure Developer CLI apps to display in the Workspace Resource view (default: 5)
- `azure-dev.auth.useIntegratedAuth`: Use VS Code integrated authentication with the Azure Developer CLI (alpha feature)

## Keyboard Shortcuts

Access Azure Developer CLI commands quickly:

- Open Command Palette (`Cmd+Shift+P` or `Ctrl+Shift+P`)
- Type "Azure Developer CLI" to see all available commands

## Tell Us What You Think

- [Give us a thumbs up or down](https://aka.ms/azure-dev/hats). We want to hear good news, but bad news are even more important!
- Use [Discussions](https://aka.ms/azure-dev/discussions) to share new ideas or ask questions about Azure Developer CLI and the VS Code extension.
- To report problems [file an issue](https://aka.ms/azure-dev/issues).

## Code of Conduct

This project has adopted the [Microsoft Open Source Code of Conduct](https://opensource.microsoft.com/codeofconduct/). For more information see the [Code of Conduct FAQ](https://opensource.microsoft.com/codeofconduct/faq/) or contact [opencode@microsoft.com](mailto:opencode@microsoft.com) with any additional questions or comments.

## Telemetry

VS Code collects usage data and sends it to Microsoft to help improve our products and services. Read our [privacy statement](https://go.microsoft.com/fwlink/?LinkID=528096&clcid=0x409) to learn more. If you don’t wish to send usage data to Microsoft, you can set the `telemetry.telemetryLevel` setting to `off`. Learn more about telemetry handling in [VS Code FAQ](https://code.visualstudio.com/docs/supporting/faq#_how-to-disable-telemetry-reporting).

## Contributing

See [the contribution guidelines](CONTRIBUTING.md) for ideas and guidance on how to improve the extension. Thank you!

## License

[MIT](https://github.com/Azure/azure-dev/LICENSE.md)


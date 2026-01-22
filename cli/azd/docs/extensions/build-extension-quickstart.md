---
title: Build an Azure Developer CLI (azd) extension
description: Learn how to create, build, test, and publish a custom extension for the Azure Developer CLI using the azd extension framework.
author: alexwolfmsft
ms.author: alexwolf
ms.date: 01/06/2026
ms.service: azure-dev-cli
ms.topic: tutorial
ms.custom: devx-track-azdevcli, devx-track-go
---

# Build an Azure Developer CLI extension

The Azure Developer CLI (`azd`) extension framework allows you to extend `azd`'s functionality with custom commands, lifecycle event hooks, and integration with external services. In this tutorial, you'll learn how to scaffold, build, and test a custom `azd` extension.

## Prerequisites

- **Azure Developer CLI**: You need `azd` version 1.12.0 or later.
- **Go (Golang)**: This tutorial uses Go, which currently has the best support and performance for `azd` extensions. Ensure you have [Go installed](https://go.dev/doc/install) (version 1.21+ recommended).
- **PowerShell 7 (pwsh)**: Required on Windows to run the extension build scripts. [Install PowerShell](/powershell/scripting/install/installing-powershell-on-windows).
- **Git**: Required for version control and publishing workflows.

> [!NOTE]
> `azd` extensions are currently in beta and the API is subject to change.

## Install the developer tools

The `microsoft.azd.extensions` extension simplifies scaffolding, building, packing, and releasing extensions (extension lifecycle).

Install the developer extension:

```bash
azd extension install microsoft.azd.extensions
```

Once installed, access the new development commands under the `azd x` namespace.

## Initialize a new extension

The `microsoft.azd.extensions` extension includes an `init` command that scaffolds a new extension project for you.

1. Create a new directory for your extension:

    ```bash
    mkdir sample-extension
    cd sample-extension
    ```

1. The `init` command automatically scaffolds the file structure, builds the initial binary, packages it, and installs it locally for immediate testing.

    Run the `init` command:

    ```bash
    azd x init
    ```

1. Follow the interactive prompts to provide information about your extension, such as the following sample values:

    ```output
    ? Enter a unique identifier for your extension: sample.extension
    ? Enter a display name for your extension: Sample extension
    ? Enter a description for your extension: This is a sample extension
    ? Enter tags for your extension (comma-separated): sampleTag
    ? Enter a namespace for your extension: sample
    ? Select capabilities for your extension: Custom Commands
    ? Select a programming language for your extension: Go
      > Go
        C#
        JavaScript
        Python
    ```

## Explore the project structure

After initialization, your directory contains the following key files:

| File/Directory | Description |
|----------------|-------------|
| `extension.yaml` | The manifest file defining metadata, capabilities, and settings. |
| `main.go` | The entry point for your Go extension. |
| `go.mod` / `go.sum` | Go module definitions and dependencies. |
| `bin/` | Contains the compiled binaries after a build. |

### The extension manifest (`extension.yaml`)

Open `extension.yaml`. This file tells `azd` how to load and interact with your extension.

```yaml
capabilities:
    - custom-commands
description: My sample extension
displayName: Sample extension
id: sample.extension
language: go
namespace: sample
usage: azd sample <command> [options]
version: 0.0.1
```

Key fields:

- **namespace**: Determines the top-level command group (`azd sample`).
- **capabilities**: Extensions can declare the following capabilities in their manifest:
  - `custom-commands`: Expose new command groups and commands to `azd`.
  - `lifecycle-events`: Subscribe to `azd` project and service lifecycle events.
  - `mcp-server`: Provide Model Context Protocol (MCP) tools for AI agents.
  - `service-target-provider`: Provide custom service deployment targets.
  - `framework-service-provider`: Provide custom language frameworks and build systems.
- **usage**: Describes the syntax used to invoke the extension commands.
- **language**: Describes the programming language used to build the extension.

## Add a custom command

Custom commands allow you to add new keywords to `azd` for custom operations. In this example, you create a command to print a hello world message from your extension.

1. Open `main.go`.

1. Add a new function at the bottom of the file to define a command.

    ```go
    func newHelloCommand() *cobra.Command {
        return &cobra.Command{
            Use:   "hello",
            Short: "Prints a hello message",
            RunE: func(cmd *cobra.Command, args []string) error {
                fmt.Println("Hello from the sample extension!")
                return nil
            },
        }
    }
    ```

1. Update the import statment to include `github.com/spf13/cobra` and `fmt`.

1. Register this command in the `main` function.

    ```go
    func main() {
        // Execute the root command
        ctx := context.Background()
        rootCmd := cmd.NewRootCommand()
        rootCmd.AddCommand(newHelloCommand())
        
        if err := rootCmd.ExecuteContext(ctx); err != nil {
            color.Red("Error: %v", err)
            os.Exit(1)
        }
    }
    ```

## Build and test locally

To see your changes, you need to rebuild the extension.

1. Rebuild and reinstall the extension locally:

    ```bash
    azd x build
    ```

    This compiles your Go code and updates the local extension installation.

1. Test your custom command:

    ```bash
    azd sample hello
    ```

    You should see the following message printed: `Hello from the sample extension!`

## Watch mode

During active development, use watch mode to automatically rebuild and reinstall when you change files:

```bash
azd x watch
```

## Next steps

- Learn more about the [Extension Manifest schema](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/extension.schema.json).
- Explore [Publishing your extension](https://github.com/Azure/azure-dev/blob/main/cli/azd/docs/extension-framework.md#publishing-workflow) to share it with your team or the world.

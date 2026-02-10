# `azd` Demo Extension

An AZD Demo extension with custom commands showcasing current capabilities of the `azd` extension framework.

## Installation

Run `azd ext install microsoft.azd.demo`

## Commands

### `context`

Run `context` within an initialized `azd` project.
The command will show metadata about the current project, selected environment and any deployed resources.

#### Usage: `azd demo context`

### `prompt`

Run the `prompt` command to see an example of using `azd` styled prompts within your application written in any language.
This features UX as a service via gRPC.

#### Usage: `azd demo prompt`

#### PromptSubscription

Displays a single select prompt allowing selection of an Azure subscription based on the current logged in user.

#### PromptLocation

Displays a single select prompt allowing selection of a list of Azure locations for the current logged in user and subscription.

#### PromptResourceGroup

Displays a single select allowing the selection of Azure resource groups for the current logged in user and subscription.

#### Confirm

Display a yes/no prompt for a boolean value

#### Prompt

Displays a standard text input prompt for options.

#### Select

Displays a single select prompt with configurable choices.

#### MultiSelect

Displays a multi select prompt with configurable choices.

#### PromptSubscriptionResource

Displays a list of Azure resources based on the current logged in user, and subscription filtered by resource type and kind.

#### PromptResourceGroupResource

Displays a list of Azure resources based on the current logged in user, subscription and resource group filtered by resource type and kind.

### `ai`

The `ai` command demonstrates AI model catalog, deployment selection, and quota capabilities through interactive flows.

#### Usage: `azd demo ai <command>`

#### `azd demo ai models`

Browse available AI models interactively and view model details, including locations, versions, SKUs, and capacity constraints.

#### `azd demo ai deployment`

Select model/version/SKU/capacity and resolve a valid deployment configuration.

#### `azd demo ai quota`

View usage meters and limits for a selected location.

### `metadata`

The `metadata` command demonstrates the metadata capability, which provides command structure and configuration schemas.

#### Usage: `azd demo metadata`

This command generates JSON metadata including:
- **Command Tree**: All available commands, subcommands, flags, and arguments with descriptions
- **Configuration Schemas**: Type-safe JSON schemas for project and service-level configuration

Example configuration in `azure.yaml`:

```yaml
extensions:
  demo:
    project:
      enableColors: true
      maxItems: 20
      labels:
        team: "platform"
        env: "dev"
    
services:
  web:
    extensions:
      demo:
        service:
          endpoint: "https://api.example.com"
          port: 8080
          environment: "staging"
          healthCheck:
            enabled: true
            path: "/health"
            interval: 30
```

The metadata capability enables:
- IDE autocomplete and validation for extension configuration
- Automatic help generation
- Configuration validation before deployment
- Better discoverability of extension features

### `listen`

This `listen` command is required when your extension leverages `LifecycleEvents` capability.

#### Usage: `azd demo listen`

This command is invoked by `azd` to allow your extension to subscribe to lifecycle events within the current `azd` project and services.

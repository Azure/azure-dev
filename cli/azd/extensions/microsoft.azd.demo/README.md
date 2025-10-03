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

### `listen`

This `listen` command is required when your extension leverages `LifecycleEvents` capability.

#### Usage: `azd demo listen`

This command is invoked by `azd` to allow your extension to subscribe to lifecycle events within the current `azd` project and services.
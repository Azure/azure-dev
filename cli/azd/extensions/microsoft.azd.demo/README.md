# `azd` Demo Extension

An azd Demo extension with custom commands showcasing current capabilities of the `azd` extension framework.

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

#### Secret prompt

The first prompt in the flow is an optional API key with `Secret: true` set on `PromptOptions`. The typed value is rendered as asterisks in the terminal and hint keys (`?` / escape) are accepted as regular input characters so they can be part of the secret.

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

The demo uses beta service handlers for `predeploy` and `postdeploy`. They
return structured info and warning messages that azd displays after the
deployment table and includes in deploy/up JSON results. Other demo lifecycle
handlers continue to use the stable extension host.

### Input provider (beta)

The demo also declares `input-provider` and registers
`ExtensionHost.WithBetaInputProvider`. Build it with this branch's azd SDK and
use azd 1.38.0-beta.1 or later (`requiredAzdVersion: ">=1.38.0-0"`).
This sample shares the core Go module; it has no separate SDK dependency.

With the locally built demo installed, run `azd up` in an existing template:

- If no environment name or default environment is selected, the demo offers
  `demo` and `demo-dev`, recommending `demo`. Core creates and selects the environment
  through its normal lifecycle.
- If the Bicep infrastructure needs a missing location input, the demo offers
  `eastus2` and `westus3`, recommending `eastus2`, or offers the descriptor's allowed
  list and recommends its first entry. Core filters options against subscription locations and parameter
  constraints. Parameters mapped to `AZURE_LOCATION` persist the accepted value
  in the environment.

No `azure.yaml` input declarations are needed. Explicit flags, existing environment
values, parameter-file values, saved parameters, and native parameter defaults
remain authoritative. A template with no location requirement does not query
the demo for a location. Authentication and subscription selection are unchanged.
The same resolution runs before missing-input errors in `--no-prompt` mode.
Core accepts a sole valid option or unique recommendation automatically; if several
options remain without a unique recommendation, it prompts or fails under `--no-prompt`.
Providers can mark their list `required` to intersect it with other providers' required
sets; the demo's lists are suggestions, not hard constraints.

This is deliberately a static-options sample, not a quota-aware recommendation service.
It does not create resources or generate infrastructure.

### `telemetry`

The `telemetry` command demonstrates the telemetry service, which lets an extension report its own usage events into `azd`'s telemetry pipeline.

The demo uses stable `Telemetry().ReportUsage` and `azdext.ReportUsageRequest`.
It requires azd 1.36.0 or later. It shares the core Go module rather than
pinning a separate SDK dependency; other extensions should pin the published
azd SDK to v1.36.0 or later when adopting this stable API.

#### Usage: `azd demo telemetry`

The command reports a single `demo.telemetry.reported` event with two fixed, low-cardinality attributes and prints whether the host recorded it.

This example shows two things:

- **Every value is a fixed enum.** Resource names, paths, prompts, and anything a user typed are customer content and must never be sent.
- **Telemetry is best effort.** `ReportUsage` errors are logged and shown as warnings without failing the command. `Accepted: false` is also non-fatal when the host drops an event because the configured source does not match the verified official `azd` registry or the per-invocation event budget is spent.

See [Extension Telemetry](../../docs/extensions/extension-telemetry.md) for the full author guide.

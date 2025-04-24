# Extension Framework

Table of Contents

- [Getting Started](#getting-started)
- [Managing Extensions](#managing-extensions)
- [Developing Extensions](#developing-extensions)
  - [Capabilities](#capabilities)
  - [Developer Artifacts](#developer-artifacts)
  - [gRPC Services](#grpc-services)
    - [Project Service](#project-service)
    - [Environment Service](#environment-service)
    - [User Config Service](#user-config-service)
    - [Deployment Service](#deployment-service)
    - [Prompt Service](#prompt-service)
    - [Event Service](#event-service)
    - [Compose Service](#compose-service)
    - [Workflow Service](#workflow-service)

## Getting Started

`azd` extensions are currently an alpha feature within `azd.

> [!IMPORTANT]
> Enable `azd` extensions alpha feature by running `azd config set alpha.extensions on`

- Initially official extensions will start shipping at //BUILD 2025.
- Official extensions must be developed in a fork of the [azure/azure-dev](https://github.com/azure/azure-dev) github repo.
- Extension binaries are shipped as Github releases to the same repo through our official pipelines.

## Managing Extensions

### Extension Sources

Extension sources are file or URL based manifests that provide a registry of available `azd` extensions.
Users can add custom extension sources that connect to private, local, or public registries.
Extension sources are an equivalent concept to Nuget or NPM feeds.

Extension registries must adhere to the [official extension registry schema](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/registry.schema.json).

#### Official Registry

The official extension source registry is pre-configured in `azd` and is hosted at [https://aka.ms/azd/extensions/registry](https://aka.ms/azd/extensions/registry).

The registry is hosted in the [`azd` github repo](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/registry.json).

#### Development Registry

> [!CAUTION]
> Extensions hosted in the dev registry DO NOT contain signed binaries at the moment.

A shared development registry can be added to your `azd` configuration.
This registry contains extensions that are experiments and also used for internal testing before shipping official extensions.

To opt-in for the development registry run the following command:

```bash
# Add a new extension source name 'dev' to your `azd` configuration.
azd extension source add -n dev -t url -l "https://aka.ms/azd/extensions/registry/dev"
```

#### `azd extension source list`

Displays a list of installed extension sources.

#### `azd extension source add [flags]`

Adds a new named extension source to the global `azd` configuration.

- `-l, --location` The location of the extension source.
- `-n, --name` The name of the extension source.
- `-t, --type` The type of extension source. Supported types are `file` and `url`.

#### `azd extension source remove <name>`

Removes an extension source with the specified named argument

### Extension Management

Extensions are a collection of executable artifacts that extend or enhance functionality within `azd`.

#### `azd extension list [flags]`

Lists matching extensions from one or more extension sources

- `--installed` When set displays a list of installed extensions.
- `--source` When set will only list extensions from the specified source.
- `--tags` Allows filtering extensions by tags (e.g., AI, test)

#### `azd extension install <extension-names> [flags]`

Installs one or more extensions from any configured extension source.

- `-v, --version` Specifies the version constraint to apply when installing extensions. Supports any semver constraint notation.
- `-s, --source` Specifies the extension source used for installations.

#### `azd extension uninstall <extension-names> [flags]`

Uninstalls one or more previously installed extensions.

- `--all` Removes all installed extensions when specified.

#### `azd extension upgrade <extension-names>`

Upgrades one or more extensions to the latest versions.

- `--all` Upgrades all previously installed extensions when specified.
- `-v, --version` Upgrades a specified extension using a semver version constraint, if provided.
- `-s, --source` Specifies the extension source used for installations.

## Developing Extensions

The following guide will help you develop and ship extensions for `azd`.

### Prerequisites

1. [Create a fork](https://github.com/Azure/azure-dev/fork) of the `azure/azure-dev` repo for extension development.
2. Clone your forked repo or open in a codespace.
3. Navigate to the `cli/azd/extensions` directory in your favorite terminal.
4. Install the `azd` [Developer Extension](#developer-extension)

### Supported Languages

`azd` extensions can be built in any programming language but starter templates are included for the following:

- Go (Best support)
- Dotnet (C#)
- Python
- JavaScript (Coming Soon)
- TypeScript (Common Soon)

### Developer Extension

The easiest way to get started building extensions is to install the `azd` Developer extension.

> [!IMPORTANT]
> Ensure you have added the `dev` extension source to your `azd` configuration
>
> [Configure dev extension source](#development-registry)

```bash
# Install the `azd` developer extension
azd extension install microsoft.azd.extensions
```

Once installed the extension registers a suite of commands under the `x` namespace.

> [!NOTE]
> Having issues or have some ideas to improve the `azd` developer extension?
>
> Just [log an issue](https://github.com/Azure/azure-dev/issues) in the `azure/azure-dev` repo.

#### Commands

`init` - Initialize a new extension project.

> [!TIP]
> You'll typically want to run this command inside the `azd/cli/extensions` directory.

Usage: `azd x init`

- Collects information for the extension and scaffolds and extension in a specified language of choice.
- Creates local extension source if it doesn't already exist
- Builds initial binaries for extension
- Packs extension
- Publishes extension to local extension source
- Installs the extension locally for immediate use

---

`build` - Build the binary for the extension.

Usage: `azd x build`

- `--cwd` - The extension directory, defaults to `.`.
- `--all` - Builds binaries for all supported operating systems and architecture.
- `--output, -o` - Path to the output directory, defaults to `./bin`.
- `--skip-install` - When skips local installation after successful build.

---

`watch` - Watches the extension directory for changes and automatically rebuilds and installs extension

Usage: `azd x watch`

- `--cwd` - The extension directory, defaults to `.`.

---

`pack` - Package your extension to prepare for publishing.

Usage: `azd x pack`

- `--cwd` - The extension directory, defaults to `.`.
- `--input, -i` - Path to the input directory that contains binary files.
- `--output, -o` - Path to the artifacts output directory, defaults to local `azd` artifacts path, `~/.azd/registry`.
- `--rebuild` - When set forces a rebuild before packaging.

---

`release` - Create a new Github release for the extension.

Usage: `azd x release --repo {owner}/{name}`

- `--cwd` - The extension directory, defaults to `.`.
- `--artifacts` - Path to the artifacts to upload for the release, defaults to local `azd` artifacts path, `~/.azd/registry`.
- `--repo` - The Github repo name in `{owner}/{repo}` format.
- `--title, -t` - The name of the release, defaults to extension name plus version.
- `--prerelease` - When set marks the release as a prerelease.
- `--draft, -d` - When set marks the release a draft
- `--notes, -n` - The release notes for the release, defaults to using contents of `changelog.md` within extension directory.
- `--version, -v` - The version of the release, defaults to extension version from extension manifest
- `--confirm` - When set bypasses confirmation prompts before release

---

`publish` - Updates the extension source registry with new metadata.

Usage: `azd x publish --repo {owner}/{name}`

- `--cwd` - The extension directory, defaults to `.`.
- `--registry, -r` - The path to the registry.json file to update, defaults to local extension registry
- `--repo` - The Github repo name in `{owner}/{repo}` format.
- `--version, -v` - The version of the release, defaults to extension version from extension manifest

---

### Capabilities

#### Current Capabilities (May 2025)

The following lists the current capabilities of `azd extensions`.

##### Extension Commands

> Extensions must declare the `custom-commands` capability in their `extension.yaml` file.

Extensions can register commands under a namespace or command group within `azd`.
For example, installing the AI extension adds a new `ai` command group.

##### Lifecycle Hooks

> Extensions must declare the `lifecycle-events` capability in their `extension.yaml` file.

Extensions can subscribe to project and service lifecycle events (both pre and post events) for:

- build
- restore
- package
- provision
- deploy

Your extension _**must**_ include a `listen` command to subscribe to these events.
`azd` will automatically invoke your extension during supported commands to establish bi-directional communication.

#### Future Considerations

Future ideas include:

- Registration of pluggable providers for:
  - Language support (e.g., Go)
  - New Azure service targets (e.g., VMs, ACI)
  - Infrastructure providers (e.g., Pulumi)
  - Source control providers (e.g., GitLab)
  - Pipeline providers (e.g., TeamCity)

---

### Developer Workflow

The following are the most common developer workflow for developing extensions.

#### Creating a new Extension

1. Run `azd x init` to start a new extension

#### Developing an Extension

1. Navigate to the extension folder `azd/cli/extensions/{EXTENSION_ID}`
1. Run `azd x watch` during development to automatically build and install local updates
1. Run `azd x build` incrementally build & install local version

#### Validate, Release and Publish development extension

1. Create a new feature branch for the extension `git checkout -b <branch-name>`
1. Modify the extension code as needed
1. Update the version parameter within the `extension.yaml` file.
1. Run `azd x pack --rebuild`
1. Run `azd x release --prerelease` to create new Github release for the extension
1. Run `azd x publish --registry ../registry.json` to publish the new version metadata into the registry
1. Commit and push the changes to the forked repo.

To share the forked registry with others just provide the raw github link to the `azd/cli/extensions/registry.json` file.

#### Validate, Release and Publish official extension

1. Follow above steps for validating, releasing & publishing development extension
1. Push the local changes `git push <remote> <branch-name> -u`
1. Create PR for the changes within `azure/azure-dev` repo

> [!IMPORTANT]
> Make sure modifications to the `registry.json` file are NOT included on extension code PR.
> Changes to the `registry.json` file should be made after code changes have been merged.

##### After PR has been merged

1. Create a new branch to publish registry changes `git checkout -b <branch-name>`
1. Run `azd x publish --registry ../registry.json` to publish the new version metadata into the registry
1. Commit the changes to the local branch, `git commit -am "<description>"`
1. Create PR for the changes within `azure/azure-dev` repo

Once PR has been merged the extension updates are now live in the official `azd` extension source registry.

### Extension Manifest

Each extension must declare an `extension.yaml` file that describe the metadata for the extension and the capabilities that it supports. This metadata is used within the extension registry to provide details to developers when searching for and installing extensions.

A [JSON schema](../extensions/registry.schema.json) is available to support authoring extension manifests.

#### Example

The following is an example of an [extension manifest](../extensions/microsoft.azd.demo/extension.yaml).

```yaml
# yaml-language-server: $schema=https://raw.githubusercontent.com/Azure/azure-dev/refs/heads/main/cli/azd/extensions/registry.schema.json

id: microsoft.azd.demo
namespace: demo
displayName: Demo Extension
description: This extension provides examples of the AZD extension framework.
usage: azd demo <command> [options]
version: 0.1.0
capabilities:
  - custom-commands
  - lifecycle-events
examples:
  - name: context
    description: Displays the current `azd` project & environment context.
    usage: azd demo context
  - name: prompt
    description: Display prompt capabilities.
    usage: azd demo prompt
```

### Invoking Extension Commands

When `azd` invokes an extension command, the following steps occur:

1. `azd` starts a gRPC server on a random port.
2. `azd` invokes your command, passing all arguments and flags:
    - An environment variable named `AZD_SERVER` is set with the server address and random port (e.g., `localhost:12345`).
    - An `azd` access token environment variable `AZD_ACCESS_TOKEN` is set which is a JWT token signed with a randomly generated key good for the lifetime of the command. The token includes claims that identify each unique extensions and its supported capabilities.
    - Additional environment variables from the current `azd` environment are also set.
3. The extension command can communicate with `azd` through [extension framework gRPC services](#grpc-services).
4. `azd` waits for the extension command to complete:
    - If a non-zero exit code is returned, `azd` reports the operation as an error.

To enable interaction with `azd` from within the extension, the extension must leverage a gRPC client and connect to the server using the address specified in the `AZD_SERVER` environment variable.

The gRPC client must also include an `authorization` parameter with the value from `AZD_ACCESS_TOKEN`; otherwise, requests will fail due to invalid authorization. Extensions must declare their supported capabilities within the extension registry otherwise certain service operations may fail with a permission denied error.

#### Go

For extensions built using Go, the `azdext` package provides an `AzdClient` which acts as the gRPC client.

#### Other Languages

For extensions authored in other programming languages, the [gRPC proto files](../grpc/proto/) can be used to generate clients in your preferred language.

### How to set `azd` access token on requests

The following example shows how an outgoing Go context can be constructed with the `azd` access token.

```go
// WithAccessToken sets the access token for the `azd` client into a new Go context.
func WithAccessToken(ctx context.Context, params ...string) context.Context {
    tokenValue := strings.Join(params, "")
    if tokenValue == "" {
        tokenValue = os.Getenv("AZD_ACCESS_TOKEN")
    }

    md := metadata.Pairs("authorization", tokenValue)
    return metadata.NewOutgoingContext(ctx, md)
}
```

### How to call `azd` gRPC service

```go
// Create a new context that includes the AZD access token
ctx := azdext.WithAccessToken(cmd.Context())

// Create a new AZD client
// The constructor function automatically constructs the address
// from the `AZD_SERVER` environment variable but can be overridden if required.
azdClient, err := azdext.NewAzdClient()
if err != nil {
    return fmt.Errorf("failed to create azd client: %w", err)
}

defer azdClient.Close()

getProjectResponse, err := azdClient.Project().Get(ctx, &azdext.EmptyRequest{})
if err != nil {
    // No project found
    return err
}

// Print out the `azd` project name
fmt.Println(getProjectResponse.Project.Name)
```

### How to subscribe to lifecycle events

The following is an example of subscribing to project & service lifecycle events within an `azd` template.

In this example the extension is leveraging the `azdext.EventManager` struct. This struct makes it easier to subscribe and consume the gRPC bi-directional event stream between `azd` and the extension.

Other languages will need to manually handle the different message types invoked by the service.

```go
// Create a new context that includes the AZD access token.
ctx := azdext.WithAccessToken(cmd.Context())

// Create a new AZD client.
azdClient, err := azdext.NewAzdClient()
if err != nil {
    return fmt.Errorf("failed to create azd client: %w", err)
}
defer azdClient.Close()

eventManager := azdext.NewEventManager(azdClient)
defer eventManager.Close()

// Subscribe to a project event
err = eventManager.AddProjectEventHandler(
    ctx,
    "preprovision",
    func(ctx context.Context, args *azdext.ProjectEventArgs) error {
        // This is your event handler code
    for i := 1; i <= 20; i++ {
            fmt.Printf("%d. Doing important work in extension...\n", i)
            time.Sleep(250 * time.Millisecond)
        }

        return nil
    },
)
if err != nil {
    return fmt.Errorf("failed to add preprovision project event handler: %w", err)
}

// Subscribe to a service event
err = eventManager.AddServiceEventHandler(
    ctx,
    "prepackage",
    func(ctx context.Context, args *azdext.ServiceEventArgs) error {
        // This is your event handler
        for i := 1; i <= 20; i++ {
            fmt.Printf("%d. Doing important work in extension...\n", i)
            time.Sleep(250 * time.Millisecond)
        }

        return nil
    },
    // Optionally filter your subscription by service host and/or language
    &azdext.ServerEventOptions{
        Host: "containerapp",
        Language: "python",
    },
)

if err != nil {
    return fmt.Errorf("failed to add prepackage event handler: %w", err)
}

// Start listening for events
// This is a blocking call and will not return until the server connection is closed.
if err := eventManager.Receive(ctx); err != nil {
    return fmt.Errorf("failed to receive events: %w", err)
}

```

## Developer Artifacts

`azd` leverages gRPC for the communication protocol between Core `azd` and extensions. gRPC client & server components are automatically generated from profile files.

- Proto files @ [grpc/proto](../grpc/proto/)
- Generated files @ [pkg/azdext](../pkg/azdext)
- Make file @ [Makefile](../Makefile)

To re-generate gRPC clients:

- Run `protoc --version` to check if `protoc` is installed. If not, download and install it from [GitHub](https://github.com/protocolbuffers/protobuf/releases).
- Run `make --version` to check if `make` is installed.
- Run `go install google.golang.org/protobuf/cmd/protoc-gen-go@latest` and `go install google.golang.org/grpc/cmd/protoc-gen-go-grpc@latest` to install the required Go tools.
- Run `make proto` from the `~/cli/azd` folder of the repo in `Git Bash`.
- Run `../../eng/scripts/copyright-check.sh . --fix` to add copyright.

## gRPC Services

The following are a list of available gRPC services for extension developer to integrate with `azd` core.

### Table of Contents

- [Project Service](#project-service)
- [Environment Service](#environment-service)
- [User Config Service](#user-config-service)
- [Deployment Service](#deployment-service)
- [Prompt Service](#prompt-service)
- [Event Service](#event-service)
- [Compose Service](#compose-service)
- [Workflow Service](#workflow-service)

---

### Project Service

This service manages project configuration retrieval and related operations.

> See [project.proto](../grpc/proto/project.proto) for more details.

#### Get

Gets the current project configuration.

- **Request:** _EmptyRequest_ (no fields)
- **Response:** _GetProjectResponse_
  - Contains **ProjectConfig**:
    - `name` (string)
    - `resource_group_name` (string)
    - `path` (string)
    - `metadata`: `{ template: string }`
    - `services`: map of _ServiceConfig_
    - `infra`: _InfraOptions_

#### AddService

Adds a new service to the project.

- **Request:** _AddServiceRequest_
  - Contains:
    - `service`: _ServiceConfig_
- **Response:** _EmptyResponse_

---

### Environment Service

This service handles environment management including retrieval, selection, and key-value operations.

> See [environment.proto](../grpc/proto/environment.proto) for more details.

#### GetCurrent

Gets the current environment.

- **Request:** _EmptyRequest_
- **Response:** _EnvironmentResponse_ (includes an _Environment_ with `name`)

#### List

Retrieves all azd environments.

- **Request:** _EmptyRequest_
- **Response:** _EnvironmentListResponse_ (list of _EnvironmentDescription_)

#### Get

Retrieves an environment by its name.

- **Request:** _GetEnvironmentRequest_
  - `name` (string)
- **Response:** _EnvironmentResponse_
  - Contains an **Environment** with `name` (string)

#### Select

Sets the selected environment as default.

- **Request:** _SelectEnvironmentRequest_
  - `name` (string)
- **Response:** _EmptyResponse_

#### GetValues

Retrieves all key-value pairs in the specified environment.

- **Request:** _GetEnvironmentRequest_
  - `name` (string)
- **Response:** _KeyValueListResponse_
  - Contains a list of **KeyValue**:
    - `key` (string)
    - `value` (string)

#### GetValue

Retrieves the value of a specific key.

- **Request:** _GetEnvRequest_
  - `env_name` (string)  
  - `key` (string)
- **Response:** _KeyValueResponse_
  - Contains:
    - `key` (string)
    - `value` (string)

#### SetValue

Sets the value of a key in an environment.

- **Request:** _SetEnvRequest_
  - `env_name` (string)  
  - `key` (string)  
  - `value` (string)
- **Response:** _EmptyResponse_

#### GetConfig

Retrieves a config value by path.

- **Request:** _GetConfigRequest_
  - `path` (string)
- **Response:** _GetConfigResponse_
  - Contains:
    - `value` (bytes)
    - `found` (bool)

#### GetConfigString

Retrieves a config value as a string by path.

- **Request:** _GetConfigStringRequest_
  - `path` (string)
- **Response:** _GetConfigStringResponse_
  - Contains:
    - `value` (string)
    - `found` (bool)

#### GetConfigSection

Retrieves a config section by path.

- **Request:** _GetConfigSectionRequest_
  - `path` (string)
- **Response:** _GetConfigSectionResponse_
  - Contains:
    - `section` (bytes)
    - `found` (bool)

#### SetConfig

Sets a config value at a given path.

- **Request:** _SetConfigRequest_
  - `path` (string)  
  - `value` (bytes)
- **Response:** _EmptyResponse_

#### UnsetConfig

Removes a config value at a given path.

- **Request:** _UnsetConfigRequest_
  - `path` (string)
- **Response:** _EmptyResponse_

---

### User Config Service

This service manages user-specific configuration retrieval and updates.

> See [user_config.proto](../grpc/proto/user_config.proto) for more details.

#### Get

Retrieves a user configuration value by path.

- **Request:** _GetRequest_
  - `path` (string)
- **Response:** _GetResponse_
  - Contains:
    - `value` (bytes)
    - `found` (bool)

#### GetString

Retrieves a user configuration value as a string.

- **Request:** _GetStringRequest_
  - `path` (string)
- **Response:** _GetStringResponse_
  - Contains:
    - `value` (string)
    - `found` (bool)

#### GetSection

Retrieves a section of the user configuration by path.

- **Request:** _GetSectionRequest_
  - `path` (string)
- **Response:** _GetSectionResponse_
  - Contains:
    - `section` (bytes)
    - `found` (bool)

#### Set

Sets a user configuration value.

- **Request:** _SetRequest_
  - `path` (string)
  - `value` (bytes)
- **Response:** _SetResponse_
  - Contains:
    - `status` (string)

#### Unset

Removes a user configuration value.

- **Request:** _UnsetRequest_
  - `path` (string)
- **Response:** _UnsetResponse_
  - Contains:
    - `status` (string)

---

### Deployment Service

This service provides operations for deployment retrieval and context management.

> See [deployment.proto](../grpc/proto/deployment.proto) for more details.

#### GetDeployment

Retrieves the latest Azure deployment provisioned by azd.

- **Request:** _EmptyRequest_
- **Response:** _GetDeploymentResponse_
  - Contains **Deployment**:
    - `id` (string)
    - `location` (string)
    - `deploymentId` (string)
    - `name` (string)
    - `type` (string)
    - `tags` (map<string, string>)
    - `outputs` (map<string, string>)
    - `resources` (repeated string)

#### GetDeploymentContext

Retrieves the current deployment context.

- **Request:** _EmptyRequest_
- **Response:** _GetDeploymentContextResponse_
  - Contains **AzureContext**:
    - `scope` with:
      - `tenantId` (string)
      - `subscriptionId` (string)
      - `location` (string)
      - `resourceGroup` (string)
    - `resources` (repeated string)

---

### Prompt Service

This service manages user prompt interactions for subscriptions, locations, resources, and confirmations.

> See [prompt.proto](../grpc/proto/prompt.proto) for more details.

#### PromptSubscription

Prompts the user to select a subscription.

- **Request:** _PromptSubscriptionRequest_ (empty)
- **Response:** _PromptSubscriptionResponse_
  - Contains **Subscription**

#### PromptLocation

Prompts the user to select a location.

- **Request:** _PromptLocationRequest_
  - `azure_context` (AzureContext)
- **Response:** _PromptLocationResponse_
  - Contains **Location**

#### PromptResourceGroup

Prompts the user to select a resource group.

- **Request:** _PromptResourceGroupRequest_
  - `azure_context` (AzureContext)
- **Response:** _PromptResourceGroupResponse_
  - Contains **ResourceGroup**

#### Confirm

Prompts the user to confirm an action.

- **Request:** _ConfirmRequest_
  - `options` (ConfirmOptions) with:
    - `default_value` (optional bool)
    - `message` (string)
    - `helpMessage` (string)
    - `hint` (string)
    - `placeholder` (string)
- **Response:** _ConfirmResponse_
  - Contains an optional `value` (bool)

#### Prompt

Prompts the user for text input.

- **Request:** _PromptRequest_
  - `options` (PromptOptions) with:
    - `message` (string)
    - `help_message` (string)
    - `hint` (string)
    - `placeholder` (string)
    - `validation_message` (string)
    - `required_message` (string)
    - `required` (bool)
    - `defaultValue` (string)
    - `clear_on_completion` (bool)
    - `ignore_hint_keys` (bool)
- **Response:** _PromptResponse_
  - Contains `value` (string)

#### Select

Prompts the user to select an option from a list.

- **Request:** _SelectRequest_
  - `options` (SelectOptions) with:
    - `SelectedIndex` (optional int32)
    - `message` (string)
    - `allowed` (repeated string)
    - `help_message` (string)
    - `hint` (string)
    - `display_count` (int32)
    - `display_numbers` (optional bool)
    - `enable_filtering` (optional bool)
- **Response:** _SelectResponse_
  - Contains an optional `value` (int32)

#### PromptSubscriptionResource

Prompts the user to select a resource from a subscription.

- **Request:** _PromptSubscriptionResourceRequest_ with:
  - `azure_context` (AzureContext)
  - `options` (PromptResourceOptions) with:
    - `resource_type` (string)
    - `kinds` (repeated string)
    - `resource_type_display_name` (string)
    - `select_options` (PromptResourceSelectOptions)
- **Response:** _PromptSubscriptionResourceResponse_
  - Contains **ResourceExtended**

#### PromptResourceGroupResource

Prompts the user to select a resource from a resource group.

- **Request:** _PromptResourceGroupResourceRequest_ with:
  - `azure_context` (AzureContext)
  - `options` (PromptResourceOptions) (same structure as above)
- **Response:** _PromptResourceGroupResourceResponse_
  - Contains **ResourceExtended**

---

### Event Service

This service defines methods for event subscription, invocation, and status updates.  
Clients can subscribe to events and receive notifications via a bidirectional stream.

#### EventStream

- Establishes a bidirectional stream that enables clients to:
  - Subscribe to project and service events.
  - Invoke event handlers.
  - Send status updates regarding event processing.

> See [event.proto](../grpc/proto/event.proto) for more details.

#### Message Types

- **EventMessage**
  Encapsulates a single event payload among several possible types.

  Contains:
  - Uses a oneof field to encapsulate different event types.
- **ExtensionReadyEvent**
  Signals that an extension is ready, including any status updates.

  Contains:
  - `status`: Indicates the readiness state of the extension.
  - `message`: Provides additional details.
- **SubscribeProjectEvent**
  Allows clients to subscribe to events specific to project lifecycle changes.

  Contains:
  - `event_names`: A list of event names to subscribe to for project events.
- **SubscribeServiceEvent**
  Allows clients to subscribe to service-specific events along with context details.

  Contains:
  - `event_names`: A list of event names to subscribe to for service events.
  - `language`: The language of the service.
  - `host`: The host identifier.
- **InvokeProjectHandler**
  Instructs the invocation of a project event handler with configuration details.

  Contains:
  - `event_name`: The name of the event being invoked.
  - `project`: The project configuration.
- **InvokeServiceHandler**
  Instructs the invocation of a service event handler including associated configurations.

  Contains:
  - `event_name`: The name of the event being invoked.
  - `project`: The project configuration.
  - `service`: The specific service configuration.
- **ProjectHandlerStatus**
  Provides status updates for project events.

  Contains:
  - `event_name`: The event name for which this status update applies.
  - `status`: Status such as "running", "completed", or "failed".
  - `message`: Optional additional details.
- **ServiceHandlerStatus**
  Provides status updates for service events.

  Contains:
  - `event_name`: The event name for which this status update applies.
  - `service_name`: The name of the service.
  - `status`: Status such as "running", "completed", or "failed".
  - `message`: Optional additional details.

### Compose Service

This service manages composability resources in an AZD project.

> See [compose.proto](../grpc/proto/compose.proto) for more details.

#### ListResources

Lists all configured composability resources.

- **Request:** _EmptyRequest_
- **Response:** _ListResourcesResponse_
  - Contains a list of **ComposedResource**

#### GetResource

Retrieves the configuration of a specific composability resource.

- **Request:** _GetResourceRequest_
  - Contains:
    - `name` (string)
- **Response:** _GetResourceResponse_
  - Contains:
    - `resource`: _ComposedResource_

#### ListResourceTypes

Lists all supported composability resource types.

- **Request:** _EmptyRequest_
- **Response:** _ListResourceTypesResponse_
  - Contains a list of **ComposedResourceType**

#### GetResourceType

Retrieves the schema of a specific composability resource type.

- **Request:** _GetResourceTypeRequest_
  - Contains:
    - `type_name` (string)
- **Response:** _GetResourceTypeResponse_
  - Contains:
    - `resource_type`: _ComposedResourceType_

#### AddResource

Adds a new composability resource to the project.

- **Request:** _AddResourceRequest_
  - Contains:
    - `resource`: _ComposedResource_
- **Response:** _AddResourceResponse_
  - Contains:
    - `resource`: _ComposedResource_

---

### Workflow Service

This service executes workflows defined within the project.

> See [workflow.proto](../grpc/proto/workflow.proto) for more details.

#### Run

Executes a workflow consisting of sequential steps.

- **Request:** _RunWorkflowRequest_
  - Contains:
    - `workflow`: _Workflow_ (with `name` and `steps`)
- **Response:** _EmptyResponse_

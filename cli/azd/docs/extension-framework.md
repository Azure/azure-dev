# Extension Framework

> **NOTE:** The `azd` extension framework is experimental.
>
> To enable extensions, run:  
> `azd config set alpha.extensions on`

Table of Contents

- [Capabilities](#capabilities)
- [Managing Extensions](#managing-extensions)
- [Developing Extensions](#developing-extensions)
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

## Capabilities

### Current Capabilities (Feb 2025)

The following lists the current capabilities of `azd extensions`.

#### Extension Commands

> Extensions must declare the `custom-commands` capability in their `extension.yaml` file.

Extensions can register commands under a namespace or command group within `azd`.
For example, installing the AI extension adds a new `ai` command group.

#### Lifecycle Hooks

> Extensions must declare the `lifecycle-events` capability in their `extension.yaml` file.

Extensions can subscribe to project and service lifecycle events (both pre and post events) for:

- build
- restore
- package
- provision
- deploy

Your extension _**must**_ include a `listen` command to subscribe to these events.
`azd` will automatically invoke your extension during supported commands to establish bi-directional communication.

##### Install extensions

Run:

```bash
azd extension install microsoft.azd.ai
```

In this example, the AI extension registers under the `ai` namespace.

##### Run extensions

Run:

```bash
azd ai <command> [flags]
```

### Future Considerations

Future ideas include:

- Registration of pluggable providers for:
  - Language support (e.g., Go)
  - New Azure service targets (e.g., VMs, ACI)
  - Infrastructure providers (e.g., Pulumi)
  - Source control providers (e.g., GitLab)
  - Pipeline providers (e.g., TeamCity)

## Managing Extensions

### Extension Sources

Extension sources are file- or URL-based manifests that provide a registry of available `azd` extensions.
Users can add custom extension sources that connect to private, local, or public registries.

Extension registries must adhere to the [official extension registry schema](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/registry.schema.json).

The official `azd` extension registry is available in the [`azd` github repo](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/registry.json).

#### `azd extension source list`

Displays a list of installed extension sources

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

`azd` extensions can be developed using any programming language. It is recommended that initial extensions leverage Go for best support.

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

To re-generate gRPC clients run `make proto` from the `~/cli/azd` folder of the repo.

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

### Project Service

This service manages project configuration retrieval and related operations.

#### Get

Gets the current project configuration.

- **Request:** *EmptyRequest* (no fields)
- **Response:** *GetProjectResponse*
  - Contains **ProjectConfig**:
    - `name` (string)
    - `resource_group_name` (string)
    - `path` (string)
    - `metadata`: `{ template: string }`
    - `services`: map of *ServiceConfig*
    - `infra`: *InfraOptions*

#### AddService
Adds a new service to the project.

- **Request:** *AddServiceRequest*
  - Contains:
    - `service`: *ServiceConfig*
- **Response:** *EmptyResponse*

### Environment Service

This service handles environment management including retrieval, selection, and key-value operations.

#### GetCurrent

Gets the current environment.

- **Request:** *EmptyRequest*
- **Response:** *EnvironmentResponse* (includes an *Environment* with `name`)

#### List

Retrieves all azd environments.

- **Request:** *EmptyRequest*
- **Response:** *EnvironmentListResponse* (list of *EnvironmentDescription*)

#### Get

Retrieves an environment by its name.

- **Request:** *GetEnvironmentRequest*
  - `name` (string)
- **Response:** *EnvironmentResponse*
  - Contains an **Environment** with `name` (string)

#### Select

Sets the selected environment as default.

- **Request:** *SelectEnvironmentRequest*
  - `name` (string)
- **Response:** *EmptyResponse*

#### GetValues

Retrieves all key-value pairs in the specified environment.

- **Request:** *GetEnvironmentRequest*
  - `name` (string)
- **Response:** *KeyValueListResponse*
  - Contains a list of **KeyValue**:
    - `key` (string)
    - `value` (string)

#### GetValue

Retrieves the value of a specific key.

- **Request:** *GetEnvRequest*
  - `env_name` (string)  
  - `key` (string)
- **Response:** *KeyValueResponse*
  - Contains:
    - `key` (string)
    - `value` (string)

#### SetValue

Sets the value of a key in an environment.

- **Request:** *SetEnvRequest*
  - `env_name` (string)  
  - `key` (string)  
  - `value` (string)
- **Response:** *EmptyResponse*

#### GetConfig

Retrieves a config value by path.

- **Request:** *GetConfigRequest*
  - `path` (string)
- **Response:** *GetConfigResponse*
  - Contains:
    - `value` (bytes)
    - `found` (bool)

#### GetConfigString

Retrieves a config value as a string by path.

- **Request:** *GetConfigStringRequest*
  - `path` (string)
- **Response:** *GetConfigStringResponse*
  - Contains:
    - `value` (string)
    - `found` (bool)

#### GetConfigSection

Retrieves a config section by path.

- **Request:** *GetConfigSectionRequest*
  - `path` (string)
- **Response:** *GetConfigSectionResponse*
  - Contains:
    - `section` (bytes)
    - `found` (bool)

#### SetConfig

Sets a config value at a given path.

- **Request:** *SetConfigRequest*
  - `path` (string)  
  - `value` (bytes)
- **Response:** *EmptyResponse*

#### UnsetConfig

Removes a config value at a given path.

- **Request:** *UnsetConfigRequest*
  - `path` (string)
- **Response:** *EmptyResponse*

### User Config Service

This service manages user-specific configuration retrieval and updates.

#### Get

Retrieves a user configuration value by path.

- **Request:** *GetRequest*
  - `path` (string)
- **Response:** *GetResponse*
  - Contains:
    - `value` (bytes)
    - `found` (bool)

#### GetString

Retrieves a user configuration value as a string.

- **Request:** *GetStringRequest*
  - `path` (string)
- **Response:** *GetStringResponse*
  - Contains:
    - `value` (string)
    - `found` (bool)

#### GetSection

Retrieves a section of the user configuration by path.

- **Request:** *GetSectionRequest*
  - `path` (string)
- **Response:** *GetSectionResponse*
  - Contains:
    - `section` (bytes)
    - `found` (bool)

#### Set

Sets a user configuration value.

- **Request:** *SetRequest*
  - `path` (string)
  - `value` (bytes)
- **Response:** *SetResponse*
  - Contains:
    - `status` (string)

#### Unset

Removes a user configuration value.

- **Request:** *UnsetRequest*
  - `path` (string)
- **Response:** *UnsetResponse*
  - Contains:
    - `status` (string)

### Deployment Service

This service provides operations for deployment retrieval and context management.

#### GetDeployment

Retrieves the latest Azure deployment provisioned by azd.

- **Request:** *EmptyRequest*
- **Response:** *GetDeploymentResponse*
  - Contains **Deployment**:
    - `id` (string)
    - `location` (string)
    - `deploymentId` (string)
    - `name` (string)
    - `type` (string)
    - `tags` (map<string, string>)
    - `outputs` (map<string, string>)
    - `resources` (repeated string)

*See [deployment.proto](../grpc/proto/deployment.proto).*

#### GetDeploymentContext

Retrieves the current deployment context.

- **Request:** *EmptyRequest*
- **Response:** *GetDeploymentContextResponse*
  - Contains **AzureContext**:
    - `scope` with:
      - `tenantId` (string)
      - `subscriptionId` (string)
      - `location` (string)
      - `resourceGroup` (string)
    - `resources` (repeated string)

### Prompt Service

This service manages user prompt interactions for subscriptions, locations, resources, and confirmations.

#### PromptSubscription

Prompts the user to select a subscription.

- **Request:** *PromptSubscriptionRequest* (empty)
- **Response:** *PromptSubscriptionResponse*
  - Contains **Subscription**

#### PromptLocation

Prompts the user to select a location.

- **Request:** *PromptLocationRequest*
  - `azure_context` (AzureContext)
- **Response:** *PromptLocationResponse*
  - Contains **Location**

#### PromptResourceGroup

Prompts the user to select a resource group.

- **Request:** *PromptResourceGroupRequest*
  - `azure_context` (AzureContext)
- **Response:** *PromptResourceGroupResponse*
  - Contains **ResourceGroup**

#### Confirm

Prompts the user to confirm an action.

- **Request:** *ConfirmRequest*
  - `options` (ConfirmOptions) with:
    - `default_value` (optional bool)
    - `message` (string)
    - `helpMessage` (string)
    - `hint` (string)
    - `placeholder` (string)
- **Response:** *ConfirmResponse*
  - Contains an optional `value` (bool)

#### Prompt

Prompts the user for text input.

- **Request:** *PromptRequest*
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
- **Response:** *PromptResponse*
  - Contains `value` (string)

#### Select

Prompts the user to select an option from a list.

- **Request:** *SelectRequest*
  - `options` (SelectOptions) with:
    - `SelectedIndex` (optional int32)
    - `message` (string)
    - `allowed` (repeated string)
    - `help_message` (string)
    - `hint` (string)
    - `display_count` (int32)
    - `display_numbers` (optional bool)
    - `enable_filtering` (optional bool)
- **Response:** *SelectResponse*
  - Contains an optional `value` (int32)

#### PromptSubscriptionResource

Prompts the user to select a resource from a subscription.

- **Request:** *PromptSubscriptionResourceRequest* with:
  - `azure_context` (AzureContext)
  - `options` (PromptResourceOptions) with:
    - `resource_type` (string)
    - `kinds` (repeated string)
    - `resource_type_display_name` (string)
    - `select_options` (PromptResourceSelectOptions)
- **Response:** *PromptSubscriptionResourceResponse*
  - Contains **ResourceExtended**

#### PromptResourceGroupResource

Prompts the user to select a resource from a resource group.

- **Request:** *PromptResourceGroupResourceRequest* with:
  - `azure_context` (AzureContext)
  - `options` (PromptResourceOptions) (same structure as above)
- **Response:** *PromptResourceGroupResourceResponse*
  - Contains **ResourceExtended**

### Event Service

This service defines methods for event subscription, invocation, and status updates.  
Clients can subscribe to events and receive notifications via a bidirectional stream.

#### EventStream

- Establishes a bidirectional stream that enables clients to:
  - Subscribe to project and service events.
  - Invoke event handlers.
  - Send status updates regarding event processing.

*See [event.proto](../grpc/proto/event.proto) for more details.*

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

#### ListResources

Lists all configured composability resources.

- **Request:** *EmptyRequest*
- **Response:** *ListResourcesResponse*
  - Contains a list of **ComposedResource**

#### GetResource

Retrieves the configuration of a specific composability resource.

- **Request:** *GetResourceRequest*
  - Contains:
    - `name` (string)
- **Response:** *GetResourceResponse*
  - Contains:
    - `resource`: *ComposedResource*

#### ListResourceTypes

Lists all supported composability resource types.

- **Request:** *EmptyRequest*
- **Response:** *ListResourceTypesResponse*
  - Contains a list of **ComposedResourceType**

#### GetResourceType

Retrieves the schema of a specific composability resource type.

- **Request:** *GetResourceTypeRequest*
  - Contains:
    - `type_name` (string)
- **Response:** *GetResourceTypeResponse*
  - Contains:
    - `resource_type`: *ComposedResourceType*

#### AddResource

Adds a new composability resource to the project.

- **Request:** *AddResourceRequest*
  - Contains:
    - `resource`: *ComposedResource*
- **Response:** *AddResourceResponse*
  - Contains:
    - `resource`: *ComposedResource*

### Workflow Service

This service executes workflows defined within the project.

#### Run

Executes a workflow consisting of sequential steps.

- **Request:** *RunWorkflowRequest*
  - Contains:
    - `workflow`: *Workflow* (with `name` and `steps`)
- **Response:** *EmptyResponse*

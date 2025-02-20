# Extension Framework

> **NOTE:** The `azd` extension framework is currently experimental.

> Enable extensions by running:  
> `azd config set alpha.extensions on`

Table of Contents

- [Capabilities](#capabilities)
- [Managing Extensions](#managing-extensions)
- [Developing Extensions](#developing-extensions)
- [Developer Artifacts](#developer-artifacts)

## Capabilities

### Current Capabilities (Feb 2025)

The following lists the current capabilities of `azd extensions`.

#### Extension Commands

Extensions can register commands under a namespace or command group within `azd`.  
For example, installing the AI extension adds a new `ai` command group.

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

- Opt-in execution of extensions during project/service lifecycle events.
- Registration of pluggable providers for:
  - Language support (e.g., Go)
  - New Azure service targets (e.g., VMs, ACI)
  - Infrastructure providers (e.g., Pulumi)
  - Source control providers (e.g., GitLab)
  - Pipeline providers (e.g., TeamCity)

## Managing Extensions

### Extension Sources

Extension sources are file or URL based manifests that provide a registry of available `azd` extensions.
Users can add custom extension source that connect to private, local or public registries.

Extension registries must adhere to the [official extension registry schema](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/registry.schema.json).

The official `azd` extension registry is available is available in the [`azd` github repo](https://github.com/Azure/azure-dev/blob/main/cli/azd/extensions/registry.json).

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

#### `azd extension uninstall <extension-names> [flags]`

Uninstalls one or more previously installed extensions.

- `--all` Removes all installed extensions when specified.

#### `azd extension upgrade <extension-names>`

Upgrades one or more extensions to the latest versions.

- `--all` Upgrades all previously installed extensions when specified.
- `--version` Upgrades a specified extension using a semver version constraint, if provided.

## Developing Extensions

`azd` extensions can be developed using any programming language. It is recommended that initial extensions leverage Go for best support.

### Invoking Extension Commands

When `azd` invokes an extension command, the following steps occur:

1. `azd` starts a gRPC server on a random port.
2. `azd` invokes your command, passing all arguments and flags:
    - An environment variable named `AZD_SERVER` is set with the server address and random port (e.g., `localhost:12345`).
    - An `azd` access token environment variable `AZD_ACCESS_TOKEN` is set with a randomly generated 128-bit token good for the lifetime of the command.
    - Additional environment variables from the current `azd` environment are also set.
3. The extension command can communicate with `azd` through [extension framework gRPC services](#grpc-services).
4. `azd` waits for the extension command to complete:
    - If a non-zero exit code is returned, `azd` reports the operation as an error.

To enable interaction with `azd` from within the extension, the extension must leverage a gRPC client and connect to the server using the address specified in the `AZD_SERVER` environment variable.

The gRPC client must also include an `authorization` parameter with the value from `AZD_ACCESS_TOKEN`; otherwise, requests will fail due to invalid authorization.

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

## Developer Artifacts

`azd` leverages gRPC for the communication protocol between Core `azd` and extensions. gRPC client & server components are automatically generated from profile files.

- Proto files @ [grpc/proto](../grpc/proto/)
- Generated files @ [pkg/azdext](../pkg/azdext)
- Make file @ [Makefile](../Makefile)

To re-generate gRPC clients run `make proto`

## gRPC Services

The following are a list of available gRPC services for extension developer to integrate with `azd` core.

### Table of Contents

- [Project Service](#project-service)
- [Environment Service](#environment-service)
- [User Config Service](#user-config-service)
- [Deployment Service](#deployment-service)
- [Prompt Service](#prompt-service)

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

*See [project.proto](../grpc/proto/project.proto).*

------------------------------------------

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

------------------------------------------

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

------------------------------------------

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

------------------------------------------

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

````

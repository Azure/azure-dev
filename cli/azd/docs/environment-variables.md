# Environment Variables

## Environment variables used with azd

Environment variables that can be used to configure `azd` behavior, usually set within a shell or terminal. For environment variables that accept a boolean, the values `1, t, T, TRUE, true, True` are accepted as "true"; the values: `0, f, F, FALSE, false, False` are all accepted as "false".

## Configuration

- `AZD_CONFIG_DIR`: The file path of the user-level configuration directory. Defaults to `~/.azd` on Linux/macOS and `%USERPROFILE%\.azd` on Windows.
- `AZD_SKIP_UPDATE_CHECK`: If true, skips the out-of-date update check output that is typically printed at the end of the command.
- `AZD_ALLOW_NON_EMPTY_FOLDER`: If true, allows `azd init` to initialize in a non-empty folder without prompting.

## Authentication

- `AZD_AUTH_ENDPOINT`: The [External Authentication](./external-authentication.md) endpoint URL for delegating authentication to an external service.
- `AZD_AUTH_KEY`: The [External Authentication](./external-authentication.md) shared key for authenticating with the external authentication service.
- `AZD_AUTH_CERT`: Certificate for external authentication.
- `AZURE_DEV_COLLECT_TELEMETRY`: Controls whether azd collects telemetry. Set to `no` to disable telemetry collection. This is the equivalent of `AZURE_CORE_COLLECT_TELEMETRY` used by other Azure SDKs.
- `AZURE_DEV_USER_AGENT`: User agent string that callers of azd (like VS Code extensions or Visual Studio) can set to identify themselves. Used for telemetry and diagnostics.

## Azure Resources

These environment variables are typically set automatically by azd in the environment's `.env` file after provisioning, but can also be set manually:

- `AZURE_ENV_NAME`: The name of the azd environment. This is a required value for most azd operations.
- `AZURE_LOCATION`: The Azure region/location where resources are deployed (e.g., `eastus`, `westus2`).
- `AZURE_SUBSCRIPTION_ID`: The Azure subscription ID to use for deployment and resource management.
- `AZURE_TENANT_ID`: The Azure Active Directory tenant ID.
- `AZURE_PRINCIPAL_ID`: The ID of the identity (user or service principal) used for authentication.
- `AZURE_PRINCIPAL_TYPE`: The type of the principal (e.g., `User`, `ServicePrincipal`).
- `AZURE_RESOURCE_GROUP`: The name of the Azure resource group containing deployed resources.
- `AZURE_CONTAINER_REGISTRY_ENDPOINT`: The endpoint URL of the Azure Container Registry used for container image storage.
- `AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN`: The default domain for Azure Container Apps environments.
- `AZURE_AKS_CLUSTER_NAME`: The name of the Azure Kubernetes Service (AKS) cluster.
- `AZURE_APP_SERVICE_DASHBOARD_URI`: The URL for the App Service Aspire Dashboard.

## Platform

- `AZD_PLATFORM_TYPE`: Specifies the platform type being used (e.g., `devcenter` for Azure Deployment Environments).
- `AZD_INITIAL_ENVIRONMENT_CONFIG`: (Deprecated, but still supported) JSON string containing initial environment configuration. Used to reconstruct azd environment state in CI/CD pipelines. Prefer using individual variables or secrets for CI/CD parameters.

## Container and Build

- `AZD_BUILDER_IMAGE`: The Docker builder image to use for Dockerfile-less builds. Overrides the default builder image.

## Tool Paths

For tools that are auto-acquired by `azd`, you can configure the following environment variables to use a different version of the tool installed on the machine:

- `AZD_BICEP_TOOL_PATH`: The Bicep tool override path. The direct path to `bicep` or `bicep.exe`.
- `AZD_GH_TOOL_PATH`: The GitHub CLI (`gh`) tool override path. The direct path to `gh` or `gh.exe`.
- `AZD_PACK_TOOL_PATH`: The Cloud Native Buildpacks (`pack`) tool override path. The direct path to `pack` or `pack.exe`.

## Terminal and Output Control

- `AZD_FORCE_TTY`: If true, forces `azd` to write terminal-style output even when not running in an interactive terminal.
- `AZD_DEMO_MODE`: If true, enables demo mode. This hides personal information such as subscription IDs from being displayed in output.
- `NO_COLOR`: If set, disables colored output. This is a standard environment variable respected by many CLI tools.
- `FORCE_COLOR`: If set, forces colored output even when not running in an interactive terminal.
- `COLUMNS`: Specifies the console width in characters. Used for formatting output and markdown rendering.
- `TERM`: Terminal type identifier. Used to determine terminal capabilities.
- `BROWSER`: Specifies the browser command to use for opening URLs (e.g., when opening Azure Portal links).

## Alpha Features

- `AZD_ALPHA_ENABLE_<name>`: Enables or disables an alpha feature. `<name>` is the upper-cased name of the feature, with dot `.` characters replaced by underscore `_` characters. For example, to enable a feature named `awesome.feature`, set `AZD_ALPHA_ENABLE_AWESOME_FEATURE=true`.

## CI/CD Integration

### CI/CD Detection

azd automatically detects when running in various CI/CD environments by checking for the following environment variables:

- `GITHUB_ACTIONS`: Set to `true` by GitHub Actions.
- `TF_BUILD`: Set to `true` by Azure Pipelines.
- `CI`: Generic CI detection variable used by multiple CI systems.
- `CODESPACES`: Set when running in GitHub Codespaces.

### Azure Pipelines

When using Azure Pipelines with azd, the following variables are used for OIDC authentication:

- `SYSTEM_ACCESSTOKEN`: The Azure Pipelines system access token. Must be explicitly mapped in the pipeline task configuration by adding `SYSTEM_ACCESSTOKEN: $(System.AccessToken)` to the `env` section.
- `AZURESUBSCRIPTION_CLIENT_ID`: The client ID for the Azure service connection.
- `AZURESUBSCRIPTION_TENANT_ID`: The tenant ID for the Azure service connection.
- `AZURESUBSCRIPTION_SERVICE_CONNECTION_ID`: The Azure Pipelines service connection ID.

### Terraform

When using Terraform as the infrastructure provider, these environment variables are used for service principal authentication:

- `ARM_CLIENT_ID`: The Azure service principal client ID.
- `ARM_CLIENT_SECRET`: The Azure service principal client secret.
- `ARM_TENANT_ID`: The Azure service principal tenant ID.

## Extensions

- `AZD_EXT_DEBUG`: If true, enables debug logging for azd extensions.
- `AZD_EXT_TIMEOUT`: Overrides the default timeout for extension operations (in seconds).
- `AZD_SERVER`: Specifies the server address for azd extensions to connect to.
- `AZD_ACCESS_TOKEN`: Access token used for authentication between azd extensions and the core azd process.

## Cloud Shell

- `AZD_IN_CLOUDSHELL`: If true, indicates that `azd` is running in Azure Cloud Shell. This enables Cloud Shell-specific behaviors and optimizations.

## Debug and Development

These environment variables are primarily intended for azd developers and advanced troubleshooting. They are not typically needed for normal azd usage.

- `AZD_DEBUG_LOG`: If set, enables debug-level logging to help troubleshoot issues.
- `AZD_DEBUG_DOTNET_APPHOST_IGNORE_UNSUPPORTED_RESOURCES`: If true, ignores unsupported resources when working with .NET Aspire AppHost projects.
- `AZD_DEBUG_DOTNET_APPHOST_USE_FIXED_MANIFEST`: If true, uses a fixed manifest for .NET Aspire AppHost for testing purposes.
- `AZD_DEBUG_EXPERIMENTATION_TAS_ENDPOINT`: Overrides the default experimentation service endpoint for A/B testing features.
- `AZD_DEBUG_LOGIN_FORCE_SUBSCRIPTION_REFRESH`: If true, forces a refresh of the subscription list during login.
- `AZD_DEBUG_NO_ALPHA_WARNINGS`: If true, suppresses warnings about alpha features.
- `AZD_DEBUG_PROVISION_PROGRESS_DISABLE`: If true, disables the live progress display during infrastructure provisioning.
- `AZD_DEBUG_SERVER_DEBUG_ENDPOINTS`: If true, enables debug endpoints in the azd server.
- `AZD_DEBUG_SYNTHETIC_SUBSCRIPTION`: Specifies a synthetic subscription ID for testing purposes.

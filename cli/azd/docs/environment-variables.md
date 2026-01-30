# Environment Variables

## Environment variables used with azd

Environment variables that can be used to configure `azd` behavior, usually set within a shell or terminal. For environment variables that accept a boolean, the values `1, t, T, TRUE, true, True` are accepted as "true"; the values: `0, f, F, FALSE, false, False` are all accepted as "false".

---

## Core Azure Variables

These variables define fundamental Azure resources and configurations used by `azd`:

- `AZURE_ENV_NAME`: The name of the azd environment. This is the primary identifier for your environment and is typically set when creating a new environment.
- `AZURE_LOCATION`: The Azure region/location where resources are deployed (e.g., `eastus`, `westus2`).
- `AZURE_SUBSCRIPTION_ID`: The Azure subscription ID to use for deployments and resource management.
- `AZURE_TENANT_ID`: The Azure tenant ID that owns the subscription.
- `AZURE_PRINCIPAL_ID`: The principal (user or service principal) identity used for authentication.
- `AZURE_PRINCIPAL_TYPE`: The type of principal being used (e.g., `ServicePrincipal`).
- `AZURE_RESOURCE_GROUP`: The Azure resource group to use for deployments.
- `AZURE_CONTAINER_REGISTRY_ENDPOINT`: The endpoint of the Azure Container Registry for pushing container images.
- `AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN`: The default domain for Azure Container Apps environments.
- `AZURE_APP_SERVICE_DASHBOARD_URI`: The URL for the App Service Aspire Dashboard.
- `AZURE_AKS_CLUSTER_NAME`: The name of the Azure Kubernetes Service cluster.

---

## Dev Center Variables

These variables configure Azure Dev Center integration:

- `AZURE_DEVCENTER_NAME`: The name of the Azure Dev Center.
- `AZURE_DEVCENTER_PROJECT`: The Dev Center project name.
- `AZURE_DEVCENTER_CATALOG`: The catalog name within the Dev Center.
- `AZURE_DEVCENTER_ENVIRONMENT_TYPE`: The environment type for Dev Center environments.
- `AZURE_DEVCENTER_ENVIRONMENT_DEFINITION`: The environment definition to use.
- `AZURE_DEVCENTER_ENVIRONMENT_USER`: The user identity for Dev Center environments.

---

## General Configuration

Variables for general azd configuration and behavior:

- `AZD_CONFIG_DIR`: The file path of the user-level configuration directory. Overrides the default configuration location.
- `AZD_DEMO_MODE`: If true, enables demo mode. This hides personal output, such as subscription IDs, from being displayed in output.
- `AZD_FORCE_TTY`: If true, forces `azd` to write terminal-style output.
- `AZD_IN_CLOUDSHELL`: If true, `azd` runs with Azure Cloud Shell specific behavior.
- `AZD_SKIP_UPDATE_CHECK`: If true, skips the out-of-date update check output that is typically printed at the end of the command.
- `AZD_PLATFORM_TYPE`: Specifies the platform type for the current azd environment.
- `AZD_ALLOW_NON_EMPTY_FOLDER`: If true, allows `azd init` to initialize in non-empty folders.
- `AZD_CONTAINER_RUNTIME`: Specifies the container runtime to use (`docker` or `podman`).

---

## Alpha Features

- `AZD_ALPHA_ENABLE_<name>`: Enables or disables an alpha feature. `<name>` is the upper-cased name of the feature, with dot `.` characters replaced by underscore `_` characters. Example: `AZD_ALPHA_ENABLE_DEPLOYMENT_STACKS=true`.

---

## External Authentication

Variables for configuring external authentication providers:

- `AZD_AUTH_ENDPOINT`: The [External Authentication](./external-authentication.md) endpoint.
- `AZD_AUTH_KEY`: The [External Authentication](./external-authentication.md) shared key.
- `AZD_AUTH_CERT`: The [External Authentication](./external-authentication.md) certificate path.

---

## Tool Configuration

For tools that are auto-acquired by `azd`, you can configure these environment variables to use a different version of the tool installed on the machine:

- `AZD_BICEP_TOOL_PATH`: The Bicep tool override path. The direct path to `bicep` or `bicep.exe`.
- `AZD_GH_TOOL_PATH`: The `gh` CLI tool override path. The direct path to `gh` or `gh.exe`.
- `AZD_PACK_TOOL_PATH`: The `pack` tool override path. The direct path to `pack` or `pack.exe`.
- `AZD_BUILDER_IMAGE`: The builder Docker image used to perform Dockerfile-less builds.

---

## Extension Variables

Variables for configuring azd extensions:

- `AZD_EXT_TIMEOUT`: The timeout (in seconds) for extension operations.
- `AZD_EXT_DEBUG`: If true, enables debugger attachment for extensions.
- `AZD_EXTENSION_CACHE_TTL`: Time-to-live for the extension registry cache (e.g., `4h` for 4 hours).
- `AZD_SERVER`: The azd server address. Used by extensions to communicate with the azd server.
- `AZD_ACCESS_TOKEN`: Access token for authenticating with the azd server. Used by extensions.

---

## Telemetry & Tracing

Variables for telemetry collection and distributed tracing:

- `AZURE_DEV_COLLECT_TELEMETRY`: Controls telemetry collection. Set to `no` to disable telemetry.
- `AZURE_DEV_USER_AGENT`: Custom user agent string to include in Azure API calls.
- `TRACEPARENT`: W3C Trace Context traceparent header for distributed tracing.
- `TRACESTATE`: W3C Trace Context tracestate header for distributed tracing.

---

## CI/CD Variables

Variables used in CI/CD pipeline scenarios:

### Azure Pipelines
- `SYSTEM_ACCESSTOKEN`: The access token for Azure Pipelines authentication.
- `AZURESUBSCRIPTION_CLIENT_ID`: Client ID for the Azure service connection.
- `AZURESUBSCRIPTION_TENANT_ID`: Tenant ID for the Azure service connection.
- `AZURESUBSCRIPTION_SERVICE_CONNECTION_ID`: Service connection ID for Azure Pipelines.
- `AZURESUBSCRIPTION_SUBSCRIPTION_ID`: Subscription ID configured in the service connection.
- `SYSTEM_TEAMPROJECTID`: The team project ID in Azure DevOps.
- `SYSTEM_OIDCREQUESTURI`: OIDC request URI for federated authentication in Azure Pipelines.
- `TF_BUILD`: Set to `true` when running in Azure Pipelines.

### GitHub Actions
- `GITHUB_ACTIONS`: Set to `true` when running in GitHub Actions.
- `GITHUB_RUN_ID`: The unique identifier for a workflow run.
- `AZURE_OIDC_TOKEN`: OIDC token for GitHub Actions federated authentication.
- `AZURE_OIDC_REQUEST_TOKEN`: Request token for OIDC authentication.
- `AZURE_OIDC_REQUEST_URL`: Request URL for OIDC authentication.
- `ACTIONS_ID_TOKEN_REQUEST_TOKEN`: GitHub Actions ID token request token.
- `ACTIONS_ID_TOKEN_REQUEST_URL`: GitHub Actions ID token request URL.

### General CI
- `CI`: Set to `true` in most CI environments to indicate running in CI.
- `AZD_INITIAL_ENVIRONMENT_CONFIG`: (Deprecated) JSON string containing initial environment configuration for CI/CD. Used for backwards compatibility. Prefer individual environment variables instead.

---

## Terraform Provider Variables

Variables used by the Terraform infrastructure provider:

- `ARM_TENANT_ID`: Azure tenant ID for Terraform provider authentication.
- `ARM_CLIENT_ID`: Azure client ID for Terraform provider authentication.
- `ARM_CLIENT_SECRET`: Azure client secret for Terraform provider authentication.
- `ARM_SUBSCRIPTION_ID`: Azure subscription ID for Terraform provider.

---

## Console & Terminal

Variables affecting console output and terminal behavior:

- `NO_COLOR`: If set (any value), disables colored output. Follows the [NO_COLOR standard](https://no-color.org/).
- `FORCE_COLOR`: If set, forces colored output even when not in an interactive terminal.
- `COLUMNS`: Overrides the detected console width (number of columns).
- `TERM`: Terminal type identifier. Used for terminal capability detection.
- `BROWSER`: Specifies the browser to use for opening URLs (e.g., during authentication).

---

## Debug Variables

⚠️ **These variables are intended for internal debugging and development purposes.** They should not be used in production environments.

- `AZD_DEBUG`: If true, prompts for debugger attachment at startup.
- `AZD_DEBUG_LOG`: Configures debug logging output. Can be set to file paths or `stderr` for debug output.
- `AZD_DEBUG_TELEMETRY`: If true, enables debugging for background telemetry processes.
- `AZD_DEBUG_LOGIN_FORCE_SUBSCRIPTION_REFRESH`: Forces subscription list refresh during login.
- `AZD_DEBUG_SYNTHETIC_SUBSCRIPTION`: Enables synthetic subscription for testing purposes.
- `AZD_DEBUG_NO_ALPHA_WARNINGS`: Suppresses warnings about alpha features.
- `AZD_DEBUG_PROVISION_PROGRESS_DISABLE`: Disables progress display during provisioning.
- `AZD_DEBUG_DOTNET_APPHOST_USE_FIXED_MANIFEST`: Uses a fixed manifest for .NET Aspire app host.
- `AZD_DEBUG_DOTNET_APPHOST_IGNORE_UNSUPPORTED_RESOURCES`: Ignores unsupported resources in .NET Aspire app host.
- `AZD_DEBUG_SERVER_DEBUG_ENDPOINTS`: Enables debug endpoints in the Visual Studio RPC server.
- `AZD_DEBUG_EXPERIMENTATION_TAS_ENDPOINT`: Overrides the experimentation service endpoint.
- `AZD_SUBSCRIPTIONS_FETCH_MAX_CONCURRENCY`: Limits the concurrency for fetching subscriptions.
- `DEPLOYMENT_STACKS_BYPASS_STACK_OUT_OF_SYNC_ERROR`: Bypasses stack out-of-sync errors when using deployment stacks.

---

## Test Variables

⚠️ **These variables are used exclusively by azd's test infrastructure.** They should not be used outside of testing scenarios.

- `AZD_TEST_CLIENT_ID`: Client ID for functional tests.
- `AZD_TEST_TENANT_ID`: Tenant ID for functional tests.
- `AZD_TEST_AZURE_SUBSCRIPTION_ID`: Subscription ID for functional tests.
- `AZD_TEST_AZURE_LOCATION`: Azure location for functional tests.
- `AZD_TEST_CLI_VERSION`: Specifies the CLI version to test against.
- `AZD_TEST_FIXED_CLOCK_UNIX_TIME`: Unix timestamp for test recording/playback with fixed time.
- `AZD_TEST_HTTPS_PROXY`: HTTPS proxy URL for test recording.
- `AZD_TEST_DOCKER_E2E`: If set to `1`, enables Docker E2E tests.
- `AZD_FUNC_TEST`: Indicates running in functional test mode.
- `AZURE_RECORD_MODE`: Controls recording mode for Azure SDK tests.
- `CLI_TEST_AZD_PATH`: Path to the azd binary for CLI tests.
- `CLI_TEST_SKIP_BUILD`: If true, skips building azd in CLI tests.

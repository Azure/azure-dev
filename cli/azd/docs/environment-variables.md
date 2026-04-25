# Environment Variables

Comprehensive reference of environment variables used by `azd`.

For environment variables that accept a boolean, the values `1, t, T, TRUE, true, True` are accepted
as "true"; the values `0, f, F, FALSE, false, False` are all accepted as "false".

## Core Azure Variables

These variables are typically set by infrastructure provisioning outputs and stored in the `.env` file
for each environment.

| Variable | Description |
| --- | --- |
| `AZURE_ENV_NAME` | The name of the active azd environment. |
| `AZURE_LOCATION` | The default Azure region for resource deployment. |
| `AZURE_SUBSCRIPTION_ID` | The Azure subscription ID used for deployment. |
| `AZURE_TENANT_ID` | The Microsoft Entra tenant ID. |
| `AZURE_PRINCIPAL_ID` | The object ID of the signed-in principal. |
| `AZURE_PRINCIPAL_TYPE` | The type of the signed-in principal (e.g., `User`, `ServicePrincipal`). |
| `AZURE_RESOURCE_GROUP` | The default resource group name. |
| `AZURE_CONTAINER_REGISTRY_ENDPOINT` | The endpoint of the Azure Container Registry. |
| `AZURE_CONTAINER_APPS_ENVIRONMENT_DEFAULT_DOMAIN` | The default domain of the Container Apps environment. |
| `AZURE_APP_SERVICE_DASHBOARD_URI` | The URI for the .NET Aspire dashboard hosted on Azure App Service. |
| `AZURE_AKS_CLUSTER_NAME` | The name of the Azure Kubernetes Service cluster. |

## Dev Center Variables

Variables for [Azure Dev Center](https://learn.microsoft.com/azure/dev-box/overview-what-is-microsoft-dev-box)
integration.

| Variable | Description |
| --- | --- |
| `AZURE_DEVCENTER_NAME` | The name of the Dev Center instance. |
| `AZURE_DEVCENTER_PROJECT` | The Dev Center project name. |
| `AZURE_DEVCENTER_CATALOG` | The catalog name within Dev Center. |
| `AZURE_DEVCENTER_ENVIRONMENT_TYPE` | The environment type in Dev Center (e.g., `Dev`, `Test`, `Prod`). |
| `AZURE_DEVCENTER_ENVIRONMENT_DEFINITION` | The environment definition name. |
| `AZURE_DEVCENTER_ENVIRONMENT_USER` | The user identity for the Dev Center environment. |

## General Configuration

| Variable | Description |
| --- | --- |
| `AZD_CONFIG_DIR` | The file path of the user-level configuration directory. |
| `AZD_DEMO_MODE` | If true, enables demo mode. This hides personal output, such as subscription IDs, from being displayed in output. |
| `AZD_FORCE_TTY` | If true, forces `azd` to write terminal-style output. |
| `AZD_IN_CLOUDSHELL` | If true, `azd` runs with Azure Cloud Shell specific behavior. |
| `AZD_SKIP_UPDATE_CHECK` | If true, skips the out-of-date update check output that is typically printed at the end of the command. |
| `AZD_CONTAINER_RUNTIME` | The container runtime to use (e.g., `docker`, `podman`). |
| `AZD_ALLOW_NON_EMPTY_FOLDER` | If set, allows `azd init` to run in a non-empty directory without prompting. |
| `AZD_BUILDER_IMAGE` | The builder docker image used to perform Dockerfile-less builds. |
| `AZD_DEPLOY_TIMEOUT` | Timeout for deployment operations, parsed as an integer number of seconds (for example, `1200`). Defaults to `1200` seconds (20 minutes). |
| `AZD_DEPLOY_{SERVICE}_SLOT_NAME` | Sets the App Service deployment slot target for a service. Replace `{SERVICE}` with the uppercase service name (hyphens become underscores). Set to `production` to deploy to the main app, or a slot name (e.g., `staging`). When slots exist and this is not set, `--no-prompt` mode fails with an error listing available targets. |

## Extension Variables

These variables are set and consumed by azd extension hosts (for example, IDE/editor integrations)
and the azd extension framework. They are not intended to be configured as general CLI settings.

| Variable | Description |
| --- | --- |
| `AZD_NO_PROMPT` | If true, disables interactive prompts. Typically set by extension hosts for non-interactive behavior. |
| `AZD_ENVIRONMENT` | The azd environment name provided by the extension host when invoking azd. |
| `AZD_CWD` | The working directory path used by the extension host when invoking azd. |
| `AZD_SERVER` | The address (e.g., `localhost:12345`) of the azd extension server for gRPC communication. Injected and consumed by the extension framework; not typically set by users directly. |
| `AZD_ACCESS_TOKEN` | A JWT used to authenticate gRPC calls to the azd extension server. Injected and consumed by the extension framework; not typically set by users directly. |

## Alpha Features

| Variable | Description |
| --- | --- |
| `AZD_ALPHA_ENABLE_ALL` | Enables all alpha features at once. |
| `AZD_ALPHA_ENABLE_<name>` | Enables or disables an alpha feature. `<name>` is the upper-cased name of the feature, with dot `.` characters replaced by underscore `_` characters. |

## External Authentication

Variables for [External Authentication](./external-authentication.md) integration.

| Variable | Description |
| --- | --- |
| `AZD_AUTH_ENDPOINT` | The [External Authentication](./external-authentication.md) endpoint. |
| `AZD_AUTH_KEY` | The [External Authentication](./external-authentication.md) shared key. |
| `AZD_AUTH_CERT` | The [External Authentication](./external-authentication.md) client certificate, provided as a base64-encoded DER certificate string. When set, `AZD_AUTH_ENDPOINT` must use HTTPS. |

## Tool Configuration

For tools that are auto-acquired by `azd`, the following environment variables configure the path to a
specific version of the tool installed on the machine.

| Variable | Description |
| --- | --- |
| `AZD_BICEP_TOOL_PATH` | The Bicep tool override path. The direct path to `bicep` or `bicep.exe`. |
| `AZD_GH_TOOL_PATH` | The `gh` tool override path. The direct path to `gh` or `gh.exe`. |
| `AZD_PACK_TOOL_PATH` | The `pack` tool override path. The direct path to `pack` or `pack.exe`. |
| `AZD_COPILOT_CLI_PATH` | The Copilot CLI tool override path. When set, skips automatic download and uses the specified path. |

## Extension Configuration

| Variable | Description |
| --- | --- |
| `AZD_EXT_TIMEOUT` | Timeout for extension operations, parsed as an integer number of seconds (for example, `10`). Defaults to `5` seconds; this is not a duration string, so values like `10m` are not valid. |
| `AZD_EXT_DEBUG` | If true, enables debug output for extensions. |
| `AZD_EXTENSION_CACHE_TTL` | Time-to-live for extension cache entries, parsed with Go's `time.ParseDuration` format (for example, `30m`, `4h`). Defaults to `4h`. |

## Extension-Specific Variables

> **Note**: These variables are defined and consumed by individual azd extensions. As the extension
> ecosystem grows, extension-specific variables may move to each extension's own documentation.

### azure.ai.agents

| Variable | Description |
| --- | --- |
| `AZURE_AI_PROJECT_ID` | The Microsoft Foundry project resource ID used by the `azure.ai.agents` extension. |
| `AZURE_AI_PROJECT_ENDPOINT` | The Microsoft Foundry project endpoint used by the `azure.ai.agents` extension. |
| `AZURE_AI_PROJECT_PRINCIPAL_ID` | The principal ID associated with the Microsoft Foundry project identity. |
| `AZURE_AI_ACCOUNT_NAME` | The Microsoft Foundry account name associated with the project. |
| `AZURE_AI_PROJECT_NAME` | The Microsoft Foundry project name. |
| `AZURE_AI_MODEL_DEPLOYMENT_NAME` | The default model deployment name used for generated agent code and templates. |
| `AZURE_AI_PROJECT_ACR_CONNECTION_NAME` | The Azure Container Registry connection name used by the extension for hosted agents. |
| `AI_PROJECT_DEPLOYMENTS` | JSON-encoded deployment metadata populated by the extension for agent workflows. |
| `AI_PROJECT_DEPENDENT_RESOURCES` | JSON-encoded dependent resource metadata populated by the extension for agent workflows. |
| `ENABLE_HOSTED_AGENTS` | If set, indicates that hosted agents are enabled for the current azd environment. |
| `ENABLE_CONTAINER_AGENTS` | If set, indicates that container agents are enabled for the current azd environment. |
| `AGENT_DEFINITION_PATH` | Path to an agent definition file for AI agent workflows. |

## UI Prompt Integration

| Variable | Description |
| --- | --- |
| `AZD_UI_PROMPT_ENDPOINT` | The endpoint for external UI prompt service integration. |
| `AZD_UI_PROMPT_KEY` | The authentication key for the external UI prompt service. |
| `AZD_UI_NO_PROMPT_DIALOG` | Set to any non-empty value to disable prompt dialog UI. |

## Telemetry & Tracing

| Variable | Description |
| --- | --- |
| `AZURE_DEV_COLLECT_TELEMETRY` | If false, disables telemetry collection. Telemetry is enabled by default. |
| `AZURE_DEV_USER_AGENT` | Appends a custom string to the `User-Agent` header sent with Azure requests. |
| `TRACEPARENT` | The W3C Trace Context `traceparent` header for distributed tracing. Automatically set by `azd` on extension processes for trace propagation. Not typically set by users. |
| `TRACESTATE` | The W3C Trace Context `tracestate` header for vendor-specific trace data. Automatically set by `azd` alongside `TRACEPARENT`. Not typically set by users. |

## CI/CD Variables

These variables are read by `azd` to detect and integrate with CI/CD systems.

### Azure Pipelines

| Variable | Description |
| --- | --- |
| `TF_BUILD` | Set to `True` when running in Azure Pipelines. |
| `BUILD_BUILDID` | The build ID in Azure Pipelines. |
| `BUILD_BUILDNUMBER` | The build number in Azure Pipelines. |
| `SYSTEM_ACCESSTOKEN` | The access token for Azure Pipelines service connections. |
| `SYSTEM_TEAMPROJECTID` | The Team Project ID in Azure DevOps. |
| `SYSTEM_OIDCREQUESTURI` | The OIDC request URI for federated identity in Azure Pipelines. |
| `AZURESUBSCRIPTION_CLIENT_ID` | The client ID from the Azure service connection. |
| `AZURESUBSCRIPTION_TENANT_ID` | The tenant ID from the Azure service connection. |
| `AZURESUBSCRIPTION_SERVICE_CONNECTION_ID` | The service connection ID in Azure DevOps. |
| `AZURESUBSCRIPTION_SUBSCRIPTION_ID` | The subscription ID from the Azure service connection. |

### GitHub Actions

| Variable | Description |
| --- | --- |
| `GITHUB_ACTIONS` | Set to `true` when running in GitHub Actions. |
| `GITHUB_RUN_ID` | The unique ID of the current GitHub Actions workflow run. |
| `AZURE_OIDC_TOKEN` | An OIDC token for Azure federated credential authentication. |
| `AZURE_OIDC_REQUEST_TOKEN` | The request token for Azure OIDC in GitHub Actions. |
| `AZURE_OIDC_REQUEST_URL` | The request URL for Azure OIDC in GitHub Actions. |
| `ACTIONS_ID_TOKEN_REQUEST_TOKEN` | The GitHub Actions OIDC request token. |
| `ACTIONS_ID_TOKEN_REQUEST_URL` | The GitHub Actions OIDC request URL. |

### GitHub Codespaces

| Variable | Description |
| --- | --- |
| `CODESPACES` | Set to `true` when running in GitHub Codespaces. Used by `azd` for environment detection and tracing. |

### General CI

| Variable | Description |
| --- | --- |
| `CI` | Set to `true` when running in a generic CI environment. |

## Terraform Provider Variables

These variables are used by the Terraform provider integration to authenticate with Azure.

| Variable | Description |
| --- | --- |
| `ARM_TENANT_ID` | The Azure tenant ID for Terraform Azure provider. |
| `ARM_CLIENT_ID` | The Azure client ID for Terraform Azure provider. |
| `ARM_CLIENT_SECRET` | The Azure client secret for Terraform Azure provider. |
| `ARM_SUBSCRIPTION_ID` | The Azure subscription ID for Terraform Azure provider. |

## Console & Terminal

| Variable | Description |
| --- | --- |
| `NO_COLOR` | If set, disables color output. See [no-color.org](https://no-color.org). |
| `FORCE_COLOR` | Set to `1` to force color output regardless of terminal detection. Only the exact value `1` is recognized. |
| `COLUMNS` | Overrides the detected terminal width (in columns). |
| `TERM` | The terminal type. Used to detect terminal capabilities. |
| `BROWSER` | The browser command to use for opening URLs (e.g., during `azd auth login`). |

## Debug Variables

> **Warning**: Debug variables are unsupported and may change or be removed without notice.

| Variable | Description |
| --- | --- |
| `AZD_DEBUG` | If true, enables debug mode. |
| `AZD_DEBUG_LOG` | If true, enables debug-level logging. |
| `AZD_DEBUG_TELEMETRY` | If true, enables debug-level telemetry output. |
| `AZD_DEBUG_MSAL_CACHE` | If true, logs MSAL cache metadata before and after login and around the first silent token acquisitions, including account identifiers and usernames, while hashing cache keys and token secrets. |
| `AZD_DEBUG_LOGIN_FORCE_SUBSCRIPTION_REFRESH` | If true, forces a refresh of the subscription list on login. |
| `AZD_DEBUG_SYNTHETIC_SUBSCRIPTION` | If set, provides a synthetic subscription for testing. |
| `AZD_DEBUG_NO_ALPHA_WARNINGS` | If true, suppresses alpha feature warnings. |
| `AZD_DEBUG_PROVISION_PROGRESS_DISABLE` | If true, disables provision progress display. Read by both the Bicep provider and the Dev Center provisioner. |
| `AZD_DEBUG_DOTNET_APPHOST_USE_FIXED_MANIFEST` | If true, uses a fixed manifest for .NET Aspire app host. |
| `AZD_DEBUG_DOTNET_APPHOST_IGNORE_UNSUPPORTED_RESOURCES` | If true, ignores unsupported resources in .NET Aspire app host. |
| `AZD_DEBUG_SERVER_DEBUG_ENDPOINTS` | If true, enables debug endpoints in server mode. |
| `AZD_DEBUG_EXPERIMENTATION_TAS_ENDPOINT` | Overrides the experimentation TAS endpoint URL. |
| `AZD_SUBSCRIPTIONS_FETCH_MAX_CONCURRENCY` | Limits the maximum concurrency when fetching subscriptions. |
| `DEPLOYMENT_STACKS_BYPASS_STACK_OUT_OF_SYNC_ERROR` | If true, bypasses Deployment Stacks out-of-sync errors. |

## Test Variables

> **Warning**: Test variables are used by the `azd` test suite only and are not intended for end users.
>
> **Tip**: Instead of setting environment variables for every session, you can persist test defaults
> in your user-level `azd` config. These config keys act as fallbacks when the corresponding
> environment variable is not set:
>
> ```bash
> azd config set defaults.test.subscription <SUBSCRIPTION_ID>
> azd config set defaults.test.tenant <TENANT_ID>
> azd config set defaults.test.location <LOCATION>
> ```
>
> Resolution order: environment variable → `defaults.test.*` → `defaults.*` (global default).
> Note: `AZD_TEST_TENANT_ID` only falls back to `defaults.test.tenant` (no
> `defaults.tenant` global fallback). Config fallbacks are only consulted when
> the `CI` environment variable is unset.

| Variable | Description | Config Fallback |
| --- | --- | --- |
| `AZD_TEST_CLIENT_ID` | The client ID for test authentication. | — |
| `AZD_TEST_TENANT_ID` | The tenant ID for test authentication. | `defaults.test.tenant` |
| `AZD_TEST_AZURE_SUBSCRIPTION_ID` | The Azure subscription ID for tests. | `defaults.test.subscription` |
| `AZD_TEST_AZURE_LOCATION` | The Azure location for tests. | `defaults.test.location` |
| `AZD_TEST_CLI_VERSION` | Overrides the CLI version reported during tests. |
| `AZD_TEST_FIXED_CLOCK_UNIX_TIME` | Sets a fixed clock time (Unix epoch) for deterministic tests. |
| `AZD_TEST_HTTPS_PROXY` | The HTTPS proxy URL for tests. |
| `AZD_TEST_DOCKER_E2E` | If true, enables Docker-based end-to-end tests. |
| `AZD_FUNC_TEST` | If true, indicates functional test mode. |
| `UPDATE_SNAPSHOTS` | If set, updates test snapshots when running snapshot-based tests. |
| `AZURE_RECORD_MODE` | Sets the record mode for Azure SDK test recordings. Valid values: `live`, `playback`, `record`. |
| `CLI_TEST_AZD_PATH` | Overrides the `azd` binary path used in CLI tests. |
| `CLI_TEST_SKIP_BUILD` | If true, skips building `azd` before running tests. |

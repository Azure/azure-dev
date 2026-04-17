# Release History

## 0.1.24-preview (Unreleased)

### Breaking Changes

- Removed `-e` shorthand for `--environment`; use `--environment` instead. This resolves a collision with the azd global `-e/--environment` flag.

## 0.1.23-preview (2026-04-16)

- [[#7753]](https://github.com/Azure/azure-dev/pull/7753) Fix `azd ai agent init` to pass the current directory as a positional argument to `azd init`, resolving failures caused by a missing `cwd` assumption in the underlying azd call.
- [[#7729]](https://github.com/Azure/azure-dev/pull/7729) Fix `azd ai agent run` for .NET agents.
- [[#7574]](https://github.com/Azure/azure-dev/pull/7574) Add `azd ai agent sessions` command group for listing, creating, and deleting agent sessions; improve `azd ai agent files` commands with positional argument support; rename `--session` flag to `--session-id` across all commands.
- [[#7725]](https://github.com/Azure/azure-dev/pull/7725) Improve `--protocol` flag handling in `azd ai agent invoke` to correctly resolve the protocol when multiple protocols are configured.
- [[#7640]](https://github.com/Azure/azure-dev/pull/7640) Add toolbox (MCP) support: provision MCP toolbox connections via `FOUNDRY_TOOLBOX_*` environment variables and add OAuth connection fields (`authorizationUrl`, `tokenUrl`, `refreshUrl`, `scopes`, `audience`, `connectorName`) to connection resources in `azure.yaml`.
- [[#7614]](https://github.com/Azure/azure-dev/pull/7614) Fail fast on `azd ai agent init` when the user is not logged in, before any file-modifying operations begin.
- [[#7679]](https://github.com/Azure/azure-dev/pull/7679) Add `--protocol` flag to `azd ai agent invoke` to explicitly select between `responses` and `invocations` protocols when multiple are configured; return a clear error when `invocations` is requested but not enabled.
- [[#7675]](https://github.com/Azure/azure-dev/pull/7675) Add unit tests and testdata for the extension, covering agent YAML mapping, registry API helpers, API model serialization, and command utilities.

## 0.1.22-preview (2026-04-10)

- [[#7633]](https://github.com/Azure/azure-dev/pull/7633) Fix `azd ai agent init` to correctly set `AZURE_AI_MODEL_DEPLOYMENT_NAME` when initializing from a manifest, template, or `--model`/`--model-deployment` flags.
- [[#7635]](https://github.com/Azure/azure-dev/pull/7635) Fix `azd ai agent invoke` to persist an explicitly passed `--session-id` so that subsequent `azd ai agent monitor` calls can reuse the session without error.
- [[#7636]](https://github.com/Azure/azure-dev/pull/7636) Add positional argument support to `azd ai agent init`; passing a URL, manifest path, or source directory is now auto-disambiguated and equivalent to using `--manifest` or `--src`.
- [[#7645]](https://github.com/Azure/azure-dev/pull/7645) Fix `azd ai agent init -m` when adding to an existing azd project: reuse the Foundry project from the environment, show a message when an existing azd project is detected, and prompt to resolve service name collisions.

### Breaking Changes

- [[#7651]](https://github.com/Azure/azure-dev/pull/7651) Switch agent identity RBAC from a shared project-level identity to per-agent identities (`{account}-{project}-{agentName}-AgentIdentity`), add developer RBAC pre-flight checks before deploy, and remove Cognitive Services OpenAI User and Monitoring Metrics Publisher role assignments; set `AZD_AGENT_SKIP_ROLE_ASSIGNMENTS=true` to skip all role assignments in CI/CD environments.

## 0.1.21-preview (2026-04-09)

- [[#7484]](https://github.com/Azure/azure-dev/pull/7484) Detect an `agent.manifest.yaml` in the current directory and prompt to use it when running `azd ai agent init`.
- [[#7464]](https://github.com/Azure/azure-dev/pull/7464) Prompt for agent communication protocol (responses or invocations) when using `azd ai agent init` with local code.
- [[#7415]](https://github.com/Azure/azure-dev/pull/7415) Filter `azd ai agent init` prompts to only show locations and models supported for agent scenarios.
- [[#7410]](https://github.com/Azure/azure-dev/pull/7410) Fix `azd ai agent init --project-id` when `agent.yaml` does not contain a model resource.
- [[#7545]](https://github.com/Azure/azure-dev/pull/7545) Update agent endpoint handling to use the latest Foundry agent service endpoints.
- [[#7538]](https://github.com/Azure/azure-dev/pull/7538) Fix `azd ai agent invoke` streaming output to print each SSE data object on its own line.
- [[#7576]](https://github.com/Azure/azure-dev/pull/7576) Add validation to `azd ai agent init` to ensure the manifest path points to a file, not a directory.
- [[#7553]](https://github.com/Azure/azure-dev/pull/7553) Update `azd ai agent init` to stop writing `AZURE_AI_PROJECT_ENDPOINT` and `AZURE_OPENAI_ENDPOINT` to `agent.yaml`; `azd ai agent run` now translates `AZURE_AI_*` env vars to `FOUNDRY_*` equivalents for local agent processes.
- [[#7596]](https://github.com/Azure/azure-dev/pull/7596) Reduce noisy output during `azd ai agent init` by redirecting internal log statements to the debug log file; verbose details are now only visible when `--debug` is used.
- [[#7607]](https://github.com/Azure/azure-dev/pull/7607) Fix `azd ai agent init` container resource selection to save the chosen CPU/memory tier into `agent.yaml` and pre-select the existing tier on reruns; remove stale replica mention from post-init message.
- [[#7589]](https://github.com/Azure/azure-dev/pull/7589) Fix `azd ai agent init` to prompt for an existing Foundry project when the agent manifest contains no model resources.

## 0.1.20-preview (2026-04-02)

- [[#7422]](https://github.com/Azure/azure-dev/pull/7422) Add `/invocations` API support to `azd ai agent invoke`, enabling agents to accept arbitrary input passed directly to the agent.
- [[#7341]](https://github.com/Azure/azure-dev/pull/7341) Fix `azd ai agent init` writing unnecessary `scale` configuration for vnext-hosted agents, which is now skipped when vnext is enabled. Thanks @spboyer for the contribution!

## 0.1.19-preview (2026-03-31)

- [[#7327]](https://github.com/Azure/azure-dev/pull/7327) Fix `azd ai agent init` reruns to reuse an existing azd environment instead of failing when a previous attempt already created it.
- [[#7332]](https://github.com/Azure/azure-dev/pull/7332) Improve `azd ai agent init` performance when discovering existing Foundry projects, including faster `--project-id` validation.
- [[#7355]](https://github.com/Azure/azure-dev/pull/7355) Update generated Application Insights environment variables to reuse the selected connection and avoid redundant connection creation during deployment.
- [[#7373]](https://github.com/Azure/azure-dev/pull/7373) Fix `postdeploy` hook failures in projects without hosted agents so unrelated azd projects no longer error during deploy.

## 0.1.18-preview (2026-03-23)

- [[#7147]](https://github.com/Azure/azure-dev/pull/7147) Add `azd ai agent init` support for initializing from an agent template.

## 0.1.17-preview (2026-03-20)

- [[#7214]](https://github.com/Azure/azure-dev/pull/7214) Add ASCII art banner with Foundry branding and version info displayed at extension startup.
- [[#7217]](https://github.com/Azure/azure-dev/pull/7217) Update container settings to use discrete CPU and memory options, and remove min/max replicas prompts.

## 0.1.16-preview (2026-03-18)

- [[#7141]](https://github.com/Azure/azure-dev/pull/7141) Add `azd ai agent files` command group with `upload`, `download`, `list`, and `remove` subcommands for managing session-scoped files on hosted agent sandboxes.
- [[#7175]](https://github.com/Azure/azure-dev/pull/7175) Improve input validation, error handling, and path safety across the extension, including hardened path resolution, sensitive data redaction in error paths, and WebSocket origin validation.

### Breaking Changes

- [[#7181]](https://github.com/Azure/azure-dev/pull/7181) Update `azd ai agent show` and `azd ai agent monitor` commands to read agent name and version from the azd service entry, removing the requirement to pass them as parameters.

## 0.1.15-preview (2026-03-13)

- [[#7080]](https://github.com/Azure/azure-dev/pull/7080) Fix authentication failures (`AADSTS70043`/`AADSTS700082`) for multi-tenant and guest users by using `UserTenantId` for credential resolution

## 0.1.14-preview (2026-03-10)

- [[#7026]](https://github.com/Azure/azure-dev/pull/7026) Add `azd ai agent run` and `azd ai agent invoke` commands for running agents locally and invoking agents via a /responses call
- [[#6980]](https://github.com/Azure/azure-dev/pull/6980) Add `--model-deployment` parameter to `azd ai agent init` and fix agent init in copilot/CI scenarios
- [[#6979]](https://github.com/Azure/azure-dev/pull/6979) Detect and classify auth errors from azd core for improved error telemetry

## 0.1.13-preview (2026-03-04)

- [[#6957]](https://github.com/Azure/azure-dev/pull/6957) Fix unmarshal error during `azd ai agent init`

## 0.1.12-preview (2026-02-27)

- [[#6892]](https://github.com/Azure/azure-dev/pull/6892) Fix selected model check during `azd ai agent init` from code to correctly handle existing versus new model deployments
- [[#6909]](https://github.com/Azure/azure-dev/pull/6909) Add `AZURE_AI_PROJECT_ENDPOINT` to default agent environment variables and improve `AZURE_AI_MODEL_DEPLOYMENT_NAME` env var handling
- [[#6895]](https://github.com/Azure/azure-dev/pull/6895) Add `azd ai agent logs` and `azd ai agent status` commands for viewing agent run logs and deployment status
- [[#6901]](https://github.com/Azure/azure-dev/pull/6901) Add structured error handling with improved service error mapping for more informative error messages

## 0.1.11-preview (2026-02-24)

- [[#6828]](https://github.com/Azure/azure-dev/pull/6828) Add new "init from code" flow allowing users to run `azd ai agent init` without an existing project, template, or manifest
- [[#6867]](https://github.com/Azure/azure-dev/pull/6867) Add default model selection for the basic init flow

## 0.1.10-preview (2026-02-19)

- [[#6749]](https://github.com/Azure/azure-dev/pull/6749) Add "Choose a different model (all regions)" option during model selection recovery
- [[#6749]](https://github.com/Azure/azure-dev/pull/6749) Display quota availability info in model deployment prompts
- [[#6749]](https://github.com/Azure/azure-dev/pull/6749) Improve `AZURE_AI_PROJECT_ID` and deployment capacity validation

## 0.1.9-preview (2026-02-05)

- [[#6631]](https://github.com/Azure/azure-dev/pull/6631) Add support for downloading manifests from public repositories without authentication
- [[#6665]](https://github.com/Azure/azure-dev/pull/6665) Fix manifest download path handling when path contains slashes
- [[#6670]](https://github.com/Azure/azure-dev/pull/6670) Simplify `azd ai agent init` to use `--minimal` flag, reducing prompts
- [[#6672]](https://github.com/Azure/azure-dev/pull/6672) Block attempts to use extension with prompt agents (not yet supported)
- [[#6683]](https://github.com/Azure/azure-dev/pull/6683) Fix panic when parsing `agent.yaml` files without a `template` field
- [[#6693]](https://github.com/Azure/azure-dev/pull/6693) Fix unsafe DefaultAzureCredential usage
- [[#6695]](https://github.com/Azure/azure-dev/pull/6695) Display agent endpoint as plain text with documentation link instead of clickable hyperlink
- [[#6730]](https://github.com/Azure/azure-dev/pull/6730) Improve model selection handling when model is unavailable in current region

## 0.1.8-preview (2026-01-26)

- [[#6611]](https://github.com/Azure/azure-dev/pull/6611) Statically link the Linux amd64 binary for compatibility with older Linux versions

## 0.1.6-preview (2026-01-22)

- [[#6541]](https://github.com/Azure/azure-dev/pull/6541) Add metadata capability
- [[#6541]](https://github.com/Azure/azure-dev/pull/6541) Support `AZD_EXT_DEBUG=true` for debugging

## 0.1.5-preview (2026-01-12)

- [[#6468]](https://github.com/Azure/azure-dev/pull/6468) Add support for retrieving existing Application Insights connections when using `--project-id`
- [[#6482]](https://github.com/Azure/azure-dev/pull/6482) Improve `azd ai agent init -m` validation

## 0.1.4-preview (2025-12-15)

- [[#6326]](https://github.com/Azure/azure-dev/pull/6326) Fix correlation ID propagation and improve tracing for API calls
- [[#6343]](https://github.com/Azure/azure-dev/pull/6343) Improve `azd ai agent init` completion message to recommend `azd up` first
- [[#6344]](https://github.com/Azure/azure-dev/pull/6344) Rename `AI_FOUNDRY_PROJECT_APP_ID` environment variable to `AZURE_AI_PROJECT_PRINCIPAL_ID`
- [[#6366]](https://github.com/Azure/azure-dev/pull/6366) Fix manifest URL path when branch name contains "/"

## 0.1.3-preview (2025-12-03)

- Improve agent service debug logging via `AZD_EXT_DEBUG` env var and `--debug` flag

## 0.1.2-preview (2025-11-20)

- Update extension name and descriptions
- Update user facing text to use Microsoft Foundry

## 0.1.1-preview (2025-11-17)

- Fix min and max replicas not being set during agent deployment
- Fix `azd show` not displaying agent endpoint
- Polish user prompts and messages

## 0.1.0-preview (2025-11-14)

- Apply defaults instead of prompting in event handlers
- Process model resources as parameters
- Update env var generation to support multi-agent projects
- Polish error messages
- Improve local manifest handling
- Fix agent playground URL generation
- Fix panic when container settings is nil

## 0.0.7 (2025-11-13)

- Add prompting for container resources
- Add "preview" label to extension name and command descriptions
- Show agent playground URL post-deploy
- Support fetching ACR connections from existing AI Foundry projects
- Fix environment variable references
- Improve agent name validation

## 0.0.6 (2025-11-11)

- Add support for using existing AI model deployments
- Add `--project-id` flag for initializing using existing AI Foundry projects
- Fix agent definition handling for saved templates

## 0.0.5 (2025-11-06)

- Add support for tools
- Improve defaulting logic and --no-prompt support
- Fix remote build support

## 0.0.4 (2025-11-05)

- Add support for --no-prompt and --environment flags in `azd ai agent init`
- Include operation ID in timeout error
- Fix env vars not being included in agent create request

## 0.0.3 (2025-11-04)

- Add support for latest MAML format
- Fix agent endpoint handling for prompt agents

## 0.0.2 (2025-10-31)

- Add --host flag to `azd ai agent init`
- Rename host type to `azure.ai.agent`
- Store model information in service config
- Display agent endpoint on successful deploy
- Improve error handling
- Fix panic when no default model capacity is returned

## 0.0.1 (2025-10-28)

- Initial release

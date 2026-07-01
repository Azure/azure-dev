# Release History

## 1.0.0-beta.2 (2026-07-01)

### Bugs Fixed

- [[#8901]](https://github.com/Azure/azure-dev/pull/8901) Remove duplicate service-target provider claims from the `azure.ai.agents` extension manifest for hosts now owned by the split Foundry extensions (`azure.ai.projects`, `azure.ai.connections`, `azure.ai.toolboxes`). Thanks @huimiu for the contribution!
- [[#8586]](https://github.com/Azure/azure-dev/issues/8586) `azd ai agent files upload` now accepts `[agent] [file]` positional arguments, mirroring `azd ai agent invoke [agent] [message]`. The first positional is the agent name and the second is the file to upload (with a single positional, it is the file, or the agent when `--file/-f` already supplies the file). This fixes the previous trap where passing the agent name as the positional left the agent unset and, in multi-service projects without `-n/--agent-name`, hung silently on the interactive agent picker in non-TTY contexts.

## 1.0.0-beta.1 (2026-06-30)

### Features Added

- [[#8885]](https://github.com/Azure/azure-dev/pull/8885) `azd ai agent init -m <pointer>` now adopts a sample's unified `azure.yaml` as the project manifest when the pointer (local path or GitHub URL) resolves to one — that is, a manifest whose `services:` declare Foundry hosts (`azure.ai.project` / `azure.ai.agent` / `azure.ai.connection` / `azure.ai.toolbox`). The sample's `azure.yaml` and the files it references are placed at the project root via azd's native template adoption, and the services it already declares are not re-derived or duplicated under `src/<agent>/`. Pointing `-m` at an agent manifest (top-level `template:`) keeps the existing generate-from-manifest behavior, and adoption falls back to that path when a sample ships no `azure.yaml`. Adoption requires an empty target directory; adopting into a directory that already has a project `azure.yaml` is not yet supported.
- [[#8818]](https://github.com/Azure/azure-dev/pull/8818) `azd ai agent init` now writes each Foundry resource as its own `azure.yaml` service entry instead of bundling everything into the agent service. Model deployments become a single `azure.ai.project` service, each connection becomes an `azure.ai.connection` service, and each toolbox becomes an `azure.ai.toolbox` service, all wired to the agent through `uses:`. The `azure.ai.project`, `azure.ai.connection`, and `azure.ai.toolbox` hosts are now owned by their sibling extensions (`azure.ai.projects`, `azure.ai.connections`, `azure.ai.toolboxes`) as real deploy-time service targets. The agents extension no longer registers them as no-op hosts, and toolboxes are reconciled at `azd deploy` by the `azure.ai.toolbox` target rather than created during `azd provision`.
- [[#8780]](https://github.com/Azure/azure-dev/pull/8780) Add a `--call-id` flag to `azd ai agent invoke` that sends the `x-agent-foundry-call-id` header on `--local` invocations only. It is ignored for remote Foundry requests.
- [[#8879]](https://github.com/Azure/azure-dev/pull/8879) `azd deploy`/`azd up` now warn when two or more `azure.ai.agent` services resolve to the same Foundry agent `name`. Foundry identifies an agent by its name, so such services deploy to the same agent and overwrite each other; the warning names the colliding services so each can be given a unique name in `azure.yaml`. Deploy still proceeds.
- [[#8881]](https://github.com/Azure/azure-dev/pull/8881) Add `azd ai agent sessions stop <session-id>` to stop a running hosted agent session while preserving its persistent filesystem. Unlike `sessions delete`, the session is retained and can be resumed by a later invocation. Stopping an already-stopped session is idempotent and succeeds without error. Thanks @harsheet-shah for the contribution!
- [[#8869]](https://github.com/Azure/azure-dev/pull/8869) Add option to select an existing deployment when choosing a different model during `azd ai agent init`.
- [[#8874]](https://github.com/Azure/azure-dev/pull/8874) Increase default model deployment capacity from 10 to 50 for agents.
- [[#8754]](https://github.com/Azure/azure-dev/pull/8754) Add PR gate tests for the `azd ai agent` extension. Thanks @v1212 for the contribution!
- [[#8758]](https://github.com/Azure/azure-dev/pull/8758) Add live golden-path (Tier 2) pipeline for the `azd ai agent` extension. Thanks @v1212 for the contribution!
- [[#8788]](https://github.com/Azure/azure-dev/pull/8788) Migrate predeploy/postdeploy to service-level event handlers in the agents extension.
- [[#8890]](https://github.com/Azure/azure-dev/pull/8890) Bump `requiredAzdVersion` to `>=1.27.0` for all AI/Foundry extensions.

### Breaking Changes

- [[#8868]](https://github.com/Azure/azure-dev/pull/8868) `azd ai agent init` now defaults to **code deploy** (ZIP upload) instead of container deploy for Python and .NET projects. This affects `--no-prompt` runs without an explicit `--deploy-mode` flag. To preserve the previous behavior, pass `--deploy-mode container`. When code deploy is selected from a GitHub sample template, any Dockerfile and .dockerignore from the sample are removed from the scaffolded directory.
- [[#8780]](https://github.com/Azure/azure-dev/pull/8780) Replace the per-command Foundry isolation-key flags (`--user-isolation-key`, `--chat-isolation-key`, and the session-ownership `--isolation-key`) with a single `--user-identity` flag with no backward-compatible flag retention.

### Bugs Fixed

- [[#8883]](https://github.com/Azure/azure-dev/pull/8883) `azd up` now prompts for an Azure subscription and location when `AZURE_SUBSCRIPTION_ID` or `AZURE_LOCATION` is not set, matching core `azd up`, instead of failing. Under `--no-prompt` it still returns an actionable `azd env set ...` error. Fixes [[#8859]](https://github.com/Azure/azure-dev/issues/8859).
- [[#8880]](https://github.com/Azure/azure-dev/pull/8880) Fix ACR not created/linked for hosted container agents on existing Foundry projects. `azd provision` now connects to an existing Foundry project when the `azure.ai.project` service sets `endpoint:` (bring-your-own) instead of failing with a brownfield error, and `azd down` leaves a bring-your-own project in place because azd did not create it.
- [[#8769]](https://github.com/Azure/azure-dev/pull/8769) Reprompt on invalid agent name instead of crashing.
- [[#8770]](https://github.com/Azure/azure-dev/pull/8770) Avoid double agent service prompt in `azd ai agent invoke`.
- [[#8771]](https://github.com/Azure/azure-dev/pull/8771) Allow `--local` with a named agent in `azd ai agent invoke`.
- [[#8787]](https://github.com/Azure/azure-dev/pull/8787) Use venv for pip fallback in `azd ai agent run`.
- [[#8829]](https://github.com/Azure/azure-dev/pull/8829) Update container deploy schema to use `protocol_versions` and `container_configuration`. Thanks @v1212 for the contribution!
- [[#8867]](https://github.com/Azure/azure-dev/pull/8867) Fix placeholder warning to reference `azure.yaml` instead of `agent.yaml`.
- [[#8876]](https://github.com/Azure/azure-dev/pull/8876) Fix `azd ai agent init --image` azure.yaml output. Thanks @m5i-work for the contribution!
- [[#8789]](https://github.com/Azure/azure-dev/pull/8789) Update agent models to match TypeSpec definition.

## 0.1.41-preview (2026-06-19)

- [[#8731]](https://github.com/Azure/azure-dev/pull/8731) Improve the post-deploy `Next:` guidance with a stacked layout that puts each command on its own line above its description, adds a blank line between suggestions, and highlights `azd` commands. The new layout applies across deploy, `azd ai agent show`, `init`, and `doctor`. Thanks @therealjohn for the contribution!
- [[#8645]](https://github.com/Azure/azure-dev/pull/8645) Detect VNET-injected Foundry accounts during `azd ai agent init` and skip remote builds up front so hosted container agents use local builds without a failing remote-build attempt first. Thanks @m5i-work for the contribution!
- [[#8714]](https://github.com/Azure/azure-dev/pull/8714) Show a tracing disclaimer when `azd ai agent init` connects or adds an Application Insights connection. Thanks @therealjohn for the contribution!
- [[#8685]](https://github.com/Azure/azure-dev/pull/8685) Default `azd ai agent run` local Python virtual environments to Python >= 3.13 so local runs match the minimum supported Foundry runtime. Thanks @therealjohn for the contribution!
- [[#8732]](https://github.com/Azure/azure-dev/pull/8732) Update the Application Insights tracing disclaimer shown during `azd ai agent init` with revised wording and a `Learn more` link. Thanks @therealjohn for the contribution!

## 0.1.40-preview (2026-06-15)

- [[#8641]](https://github.com/Azure/azure-dev/pull/8641) Fix optimize/eval handling for array-valued mutations, resolve `dataset.local_uri` relative to the agent project, and align optimize test schema data with the current API format. Thanks @Zyysurely for the contribution!
- [[#8625]](https://github.com/Azure/azure-dev/pull/8625) Show server response timing after successful `azd ai agent invoke` calls, including total latency and time to first byte, while keeping failure and `--output raw` flows unchanged. Thanks @VBhadauria for the contribution!
- [[#8624]](https://github.com/Azure/azure-dev/pull/8624) Add Python bundled-mode guidance after `azd ai agent init` and block `azd deploy` with clear remediation when bundled dependencies were not installed into the source directory. Thanks @v1212 for the contribution!

## 0.1.39-preview (2026-06-11)

- [[#8602]](https://github.com/Azure/azure-dev/pull/8602) Support Foundry `${{...}}` server-side expressions during `azd ai agent` environment-variable expansion, so values that mix azd `${VAR}` references with Foundry `${{...}}` expressions (and `${VAR:-default}` forms) resolve correctly instead of being dropped. Thanks @huimiu for the contribution!
- [[#8589]](https://github.com/Azure/azure-dev/pull/8589) Align `azd ai agent optimize` and `eval` with the V2Preview optimization API, including snake_case payloads, the new `agent_optimization_jobs` endpoints, a required `optimization_model`, and a Strategy column in the results table. Thanks @Zyysurely for the contribution!
- [[#8576]](https://github.com/Azure/azure-dev/pull/8576) Add `azd ai agent code download` command to download (and optionally extract) the deployed source code of a code-based hosted agent, with SHA-256 verification. Thanks @v1212 for the contribution!
- [[#8574]](https://github.com/Azure/azure-dev/pull/8574) Add `azd ai agent endpoint show` command to display the live endpoint configuration, and warn before `azd ai agent endpoint update` applies a breaking authorization isolation-key-source change. Thanks @v1212 for the contribution!
- [[#8566]](https://github.com/Azure/azure-dev/pull/8566) Rename `azd ai agent eval init` to `eval generate` (keeping a hidden, deprecated `init`), honor the `-e`/`--environment` flag in optimize/eval flows, resolve relative `--dataset` paths against the current directory, and reconcile stale agent versions from the environment. Thanks @Zyysurely for the contribution!
- [[#8519]](https://github.com/Azure/azure-dev/pull/8519) Add `azd ai agent delete` command to delete a hosted agent and all of its versions, with `--force` to terminate active sessions. Thanks @v1212 for the contribution!

## 0.1.38-preview (2026-06-05)

- [[#8532]](https://github.com/Azure/azure-dev/pull/8532) Fix Agent Inspector auto-launch for slow-starting local agents by waiting for the local agent port instead of timing out after 30 seconds. Thanks @anchenyi for the contribution!
- [[#8529]](https://github.com/Azure/azure-dev/pull/8529) Update `azd ai agent init` to write a salted `AZURE_RESOURCE_GROUP` value to `.env`, reducing resource group collisions when recreating projects. Thanks @banrahan for the contribution!
- [[#8521]](https://github.com/Azure/azure-dev/pull/8521) Add generic `policies` (`rai_policy`) support to the `agent.yaml` manifest for hosted agents so users can attach governance policies via `rai_policy_name`. Thanks @amitbhave10 for the contribution!
- [[#8522]](https://github.com/Azure/azure-dev/pull/8522) Fix `azd ai agent init` from a manifest in the current directory when the target project is created in a subdirectory. Thanks @v1212 for the contribution!

## 0.1.37-preview (2026-06-01)

- [[#8512]](https://github.com/Azure/azure-dev/pull/8512) Normalize connection auth `AgenticIdentity` values to the ARM-required `AgenticIdentityToken`.
- [[#8508]](https://github.com/Azure/azure-dev/pull/8508) Add `Foundry-Features: HostedAgents=V1Preview` on all Foundry data-plane endpoint requests to prevent preview feature failures.
- [[#8488]](https://github.com/Azure/azure-dev/pull/8488) Add a resource-token salt to avoid 404 failures when recreating AI agents.

## 0.1.36-preview (2026-05-30)

- [[#8500]](https://github.com/Azure/azure-dev/pull/8500) workaround: api-version query param for non-conformant openai agent endpoints

## 0.1.35-preview (2026-05-29)

- [[#8406]](https://github.com/Azure/azure-dev/pull/8406) Add `--output raw` (`-o raw`) flag to `azd ai agent invoke` to dump the unmodified server response (status line, headers, and body verbatim) to stdout. Useful for debugging server behavior and inspecting response headers such as the agent version. Friendly summary lines (`Session:`, `Invocation:`, `Trace ID:`, `Version:`) are suppressed in raw mode.
- [[#8403]](https://github.com/Azure/azure-dev/pull/8403) Add `invocations_ws` as a displayable agent protocol. `azd deploy` now registers the callable Foundry data-plane WebSocket URL (`wss://<account>.services.ai.azure.com/api/projects/agents/endpoint/protocols/invocations_ws?api-version=v1&project_name=<project>&agent_name=<agent>`) as `AGENT_{KEY}_INVOCATIONS_WS_ENDPOINT`, and `azd ai agent show` displays it as `Endpoint (invocations_ws)`. Previously, `invocations_ws` agents fell back to the legacy resource URL labeled `Endpoint (Agent)`.
- [[#8358]](https://github.com/Azure/azure-dev/pull/8358) Add full OAuth2 fields and connector-name support to `azd ai agent connection create`, including validation for managed-connector vs bring-your-own OAuth2 flows.
- [[#8326]](https://github.com/Azure/azure-dev/pull/8326) Reuse an existing local `agent.yaml` definition during `azd ai agent init` instead of prompting to overwrite or failing in no-prompt mode.
- [[#8364]](https://github.com/Azure/azure-dev/pull/8364) Adapt `azd ai agent optimize` to the updated optimize service request/response contract, including the new `optimizationConfig` model.
- [[#8389]](https://github.com/Azure/azure-dev/pull/8389) Honor `.azdignore` during `azd ai agent init` template and manifest materialization flows.
- [[#8378]](https://github.com/Azure/azure-dev/pull/8378) Update environment-variable parsing behavior for `azd ai agent run`.
- [[#8394]](https://github.com/Azure/azure-dev/pull/8394) Remove the broken `doctor` model-deployments check that produced false failures from manifest alias names.
- [[#8393]](https://github.com/Azure/azure-dev/pull/8393) Improve post-init/run `Next:` guidance for toolbox scenarios and standardize local invoke examples.
- [[#8398]](https://github.com/Azure/azure-dev/pull/8398) Add `germanywestcentral` and `canadaeast` to the hosted-agent supported regions list.
- [[#8400]](https://github.com/Azure/azure-dev/pull/8400) Show connection metadata key/value pairs in `azd ai agent connection show` table output.
- [[#8405]](https://github.com/Azure/azure-dev/pull/8405) Fix YAML tags to use snake_case (`agent_endpoint`, `agent_card`) so agent endpoint settings are correctly loaded from `agent.yaml`.
- [[#8363]](https://github.com/Azure/azure-dev/pull/8363) Allow `azd ai agent init --no-prompt` to defer Azure/model setup when Azure context variables are missing.
- [[#8347]](https://github.com/Azure/azure-dev/pull/8347) Use `api-version=v1` for hosted agent endpoint protocol and session requests.
- [[#8422]](https://github.com/Azure/azure-dev/pull/8422) Remove the hardcoded code-deploy region allowlist and use dynamic hosted-agent region resolution.
- [[#8392]](https://github.com/Azure/azure-dev/pull/8392) Improve optimize config YAML deserialization/UX and align generation endpoint calls to `2025-11-15-preview`.
- [[#8426]](https://github.com/Azure/azure-dev/pull/8426) Add opinionated defaults for manifest-driven `azd ai agent init -m` to reduce interactive prompts.
- [[#8441]](https://github.com/Azure/azure-dev/pull/8441) Fix hosted-agent deploy failures on `CreateAgentVersion` by including `Foundry-Features: HostedAgents=V1Preview` on v1 requests.
- [[#8479]](https://github.com/Azure/azure-dev/pull/8479) Add dependency on `azure.ai.inspector`, for handling with `azd ai agent run`.
- [[#8482]](https://github.com/Azure/azure-dev/pull/8482) Improve ACR publish error handling by classifying permission-denied failures and surfacing actionable RBAC/code-deploy remediation guidance.

### Breaking Changes
- [[#8210]](https://github.com/Azure/azure-dev/pull/8210) Update sample-based init flow to create a new folder during `azd ai agent init`.
- [[#8357]](https://github.com/Azure/azure-dev/pull/8357) Migrate connection CRUD commands from `azure.ai.agents` to the `azure.ai.connections` extension.

## 0.1.34-preview (2026-05-22)

- [[#8264]](https://github.com/Azure/azure-dev/pull/8264) Launch Agent Inspector automatically on `azd ai agent run`. Use `--no-inspector` to opt out. Requires the `azure.ai.inspector` extension.
- [[#8327]](https://github.com/Azure/azure-dev/pull/8327) Add `RemoteA2A` connection kind and expand auth type support for `azd ai agent connection create`, including OAuth2, user Entra token, project managed identity, and agentic identity token auth types.
- [[#8321]](https://github.com/Azure/azure-dev/pull/8321) Introduce `AZURE_AI_DEPLOYMENTS_LOCATION` to decouple model/project deployment location from resource group location (`AZURE_LOCATION`), fixing provisioning failures when a Foundry project and resource group are in different regions.
- [[#8324]](https://github.com/Azure/azure-dev/pull/8324) Add `--deploy-mode`, `--runtime`, `--entry-point`, and `--dep-resolution` flags to `azd ai agent init` for non-interactive code deploy support in CI/CD pipelines.
- [[#8198]](https://github.com/Azure/azure-dev/pull/8198) Add `azd ai agent doctor` diagnostics command with checks for project setup, environment variables, authentication, Foundry reachability, hosted agent status, and more. Add context-aware `Next:` guidance across init, run, invoke, show, and deploy-hook flows.
- [[#8306]](https://github.com/Azure/azure-dev/pull/8306) Add `azd ai agent eval` and `azd ai agent optimize` command families for evaluating and iteratively optimizing AI agents.
- [[#8332]](https://github.com/Azure/azure-dev/pull/8332) Add handling to update agent endpoint details when an agent is redeployed to a new endpoint.

## 0.1.33-preview (2026-05-21)

- [[#8299]](https://github.com/Azure/azure-dev/pull/8299) Don't fail `azd ai agent init` when Foundry agent existence checks error.
- [[#8298]](https://github.com/Azure/azure-dev/pull/8298) Use the selected agent name for service entry when resolving Foundry name conflicts.
- [[#8292]](https://github.com/Azure/azure-dev/pull/8292) Decouple Foundry project selection from model configuration during agent init.
- [[#8271]](https://github.com/Azure/azure-dev/pull/8271) Remove the 0.25 CPU option for hosted agents.
- [[#8266]](https://github.com/Azure/azure-dev/pull/8266) Add `azd ai agent sample list` and improve non-interactive `azd ai agent init`.
- [[#8245]](https://github.com/Azure/azure-dev/pull/8245) Rename the project endpoint environment variable to `FOUNDRY_PROJECT_ENDPOINT`.
- [[#8242]](https://github.com/Azure/azure-dev/pull/8242) Skip ACR creation and startup command configuration for code deploy.
- [[#8233]](https://github.com/Azure/azure-dev/pull/8233) Allow `azd ai agent invoke` to target specific versions.
- [[#8206]](https://github.com/Azure/azure-dev/pull/8206) Support header isolation keys for agent sessions.
- [[#8189]](https://github.com/Azure/azure-dev/pull/8189) Add naming safeguards for `azd ai agent init`.
- [[#7898]](https://github.com/Azure/azure-dev/pull/7898) Remove the hardcoded invoke suggestion from `azd ai agent run`.

### Breaking Changes

- [[#8293]](https://github.com/Azure/azure-dev/pull/8293) Remove deprecated runtimes (Python 3.11/3.12 and .NET 8/9) from `azd ai agent init`.
- [[#8243]](https://github.com/Azure/azure-dev/pull/8243) Migrate project endpoint commands to the new scaffold.

## 0.1.32-preview (2026-05-18)

- [[#8223]](https://github.com/Azure/azure-dev/pull/8223) Add `.agentignore` support for controlling which files are excluded from agent code-deploy ZIP packaging. Uses `.gitignore` syntax with sensible defaults generated during `azd ai agent init`.
- [[#8222]](https://github.com/Azure/azure-dev/pull/8222) Add post-init validation to check .NET runtime compatibility with project TargetFramework and show guidance when mismatched.
- [[#7865]](https://github.com/Azure/azure-dev/pull/7865) Improve `azd ai agent invoke` trace ID handling for consistent responses, including deduping comma-folded request IDs.
- [[#8184]](https://github.com/Azure/azure-dev/pull/8184) Default `azd ai agent show` output to table format.
- [[#8182]](https://github.com/Azure/azure-dev/pull/8182) Add guidance for deploying with private ACR images.
- [[#8181]](https://github.com/Azure/azure-dev/pull/8181) Increase timeout used by `azd ai agent invoke`.
- [[#8175]](https://github.com/Azure/azure-dev/pull/8175) Wait for deployed agents to reach active state before command completion.
- [[#8174]](https://github.com/Azure/azure-dev/pull/8174) Add `azd ai agent connection` commands and credential resolution for local run. (will be removed in a future release)
- [[#8162]](https://github.com/Azure/azure-dev/pull/8162) Add `azd ai agent project` commands for managing Foundry project endpoints. (will be removed in a future release)
- [[#8161]](https://github.com/Azure/azure-dev/pull/8161) Add .NET code deploy support (dotnet 8/9/10 runtimes).
- [[#8146]](https://github.com/Azure/azure-dev/pull/8146) Support code deploy zip uploads.
- [[#8104]](https://github.com/Azure/azure-dev/pull/8104) Add support for deploying from an existing ACR image.
- [[#8075]](https://github.com/Azure/azure-dev/pull/8075) Show featured templates first during `azd ai agent init`.

## 0.1.31-preview (2026-05-07)

- [[#8096]](https://github.com/Azure/azure-dev/pull/8096) Fix for bug introduced with #8034. Properly storing root agent endpoint since sessions are independent of protocol.
- [[#8038]](https://github.com/Azure/azure-dev/pull/8038) Fix MCP tool field mapping to correctly include `url` values from tool definitions. Fixes mapping for toolbox tools to connections.

## 0.1.30-preview (2026-05-06)

- [[#8028]](https://github.com/Azure/azure-dev/pull/8028) Add `--agent-endpoint` flag to `azd ai agent invoke` to invoke a deployed agent from any directory without needing an azd project or environment. Thanks @antriksh30 for the contribution!
- [[#7999]](https://github.com/Azure/azure-dev/pull/7999) Add A2A endpoint protocol and agent card metadata support for agent deployments. Thanks @adamra-msft for the contribution!
- [[#8027]](https://github.com/Azure/azure-dev/pull/8027) Add playground URL and per-protocol endpoint URLs to `azd ai agent show` output. Thanks @Nathandrake229 for the contribution!
- [[#8034]](https://github.com/Azure/azure-dev/pull/8034) Move session and conversation ID tracking to the global azd user config, enabling session state to persist across directories and project relocations.
- [[#7947]](https://github.com/Azure/azure-dev/pull/7947) Fix `flag redefined` panics on `azd ai agent show`, `azd ai agent files list`, and `azd ai agent files stat` caused by duplicate `--output`/`-o` flag registration.
- [[#7968]](https://github.com/Azure/azure-dev/pull/7968) Fix agent templates URL used by `azd ai agent init` to use the correct `aka.ms` redirect after release.

### Breaking Changes

- [[#8040]](https://github.com/Azure/azure-dev/pull/8040) Remove prompt agent and `azureml://` registry support; prompt agent configurations in `azure.yaml` are no longer recognized.

## 0.1.29-preview (2026-04-30)

- [[#7984]](https://github.com/Azure/azure-dev/pull/7984) Fix `postdeployHandler` to skip post-deploy processing when the project has no hosted agent services, preventing errors on non-agent projects.
- [[#7974]](https://github.com/Azure/azure-dev/pull/7974) Update post-deploy output to display the agent invocation endpoint URL.
- [[#7966]](https://github.com/Azure/azure-dev/pull/7966) Update the `aka.ms` redirect URL used to fetch the agent templates list.
- [[#7921]](https://github.com/Azure/azure-dev/pull/7921) Update `azd ai agent init` to load agent templates from the unified awesome-azd `templates.json` manifest, filtered by the `extension.ai.agent` type discriminator.

## 0.1.28-preview (2026-04-28)

- [[#7930]](https://github.com/Azure/azure-dev/pull/7930) Fetch the hosted-agent supported regions list at runtime from a remote JSON manifest with an embedded fallback, replacing the hardcoded list; region data can now be updated without cutting an extension release.

## 0.1.27-preview (2026-04-22)

- [[#7880]](https://github.com/Azure/azure-dev/pull/7880) Remove ACR endpoint pre-check from the package step; packaging no longer fails early when `AZURE_CONTAINER_REGISTRY_ENDPOINT` is absent, allowing provisioning to create the registry first for new projects.

## 0.1.26-preview (2026-04-21)

- [[#7843]](https://github.com/Azure/azure-dev/pull/7843) When `azd ai agent init` uses an existing Azure AI project, set `USE_EXISTING_AI_PROJECT=true` so downstream Bicep provisioning skips creating the project, roles, and connections again.
- [[#7835]](https://github.com/Azure/azure-dev/pull/7835) Add validation for missing container registry endpoints in agent service configuration.
- [[#7790]](https://github.com/Azure/azure-dev/pull/7790) Improve `azd ai agent monitor` output: render each SSE log event as a single compact, color-coded line (`HH:MM:SS  <stream>  <message>`) with session-metadata events rendered as `session <state> (v<version>, last accessed: ...)`. Add `--utc` flag to display timestamps in UTC instead of local time, and `--raw` flag to preserve the previous raw SSE output.
- [[#7834]](https://github.com/Azure/azure-dev/pull/7834) Implement flexible timestamp parsing for `modified_time` values in JSON responses.

### Breaking Changes

- [[#7764]](https://github.com/Azure/azure-dev/pull/7764) Remove `container.scale` configuration (`minReplicas`/`maxReplicas`) from `azure.yaml`. Scale settings are no longer supported for hosted agents. Remove any `container.scale` section from your service configuration.

## 0.1.25-preview (2026-04-20)

- [[#7811]](https://github.com/Azure/azure-dev/pull/7811) Fix agent deployment RBAC checks to show warnings instead of blocking deployment when role assignment issues are encountered.
- [[#7808]](https://github.com/Azure/azure-dev/pull/7808) Add Azure AI Project Manager and Azure AI Account Owner as accepted roles in the developer RBAC role-assignment-write preflight check.
- [[#7807]](https://github.com/Azure/azure-dev/pull/7807) Fix `azd ai agent invoke` to use the correct endpoint for creating conversations.

## 0.1.24-preview (2026-04-17)

- [[#7765]](https://github.com/Azure/azure-dev/pull/7765) Improve invalid manifest error messaging to guide users to check for a required `template` field.
- [[#7763]](https://github.com/Azure/azure-dev/pull/7763) Fix developer RBAC pre-flight gaps by auto-assigning Azure AI User when missing, adding an explicit role-assignment-write check, and handling ABAC-enabled ACR registries.
- [[#7747]](https://github.com/Azure/azure-dev/pull/7747) Update agent identity RBAC resolution to read identity information from the agent version instead of relying on graph lookup.

### Breaking Changes

- [[#7741]](https://github.com/Azure/azure-dev/pull/7741) Remove `-e` shorthand for `--environment` on `azd ai agent init`; use `--environment` instead to avoid collision with azd global `-e/--environment`.

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

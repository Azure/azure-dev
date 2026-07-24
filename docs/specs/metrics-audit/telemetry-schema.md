# Telemetry Schema Reference

This document is the authoritative reference for all telemetry events, fields, classifications,
and data pipeline details in the Azure Developer CLI (`azd`).

## Events

Events are defined in `cli/azd/internal/tracing/events/events.go`. Each event is emitted as an
OpenTelemetry span name or event name.

| Constant | Value | Description |
|----------|-------|-------------|
| `CommandEventPrefix` | `cmd.` | Prefix for all command events (via `GetCommandEventName`) |
| `VsRpcEventPrefix` | `vsrpc.` | Prefix for VS Code JSON-RPC events |
| `McpEventPrefix` | `mcp.` | Prefix for MCP tool invocation events |
| `PackBuildEvent` | `tools.pack.build` | Cloud Native Buildpacks build event |
| `AgentTroubleshootEvent` | `agent.troubleshoot` | Agent troubleshooting event |
| `ExtensionRunEvent` | `ext.run` | Extension execution event |
| `ExtensionInstallEvent` | `ext.install` | Extension install/upgrade event |
| `ExtensionUpgradeEvent` | `ext.upgrade` | Single extension upgrade attempt |
| `ExtensionPromoteEvent` | `ext.promote` | Extension registry promotion (e.g., dev → main) |
| `CopilotInitializeEvent` | `copilot.initialize` | Copilot initialization event |
| `CopilotSessionEvent` | `copilot.session` | Copilot session lifecycle event |
| `ProvisionValidationEvent` | `validation.provision` | Local provision validation outcome |
| `HooksExecEvent` | `hooks.exec` | Lifecycle hook execution |
| `AksPostprovisionSkipEvent` | `aks.postprovision.skip` | AKS postprovision hook skipped (cluster not yet available) |
| `ArmDeploySubscriptionEvent` | `arm.deploy.subscription` | ARM subscription-scope deploy |
| `ArmDeployResourceGroupEvent` | `arm.deploy.resourcegroup` | ARM resource-group-scope deploy |
| `ArmStackDeploySubscriptionEvent` | `arm.stack.deploy.subscription` | Deployment stack at subscription scope |
| `ArmStackDeployResourceGroupEvent` | `arm.stack.deploy.resourcegroup` | Deployment stack at resource-group scope |
| `ArmWhatIfSubscriptionEvent` | `arm.whatif.subscription` | What-if preview at subscription scope |
| `ArmWhatIfResourceGroupEvent` | `arm.whatif.resourcegroup` | What-if preview at resource-group scope |
| `ArmValidateSubscriptionEvent` | `arm.validate.subscription` | Template validation at subscription scope |
| `ArmValidateResourceGroupEvent` | `arm.validate.resourcegroup` | Template validation at resource-group scope |
| `DeployAppServiceZipEvent` | `deploy.appservice.zip` | App Service zip-deploy attempt |
| `ContainerCredentialsEvent` | `container.credentials` | Container registry credential lookup |
| `ContainerPublishEvent` | `container.publish` | Container image publish (push) |
| `ContainerRemoteBuildEvent` | `container.remotebuild` | Azure-side remote container build |
| `ExeGraphRunEvent` | `exegraph.run` | Root span for executing an entire graph |
| `ExeGraphStepEvent` | `exegraph.step` | Single step execution within the graph |

## Fields

Fields are defined in `cli/azd/internal/tracing/fields/fields.go`. Each field has a classification
and purpose that governs how it may be stored, queried, and retained.

### Application-Level (Resource Attributes)

These are set once at process startup via `resource.New()` and attached to every span.

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Service name | `service.name` | — | — | Always `"azd"` |
| Service version | `service.version` | — | — | Build version string |
| OS type | `os.type` | — | — | e.g. `linux`, `windows`, `darwin` |
| OS version | `os.version` | SystemMetadata | PerformanceAndHealth | Kernel / build version |
| Host architecture | `host.arch` | SystemMetadata | PerformanceAndHealth | e.g. `amd64`, `arm64` |
| Runtime version | `process.runtime.version` | SystemMetadata | PerformanceAndHealth | Go version |
| Machine ID | `machine.id` | EndUserPseudonymizedInformation | BusinessInsight | MAC address hash |
| Dev Device ID | `machine.devdeviceid` | EndUserPseudonymizedInformation | BusinessInsight | SQM User ID |
| Execution environment | `execution.environment` | SystemMetadata | BusinessInsight | Format: `<environment>[;<modifier>...]`. Base values: `Desktop`, `Visual Studio`, `Visual Studio Code`, `VS Code Azure GitHub Copilot`, `Azure CloudShell`, `Claude Code`, `GitHub Copilot CLI`, `Gemini`, `OpenCode`, `UnknownCI`, `Azure Pipelines`, `GitHub Actions`, `AppVeyor`, `Bamboo`, `BitBucket Pipelines`, `Travis CI`, `Circle CI`, `GitLab CI`, `Jenkins`, `AWS CodeBuild`, `Google Cloud Build`, `TeamCity`, `JetBrains Space`, `GitHub Codespaces`. Modifiers: `Azure App Spaces Portal`, `Microsoft Foundry Canvas`, `Microsoft Foundry Skill` |
| Installer | `service.installer` | SystemMetadata | FeatureInsight | How azd was installed |

### Experimentation

| Field | OTel Key | Classification | Purpose |
|-------|----------|----------------|---------|
| Assignment context | `exp.assignmentContext` | SystemMetadata | FeatureInsight |

### Identity and Account Context

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Object ID | `user_AuthenticatedId` | — | — | From Application Insights contracts |
| Tenant ID | `ad.tenant.id` | SystemMetadata | BusinessInsight | Entra ID tenant |
| Account type | `ad.account.type` | SystemMetadata | BusinessInsight | `"User"` or `"Service Principal"` |
| Subscription ID | `ad.subscription.id` | OrganizationalIdentifiableInformation | PerformanceAndHealth | Azure subscription |

### Project Context (azure.yaml)

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Template ID | `project.template.id` | SystemMetadata | FeatureInsight | **Hashed** |
| Template version | `project.template.version` | SystemMetadata | FeatureInsight | **Hashed** |
| Project name | `project.name` | SystemMetadata | FeatureInsight | **Hashed** |
| Service hosts | `project.service.hosts` | SystemMetadata | FeatureInsight | List of host types |
| Service targets | `project.service.targets` | SystemMetadata | FeatureInsight | List of deploy targets |
| Service languages | `project.service.languages` | SystemMetadata | FeatureInsight | List of languages |
| Service language | `project.service.language` | SystemMetadata | PerformanceAndHealth | Single service language |
| Platform type | `platform.type` | SystemMetadata | FeatureInsight | e.g. `aca`, `aks` |

### Config and Environment

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Feature flags | `config.features` | SystemMetadata | FeatureInsight | Active feature flags |
| Environment name | `env.name` | SystemMetadata | FeatureInsight | **Hashed** |

### Command Entry-Point

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Flags | `cmd.flags` | SystemMetadata | FeatureInsight | Which flags were passed |
| Argument count | `cmd.args.count` | SystemMetadata | FeatureInsight | **Measurement** |
| Entry point | `cmd.entry` | SystemMetadata | FeatureInsight | How the command was invoked |

### Error Attributes

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Error category | `error.category` | SystemMetadata | PerformanceAndHealth | |
| Error code | `error.code` | SystemMetadata | PerformanceAndHealth | |
| Error type | `error.type` | SystemMetadata | PerformanceAndHealth | ResultCode or Go type for the classified error |
| Error chain types | `error.chain.types` | SystemMetadata | PerformanceAndHealth | Wrapped Go error type chain, outermost first |

Error classification is handled by `MapError` in `internal/cmd/errors.go`, which categorizes
errors into: update errors, auth errors, service (Azure) errors, deployment errors, extension
errors, tool errors, sentinel errors, and network errors. Each receives an `error.code`,
`error.category`, and contextual attributes.

Generic-only error chains now use the catch-all ResultCode `internal.unclassified` instead of
the previous `internal.errors_errorString`. Use `error.chain.types` to inspect the concrete
wrapper types behind that bucket. The removed `error.inner` and `error.frame` attributes were
not emitted by azd spans.

### Service Attributes

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Service host | `service.host` | SystemMetadata | PerformanceAndHealth | |
| Service name | `service.name` | SystemMetadata | PerformanceAndHealth | |
| Status code | `service.statusCode` | SystemMetadata | PerformanceAndHealth | **Measurement** |
| Method | `service.method` | SystemMetadata | PerformanceAndHealth | |
| Error code | `service.errorCode` | SystemMetadata | PerformanceAndHealth | **Measurement**; ARM deployment errors encode JSON objects with `error.code` and `error.arm.frame_index` |
| Correlation ID | `service.correlationId` | SystemMetadata | PerformanceAndHealth | |

### Tool Attributes

| Field | OTel Key | Classification | Purpose |
|-------|----------|----------------|---------|
| Tool name | `tool.name` | SystemMetadata | FeatureInsight |
| Tool exit code | `tool.exitCode` | SystemMetadata | PerformanceAndHealth |

### Performance

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Interaction time | `perf.interact_time` | SystemMetadata | PerformanceAndHealth | **Measurement** — time to first user prompt |
| Provision duration | `perf.provision_duration_ms` | SystemMetadata | PerformanceAndHealth | **Measurement** — wall-clock provisioning phase duration (ms) |
| Deploy duration | `perf.deploy_duration_ms` | SystemMetadata | PerformanceAndHealth | **Measurement** — wall-clock deploy phase duration (ms). Excludes package/publish (run concurrently with provision) |
| Total duration | `perf.total_duration_ms` | SystemMetadata | PerformanceAndHealth | **Measurement** — total wall-clock for the entire up-graph execution |

### Pack (Buildpacks)

| Field | OTel Key | Classification | Purpose |
|-------|----------|----------------|---------|
| Builder image | `pack.builder.image` | SystemMetadata | FeatureInsight |
| Builder tag | `pack.builder.tag` | SystemMetadata | FeatureInsight |

### MCP

| Field | OTel Key | Classification | Purpose |
|-------|----------|----------------|---------|
| Client name | `mcp.client.name` | SystemMetadata | FeatureInsight |
| Client version | `mcp.client.version` | SystemMetadata | FeatureInsight |

### Init

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Init method | `init.method` | SystemMetadata | FeatureInsight | template/app/project/environment/copilot |
| Detected databases | `appinit.detected.databases` | SystemMetadata | FeatureInsight | |
| Detected services | `appinit.detected.services` | SystemMetadata | FeatureInsight | |
| Confirmed databases | `appinit.confirmed.databases` | SystemMetadata | FeatureInsight | |
| Confirmed services | `appinit.confirmed.services` | SystemMetadata | FeatureInsight | |
| Modify add count | `appinit.modify_add.count` | SystemMetadata | FeatureInsight | **Measurement** |
| Modify remove count | `appinit.modify_remove.count` | SystemMetadata | FeatureInsight | **Measurement** |
| Last step | `appinit.lastStep` | SystemMetadata | FeatureInsight | |

### Remote Build

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Remote build count | `container.remoteBuild.count` | SystemMetadata | FeatureInsight | **Measurement** |

### JSON-RPC

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| RPC method | `rpc.method` | SystemMetadata | FeatureInsight | |
| Request ID | `rpc.jsonrpc.request_id` | SystemMetadata | PerformanceAndHealth | |
| Error code | `rpc.jsonrpc.error_code` | SystemMetadata | PerformanceAndHealth | **Measurement** |

### Agent

| Field | OTel Key | Classification | Purpose |
|-------|----------|----------------|---------|
| Fix attempts | `agent.fix.attempts` | SystemMetadata | PerformanceAndHealth |

### Extensions

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Extension ID | `extension.id` | SystemMetadata | FeatureInsight | |
| Extension version | `extension.version` | SystemMetadata | FeatureInsight | |
| Extension installed | `extension.installed` | SystemMetadata | FeatureInsight | List of installed extensions, each formatted `id@version` |
| Extension version from | `extension.version.from` | SystemMetadata | FeatureInsight | Installed version before an upgrade |
| Extension version to | `extension.version.to` | SystemMetadata | FeatureInsight | Target version after an upgrade |
| Extension source | `extension.source` | SystemMetadata | FeatureInsight | Registry source used for the upgrade |
| Extension source kind | `extension.source.kind` | SystemMetadata | FeatureInsight | Allowed values: `none`, `registered`, `location` |
| Extension source from | `extension.source.from` | SystemMetadata | FeatureInsight | Registry source before a promotion |
| Extension source to | `extension.source.to` | SystemMetadata | FeatureInsight | Registry source after a promotion |
| Upgrade duration | `extension.upgrade.duration_ms` | SystemMetadata | PerformanceAndHealth | **Measurement** — time in ms for one upgrade |
| Upgrade outcome | `extension.upgrade.outcome` | SystemMetadata | FeatureInsight | Upgrade result status |
| Dependency of | `extension.dependency_of` | SystemMetadata | FeatureInsight | Parent extension for a dependency upgrade |
| Dependency upgrade count | `extension.dependency_upgrade_count` | SystemMetadata | FeatureInsight | Recursive dependency upgrade count |

### Update

| Field | OTel Key | Classification | Purpose |
|-------|----------|----------------|---------|
| Update channel | `update.channel` | SystemMetadata | FeatureInsight |
| Install method | `update.installMethod` | SystemMetadata | FeatureInsight |
| From version | `update.fromVersion` | SystemMetadata | FeatureInsight |
| To version | `update.toVersion` | SystemMetadata | FeatureInsight |
| Update result | `update.result` | SystemMetadata | FeatureInsight |

### Copilot Session

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Session ID | `copilot.session.id` | SystemMetadata | FeatureInsight | |
| Is new session | `copilot.session.isNew` | SystemMetadata | FeatureInsight | |
| Message count | `copilot.session.messageCount` | SystemMetadata | FeatureInsight | **Measurement** |

### Copilot Init

| Field | OTel Key | Classification | Purpose |
|-------|----------|----------------|---------|
| Is first run | `copilot.init.isFirstRun` | SystemMetadata | FeatureInsight |
| Reasoning effort | `copilot.init.reasoningEffort` | SystemMetadata | FeatureInsight |
| Model | `copilot.init.model` | SystemMetadata | FeatureInsight |
| Consent scope | `copilot.init.consentScope` | SystemMetadata | FeatureInsight |

### Copilot Message

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Mode | `copilot.mode` | SystemMetadata | FeatureInsight | |
| Model | `copilot.message.model` | SystemMetadata | FeatureInsight | |
| Input tokens | `copilot.message.inputTokens` | SystemMetadata | PerformanceAndHealth | **Measurement** |
| Output tokens | `copilot.message.outputTokens` | SystemMetadata | PerformanceAndHealth | **Measurement** |
| Billing rate | `copilot.message.billingRate` | SystemMetadata | BusinessInsight | **Measurement** |
| Premium requests | `copilot.message.premiumRequests` | SystemMetadata | BusinessInsight | **Measurement** |
| Duration (ms) | `copilot.message.durationMs` | SystemMetadata | PerformanceAndHealth | **Measurement** |

### Copilot Consent

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Approved count | `copilot.consent.approvedCount` | SystemMetadata | FeatureInsight | **Measurement** |
| Denied count | `copilot.consent.deniedCount` | SystemMetadata | FeatureInsight | **Measurement** |

## Command-Specific Fields

The following fields are defined in `fields.go`.

| Field | OTel Key | Classification | Purpose | Values |
|-------|----------|----------------|---------|--------|
| Auth method | `auth.method` | SystemMetadata | FeatureInsight | `browser`, `device-code`, `service-principal-secret`, `service-principal-certificate`, `federated-github`, `federated-azure-pipelines`, `federated-oidc`, `managed-identity`, `external`, `oneauth`, `check-status` |
| Env count | `env.count` | SystemMetadata | FeatureInsight | **Measurement** — number of environments |
| Hooks name | `hooks.name` | SystemMetadata | FeatureInsight | Built-in hook name (raw) or SHA-256 hash for extension/custom hooks. Known values: `prebuild`, `postbuild`, `predeploy`, `postdeploy`, `predown`, `postdown`, `prepackage`, `postpackage`, `preprovision`, `postprovision`, `prepublish`, `postpublish`, `prerestore`, `postrestore`, `preup`, `postup` |
| Hooks type | `hooks.type` | SystemMetadata | FeatureInsight | `project`, `service`, `layer` |
| Hooks kind | `hooks.kind` | SystemMetadata | FeatureInsight | Executor used to run the hook. Values: `sh`, `pwsh`, `python`, `js`, `ts`, `dotnet` |
| Pipeline provider | `pipeline.provider` | SystemMetadata | FeatureInsight | Resolved provider display name after auto-detection: `GitHub`, `Azure DevOps` |
| Pipeline auth | `pipeline.auth` | SystemMetadata | FeatureInsight | Emitted only when `--auth-type` is set on `pipeline config`: `federated`, `client-credentials` |
| Infra provider | `infra.provider` | SystemMetadata | FeatureInsight | provision/up/down: sorted, de-duplicated string slice of resolved providers — `bicep`/`terraform`/`arm`/`pulumi` verbatim, `custom` for any other (extension) provider (raw name not emitted); multi-layer projects that combine providers record each distinct value (e.g. `["bicep","terraform"]`). `infra generate`/`synth`: the value read from azure.yaml's `infra.provider` directly as a single string (`bicep`/`terraform`/`arm`/`pulumi`, `auto` when unset, or `custom` for any other (extension) provider — raw name not emitted) |

### App Service Deploy

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Deploy attempt | `deploy.appservice.attempt` | SystemMetadata | PerformanceAndHealth | **Measurement** — retry attempt number for App Service zip deploy |
| Deploy linux | `deploy.appservice.linux` | SystemMetadata | FeatureInsight | Whether the deploy targets a Linux web app |

### Tool Management

Telemetry for active `azd tool` install / upgrade / check operations and the dormant
first-run field contract. Only built-in tool IDs (e.g. `az-cli`) and version strings are
captured — no file paths, no user-identifiable data, no raw error text.

#### Dormant tool first-run (reserved)

The first-run middleware is not currently registered, so these fields are not emitted. They
remain defined to support a possible future redesign without changing the telemetry contract.

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Skip reason | `tool.firstrun.skip_reason` | SystemMetadata | FeatureInsight | Why the first-run flow was bypassed. Values: `env_var`, `no_prompt`, `ci_cd`, `non_interactive`, `already_completed`, `config_error` |
| Opt-in | `tool.firstrun.opt_in` | SystemMetadata | FeatureInsight | Whether the user accepted the first-run prompt |
| Tools detected | `tool.firstrun.tools_detected` | SystemMetadata | FeatureInsight | **Measurement** — built-in tools already installed at first-run |
| Tools offered | `tool.firstrun.tools_offered` | SystemMetadata | FeatureInsight | **Measurement** — number of recommended tools offered |
| Tools selected | `tool.firstrun.tools_selected` | SystemMetadata | FeatureInsight | **Measurement** — number the user selected to install |
| Tools selected (names) | `tool.firstrun.tools_selected_names` | SystemMetadata | FeatureInsight | Comma-separated **built-in tool IDs** the user selected (e.g. `az-cli,vscode-bicep,github-copilot-cli`). Not user input — drawn from a fixed catalog |
| Tools deselected (names) | `tool.firstrun.tools_deselected_names` | SystemMetadata | FeatureInsight | Comma-separated built-in tool IDs the user deselected |
| Outcome | `tool.firstrun.outcome` | SystemMetadata | FeatureInsight | Terminal state. Values: `completed`, `declined`, `cancelled`, `detect_failed`, `install_failed`. Mutually exclusive with `skip_reason` |
| Install success count | `tool.firstrun.install_success_count` | SystemMetadata | FeatureInsight | **Measurement** — first-run-scoped counterpart of `tool.install.success_count` |
| Install failure count | `tool.firstrun.install_failure_count` | SystemMetadata | FeatureInsight | **Measurement** — first-run-scoped counterpart of `tool.install.failure_count` |
| Install failed IDs | `tool.firstrun.install_failed_ids` | SystemMetadata | FeatureInsight | Comma-separated built-in tool IDs whose first-run install failed |
| Install duration | `tool.firstrun.install_duration_ms` | SystemMetadata | FeatureInsight | **Measurement** — first-run-scoped install duration in ms |

#### Tool install / upgrade / check

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Tool ID | `tool.id` | SystemMetadata | FeatureInsight | Built-in tool ID for single-tool operations. Values drawn from a fixed catalog (e.g. `az-cli`, `vscode-bicep`, `github-copilot-cli`, `azure-mcp-server`) |
| Tool IDs | `tool.ids` | SystemMetadata | FeatureInsight | Comma-separated built-in tool IDs for batch operations |
| Dry run | `tool.dry_run` | SystemMetadata | FeatureInsight | Whether `--dry-run` was specified |
| Install strategy | `tool.install.strategy` | SystemMetadata | FeatureInsight | Install backend. Values come from `strategy.PackageManager` (e.g., `winget`, `brew`, `apt`, `npm`, `code`); `command` for command-based installs with no package manager; `manual` when the named package manager is unavailable on the platform |
| Install success | `tool.install.success` | SystemMetadata | FeatureInsight | Whether a single-target install/upgrade succeeded |
| Install success count | `tool.install.success_count` | SystemMetadata | FeatureInsight | **Measurement** — number of tools that succeeded in a batch |
| Install failure count | `tool.install.failure_count` | SystemMetadata | FeatureInsight | **Measurement** — number of tools that failed in a batch |
| Install failed IDs | `tool.install.failed_ids` | SystemMetadata | FeatureInsight | Comma-separated built-in tool IDs whose install/upgrade failed. Per-tool error messages are intentionally not captured |
| Install duration | `tool.install.duration_ms` | SystemMetadata | FeatureInsight | **Measurement** — total install/upgrade duration in ms |
| Upgrade from version | `tool.upgrade.from_version` | SystemMetadata | FeatureInsight | Prior version (single-target upgrades) |
| Upgrade to version | `tool.upgrade.to_version` | SystemMetadata | FeatureInsight | New version after a successful upgrade |
| Updates available | `tool.check.updates_available` | SystemMetadata | FeatureInsight | **Measurement** — number of installed tools with an available upgrade |

### Provision Validation

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Outcome | `validation.provision.outcome` | SystemMetadata | FeatureInsight | Values: `passed`, `warnings_accepted`, `canceled_by_errors`, `canceled_by_user`, `skipped`, `error` |
| Diagnostic IDs | `validation.provision.diagnostics` | SystemMetadata | FeatureInsight | List of diagnostic IDs emitted by provision validation checks (fixed code-defined enum, e.g. `role_assignment_missing`, `role_assignment_conditional`) |
| Rule IDs | `validation.provision.rules` | SystemMetadata | FeatureInsight | List of rule IDs that were executed (fixed code-defined enum, e.g. `role_assignment_permissions`) |
| Extension rule IDs | `validation.provision.extension_rules` | SystemMetadata | FeatureInsight | List of rule IDs from extension-provided validation checks (fixed code-defined enum) |
| Check type | `validation.provision.check_type` | SystemMetadata | FeatureInsight | Dispatch site: `arm-provision` (Bicep provider) or `provision` (provider-agnostic). Both share `validation.provision`; on Bicep provisions both fire, so consumers must group/filter by this to avoid double-counting |
| Warning count | `validation.provision.warning.count` | SystemMetadata | FeatureInsight | **Measurement** |
| Error count | `validation.provision.error.count` | SystemMetadata | FeatureInsight | **Measurement** |

### Provision

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Cancellation | `provision.cancellation` | SystemMetadata | FeatureInsight | How a Ctrl+C interrupt during `azd provision` / `azd up` was handled. Values: `none`, `leave_running`, `canceled`, `cancel_timed_out`, `cancel_timed_out_nested`, `cancel_raced_succeeded`, `cancel_raced_failed`, `cancel_raced_deleted`, `cancel_too_late`, `cancel_failed` |

### Execution Graph (Scheduler)

The execution graph powers the parallel `up` / `provision` / `deploy` engine.

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Step count | `exegraph.step.count` | SystemMetadata | PerformanceAndHealth | **Measurement** — total number of steps in the graph |
| Max concurrency | `exegraph.max_concurrency` | SystemMetadata | PerformanceAndHealth | Effective concurrency limit used for the run |
| Error policy | `exegraph.error_policy` | SystemMetadata | PerformanceAndHealth | `fail_fast` or `continue_on_error` |
| Step name | `exegraph.step.name` | SystemMetadata | PerformanceAndHealth | **Hashed** via `fields.StringHashed` — step names embed user-chosen service / layer names from `azure.yaml` (e.g., `deploy-<svc.Name>`, `<layer.Name>`) |
| Step deps | `exegraph.step.deps` | SystemMetadata | PerformanceAndHealth | **Hashed slice** via `fields.StringSliceHashed` — each entry is another step name that embeds user-chosen identifiers |
| Step tags | `exegraph.step.tags` | SystemMetadata | PerformanceAndHealth | Fixed internal vocabulary set by azd code (e.g., `provision`, `deploy`, `package`, `cmdhook`, `event`); emitted raw because it does not contain user input |
| Step timeout | `exegraph.step.timeout_s` | SystemMetadata | PerformanceAndHealth | **Measurement** — per-step timeout in seconds, when set |

### Multi-Layer Provision

Telemetry for the `infra.layers[]` parallel provisioning feature, emitted from `internal/cmd/provision_graph.go`.

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Layer count | `provision.layer.count` | SystemMetadata | PerformanceAndHealth | **Measurement** — total number of `infra.layers[]` declared in `azure.yaml` for the current run; 0 or 1 means single-layer (the legacy path) |
| Max parallel | `provision.layer.max_parallel` | SystemMetadata | PerformanceAndHealth | **Measurement** — largest number of layers scheduled in a single dependency level after static analysis (maximum *achievable* parallelism, distinct from the configured `exegraph.max_concurrency` cap) |
| Safe-fallback count | `provision.layer.safe_fallback_count` | SystemMetadata | PerformanceAndHealth | **Measurement** — layers that triggered the safe-by-default detector fallback (forced to depend on all earlier layers) |
| Explicit dependsOn count | `provision.layer.explicit_dependson_count` | SystemMetadata | PerformanceAndHealth | **Measurement** — layers that used the explicit `infra.layers[].dependsOn` schema |

## Data Classifications

Classifications are defined in `internal/telemetry/fields/fields.go` and control how data
is stored, retained, and who may access it.

| Classification | Description |
|----------------|-------------|
| `PublicPersonalData` | Data the user has made public (e.g. GitHub username) |
| `SystemMetadata` | Non-personal system/environment metadata |
| `CallstackOrException` | Stack traces and exception details |
| `CustomerContent` | User-created content (files, messages) — highest sensitivity |
| `EndUserPseudonymizedInformation` | Pseudonymized user identifiers (hashed MACs, device IDs) |
| `OrganizationalIdentifiableInformation` | Organization-level identifiers (subscription IDs, tenant IDs) |

## Purposes

Each field is tagged with one or more purposes that govern its permitted use.

| Purpose | Description |
|---------|-------------|
| `FeatureInsight` | Understanding feature adoption and usage patterns |
| `BusinessInsight` | Business metrics (active users, organizations, growth) |
| `PerformanceAndHealth` | Performance monitoring, error rates, reliability |

## Hashing

Sensitive values are hashed before emission using functions in `cli/azd/internal/tracing/fields/key.go`.

| Function | Behavior |
|----------|----------|
| `CaseInsensitiveHash(value string) string` | Lowercases, then SHA-256 hashes |
| `StringHashed(k AttributeKey, v string) attribute.KeyValue` | Creates an OTel attribute with a case-insensitive SHA-256 hash |
| `StringSliceHashed(k AttributeKey, v []string) attribute.KeyValue` | Hashes each element in a string slice independently |

Fields that are hashed:

- `project.template.id`
- `project.template.version`
- `project.name`
- `env.name`
- `exegraph.step.name` (embeds user-chosen service / layer names from `azure.yaml`)
- `exegraph.step.deps` (each entry is another step name and therefore embeds user-chosen identifiers)
- `hooks.name` is hashed **only when** the value comes from an extension or custom hook (the built-in hook names are emitted raw)
- `pack.builder.image`, `pack.builder.tag` are hashed when a user-defined image is used

## Data Pipeline

```
┌──────────────┐    ┌─────────────────────┐    ┌──────────────┐    ┌──────────────────┐
│  OTel Spans  │───▶│ App Insights        │───▶│  Disk Queue  │───▶│ Azure Monitor /  │
│  (in-process)│    │ Exporter (custom)   │    │  (~/.azd/)   │    │ Kusto            │
└──────────────┘    └─────────────────────┘    └──────────────┘    └──────────────────┘
                                                       │
                                                       ▼
                                               ┌──────────────┐
                                               │  telemetry   │
                                               │  upload cmd  │
                                               └──────────────┘
```

1. **Instrumentation**: Commands create OTel spans with attributes via `tracing.Start` and `SetUsageAttributes`.
2. **Export**: A custom Application Insights exporter converts spans to App Insights envelopes.
3. **Queue**: Envelopes are written to disk under `~/.azd/telemetry/`.
4. **Upload**: The `azd telemetry upload` command (run as a background process) reads the queue and sends data to Azure Monitor.
5. **Analysis**: Data flows into Kusto tables for dashboarding and analysis via LENS jobs and cooked tables.

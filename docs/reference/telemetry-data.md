# Telemetry Data Reference — Understanding azd Telemetry

> Schema reference for all azd telemetry events, fields, and data shapes.
> Use this to understand what data azd emits and how to work with it.

> [!NOTE]
> This is the **public** telemetry documentation. A Microsoft-internal companion set of docs
> (data pipeline, Kusto/Power BI reporting, runnable queries) is maintained separately for
> internal maintainers.

## Data Shape

All azd telemetry is emitted as Application Insights `RequestData` envelopes. Each command execution produces one top-level span, with optional child spans for sub-operations.

### Core Columns

| Column | Type | Description |
|--------|------|-------------|
| `TimeGenerated` | datetime | When the event was recorded |
| `Name` | string | Event/span name (e.g., `cmd.deploy`, `ext.run`) |
| `DurationMs` | real | Total span duration in milliseconds |
| `Success` | bool | Whether the operation succeeded |
| `ResultCode` | string | Error classification code (e.g., `Success`, `service.arm.500`, `internal.unclassified`) |
| `OperationId` | string | Unique ID for the top-level command invocation |
| `Properties` | dynamic | String/bool span attributes (JSON bag) |
| `Measurements` | dynamic | Numeric span attributes (JSON bag) |
| `AppVersion` | string | azd CLI version |

### Accessing Properties and Measurements

```kql
// String properties
| extend TemplateId = tostring(Properties['project.template.id'])

// Numeric measurements
| extend InteractTimeMs = toreal(Measurements['perf.interact_time'])

// Computed execution time (excludes user interaction)
| extend ExecutionTimeMs = DurationMs - toreal(Measurements['perf.interact_time'])
```

## Events Reference

Events are defined in `cli/azd/internal/tracing/events/events.go`. Each event becomes a span `Name`.

### Core Command Events (`cmd.*`)

Commands follow the pattern `cmd.<command.path>` where spaces become dots.

| Event Pattern | Example | Description |
|--------------|---------|-------------|
| `cmd.<command>` | `cmd.init`, `cmd.up`, `cmd.deploy` | Top-level command execution |
| `cmd.<group>.<command>` | `cmd.auth.login`, `cmd.env.new` | Subcommand execution |
| `cmd.<group>.<sub>.<command>` | `cmd.pipeline.config` | Deeper subcommands |

**Common command events:**
- `cmd.init` — project initialization
- `cmd.up` — full provision + deploy cycle
- `cmd.provision` — infrastructure provisioning
- `cmd.deploy` — application deployment
- `cmd.package` — application packaging
- `cmd.down` — resource teardown
- `cmd.auth.login` — authentication
- `cmd.env.new` / `cmd.env.select` — environment management
- `cmd.pipeline.config` — CI/CD pipeline setup
- `cmd.monitor` — monitoring
- `cmd.restore` — dependency restoration

### Extension Events (`ext.*`)

| Event | Description |
|-------|-------------|
| `ext.run` | Extension command execution |
| `ext.install` | Extension installation |
| `ext.update` | Extension update attempt |
| `ext.promote` | Registry promotion (e.g., dev → main) |
| `ext.usage` | Usage event reported by an extension through the telemetry service (official-registry extensions only) |

### Agent & Copilot Events

| Event | Description |
|-------|-------------|
| `agent.troubleshoot` | Agent troubleshooting session |
| `copilot.initialize` | Copilot agent initialization |
| `copilot.session` | Copilot session creation/resumption |

### MCP Events (`mcp.*`)

| Event Pattern | Description |
|--------------|-------------|
| `mcp.<tool_name>` | MCP tool invocation |

### Infrastructure Events (`arm.*`)

| Event | Description |
|-------|-------------|
| `arm.deploy.subscription` | ARM deployment at subscription scope |
| `arm.deploy.resourcegroup` | ARM deployment at resource group scope |
| `arm.stack.deploy.subscription` | ARM deployment stack at subscription scope |
| `arm.stack.deploy.resourcegroup` | ARM deployment stack at resource group scope |
| `arm.whatif.subscription` | ARM what-if at subscription scope |
| `arm.whatif.resourcegroup` | ARM what-if at resource group scope |
| `arm.validate.subscription` | ARM validation at subscription scope |
| `arm.validate.resourcegroup` | ARM validation at resource group scope |

### Other Events

| Event | Description |
|-------|-------------|
| `tools.pack.build` | Cloud Native Buildpacks build |
| `validation.provision` | Local provision validation |
| `hooks.exec` | Lifecycle hook execution |
| `aks.postprovision.skip` | AKS postprovision hook skipped |
| `deploy.appservice.zip` | App Service zip deployment |
| `container.credentials` | Container registry credential retrieval |
| `container.publish` | Container image publish |
| `container.remotebuild` | Remote container build |
| `exegraph.run` | Execution graph run (parallel operations) |
| `exegraph.step` | Single step within execution graph |
| `aspire.apphost.unsupported` | Detected an unsupported Aspire polyglot (non-C#) AppHost during app detection |

### VS Code Extension Events (`azure-dev.*`)

These are emitted by the VS Code extension via the VS Code telemetry framework (separate from CLI telemetry).

| Event | Description |
|-------|-------------|
| `azure-dev.activate` | Extension activated |
| `azure-dev.deactivate` | Extension deactivated |
| `azure-dev.tasks.dotenv` | Dotenv task executed |
| `azure-dev.commands.cli.<cmd>.task` | CLI command tasks: `deploy`, `provision`, `up`, `down`, `init`, `restore`, `package`, `infra-delete`, `login-cli`, `pipeline-config`, `env-new`, `env-refresh`, `env-list`, `extension-install`, `extension-uninstall`, `extension-upgrade`, `extension-source-add` |
| `azure-dev.views.*` | Workspace/extensions tree view resolution (`views.workspace.application.resolve`, `views.workspace.environment.resolve`, `views.extensions.resolve`) |
| `azure-dev.azureYaml.*` | `azure.yaml` language features (`azureYaml.provideDiagnostics`, `azureYaml.provideDocumentDropEdits`, `azureYaml.projectRename.provideWorkspaceEdits`) |
| `azure-dev.survey-check` | Survey eligibility check |
| `azure-dev.survey-prompt-response` | Survey prompt user response |

### VS RPC Events (`vsrpc.*`)

JSON-RPC events for VS Code ↔ azd communication. Follow the pattern `vsrpc.<method>`.

## Fields Reference

Fields appear as `Properties` (strings/bools) or `Measurements` (numbers).

### Application-Level Fields (Every Event)

These are set once at process startup and attached to **every** span.

| Field Key | Type | Description | Example Values |
|-----------|------|-------------|----------------|
| `service.name` | string | Always `"azd"` | `azd` |
| `service.version` | string | CLI version | `1.23.5` |
| `os.type` | string | Operating system | `linux`, `windows`, `darwin` |
| `os.version` | string | OS version | `10.0.22621`, `14.5` |
| `host.arch` | string | CPU architecture | `amd64`, `arm64` |
| `process.runtime.version` | string | Go runtime version | `go1.26.0` |
| `machine.id` | string | MAC address hash (pseudonymized) | SHA-256 hash |
| `machine.devdeviceid` | string | SQM device ID | UUID string |
| `execution.environment` | string | Where azd is running | See [Execution Environments](#execution-environments) |
| `service.installer` | string | How azd was installed | `msi`, `brew`, `choco`, `rpm`, `deb` |
| `exp.assignmentContext` | string | Experimentation platform assignment context. Attached to every event when the experimentation flighting service is enabled. | Opaque assignment string |

### Identity & Account Fields

| Field Key | Type | Description |
|-----------|------|-------------|
| `user_AuthenticatedId` | string | Entra ID Object ID |
| `ad.tenant.id` | string | Entra ID Tenant ID |
| `ad.account.type` | string | `User` or `Service Principal` |
| `ad.subscription.id` | string | Azure Subscription ID |

### Project Context Fields

| Field Key | Type | Hashed? | Description |
|-----------|------|---------|-------------|
| `project.template.id` | string | ✅ SHA-256 | Template identifier from `azure.yaml` metadata |
| `project.template.version` | string | ✅ SHA-256 | Template version |
| `project.name` | string | ✅ SHA-256 | Project name |
| `project.service.hosts` | string[] | ❌ | Host types — see [Service Targets](#service-targets) |
| `project.service.targets` | string[] | ❌ | Resolved deployment targets — see [Service Targets](#service-targets) |
| `project.service.languages` | string[] | ❌ | Languages across all services — see [Service Languages](#service-languages) |
| `project.service.language` | string | ❌ | Language of specific service being executed — see [Service Languages](#service-languages) |
| `platform.type` | string | ❌ | Platform integration (e.g., `aca`, `aks`) |

#### Service Targets

Valid values for `project.service.hosts` and `project.service.targets`:

| Value | Description |
|-------|-------------|
| `appservice` | Azure App Service |
| `containerapp` | Azure Container Apps |
| `containerapp-dotnet` | Azure Container Apps (Aspire) |
| `function` | Azure Functions |
| `staticwebapp` | Azure Static Web Apps |
| `springapp` | Azure Spring Apps |
| `aks` | Azure Kubernetes Service |
| `ai.endpoint` | Azure AI endpoint |

#### Service Languages

Valid values for `project.service.languages` and `project.service.language`:

| Value | Description |
|-------|-------------|
| `dotnet` | .NET |
| `csharp` | C# |
| `fsharp` | F# |
| `python` | Python |
| `js` | JavaScript |
| `ts` | TypeScript |
| `java` | Java |
| `docker` | Docker (containerized) |
| `swa` | Static Web App |
| `custom` | Custom framework |

#### Other Project Fields

| Field Key | Type | Hashed? | Description |
|-----------|------|---------|-------------|
| `env.name` | string | ✅ SHA-256 | Environment name |
| `config.features` | string[] | ❌ | Alpha/preview feature flags enabled for the run (e.g., `all`, or individual feature IDs) |

> **Joining with template names:** Template IDs are hashed. To resolve to human-readable names,
> join with a template lookup table using the hashed `project.template.id`.

### Command Entry-Point Fields

| Field Key | Type | Description |
|-----------|------|-------------|
| `cmd.flags` | string[] | Flag names that were set (values not recorded) |
| `cmd.args.count` | measurement | Number of positional arguments |
| `cmd.entry` | string | How the command was invoked (formatted as event name) |

### Error Fields

| Field Key | Type | Description |
|-----------|------|-------------|
| `error.category` | string | High-level error category |
| `error.code` | string | Specific error code |
| `error.type` | string | Same as `ResultCode` — the classified error type |
| `error.chain.types` | string[] | At most 16 host-reflected Go error type names, outermost first |
| `error.extension.cause_types` | string[] | Case-insensitive hashes of at most 16 normalized extension-provided cause labels |
| `error.mapper.source.type` | string | Sanitized source Go type for a mapper conversion failure |
| `error.mapper.destination.type` | string | Sanitized destination Go type for a mapper conversion failure |

#### Error Classification (ResultCode Taxonomy)

The `ResultCode` field classifies errors into categories. Understanding this taxonomy is essential for querying failures.

| Pattern | Category | Example |
|---------|----------|---------|
| `Success` | No error | — |
| `user.canceled` | User cancelled the operation | — |
| `auth.<detail>` | Authentication error | `auth.login_required`, `auth.not_logged_in`, `auth.identity_failed` |
| `service.arm.<statusCode>` | ARM service error | `service.arm.500`, `service.arm.409` |
| `service.aad.<detail>` | Entra ID (AAD) error | `service.aad.failed` |
| `service.<name>.<code>` | Other Azure service error | `service.graph.403` |
| `tool.<name>.failed` / `tool.<name>.missing` | External tool error. Failure spans also carry `error.tool.name` (and `error.tool.exitCode` for `failed`); see [Tool Invocation Attributes](#tool-invocation-attributes-external-cli-tools) | `tool.docker.failed`, `tool.git.missing` |
| `tool.multiple.missing` | Multiple required external tools missing; comma-separated names in `error.tool.name` | — |
| `ext.service.<svc>.<code>` | Extension service error | `ext.service.arm.500` |
| `ext.validation.*` | Extension validation error | `ext.validation.config` |
| `ext.auth.*` | Extension auth error | `ext.auth.expired` |
| `ext.dependency.*` | Extension dependency error | `ext.dependency.missing` |
| `internal.grpc.<status>` | Host-originated gRPC status without a more specific mapping | `internal.grpc.unavailable` |
| `internal.mapper_conversion` | Conversion between registered Go mapper types failed | — |
| `internal.unclassified` | Catch-all for unclassified errors | — |
| `internal.errors_errorString` | Legacy catch-all (being replaced by `internal.unclassified`) | — |

> **⚠️ Known gap:** Many errors historically fall into `internal.errors_errorString` / `internal.unclassified`
> because the error classifier only inspects the leaf error type. The `error.chain.types` field improves this
> by capturing the full error type chain.

### Service Attributes (Azure API Calls)

These attributes are emitted as classified error details. `MapError` prefixes their declared
`service.*` keys with `error.`, so the table lists the runtime keys used in queries.

| Field Key | Type | Description |
|-----------|------|-------------|
| `error.service.host` | string | Azure service host |
| `error.service.name` | string | Azure service name associated with the failure |
| `error.service.statusCode` | measurement or string | Numeric HTTP/service status code; AAD authentication errors use a string OAuth status such as `invalid_grant` |
| `error.service.method` | string | HTTP method |
| `error.service.errorCode` | string | Service-specific error code; some ARM deployment errors encode structured JSON |
| `error.service.correlationId` | string | Azure correlation ID |

### Tool Invocation Attributes (External CLI Tools)

Set **only when an external command-line tool invocation fails**, during error classification. Because they are stamped onto the failed span through the error pipeline (`MapError`), the keys appear with an `error.` prefix. They describe an external process azd shells out to (e.g., `docker`, `git`) — distinct from the `azd tool` management fields (see [Tool Management](#tool-management) below).

| Field Key | Type | Description |
|-----------|------|-------------|
| `error.tool.name` | string | Stable identifier for the failed external tool; core missing-tool display names use a fixed mapping, unknown names become `other`, and extension-provided `ToolError` names are limited to 1-64 ASCII characters from `[a-z0-9_-]`. Multiple missing tools remain comma-separated |
| `error.tool.exitCode` | measurement | Exit code returned by the failed tool |

### Performance Fields

| Field Key | Type | Description |
|-----------|------|-------------|
| `perf.interact_time` | measurement | Time (ms) spent waiting for user input |
| `perf.provision_duration_ms` | measurement | Wall-clock provisioning-phase duration (ms), emitted on `up`/`provision` |
| `perf.deploy_duration_ms` | measurement | Wall-clock deploy-phase duration (ms); excludes package/publish |
| `perf.total_duration_ms` | measurement | Total wall-clock duration for the entire up-graph execution (ms) |

> **Computing execution time:** `ExecutionTimeMs = DurationMs - Measurements['perf.interact_time']`
> This gives you the actual processing time, excluding user interaction (prompts, confirmations).

### Feature-Specific Fields

<details>
<summary><strong>Authentication</strong></summary>

| Field Key | Type | Values |
|-----------|------|--------|
| `auth.method` | string | `browser`, `device-code`, `service-principal-secret`, `service-principal-certificate`, `federated-github`, `federated-azure-pipelines`, `federated-oidc`, `managed-identity`, `external`, `oneauth`, `check-status` |
| `auth.cache_clear_failed` | string | `auth`, `subscriptions` — which cache failed to clear during the pre-login cleanup. Emitted on `auth login`. |
</details>

<details>
<summary><strong>Init / App Init</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `init.method` | string | `template`, `app`, `project`, `environment`, `copilot` |
| `appinit.detected.databases` | string[] | Databases detected during init |
| `appinit.detected.services` | string[] | Services detected during init |
| `appinit.confirmed.databases` | string[] | Databases confirmed by user |
| `appinit.confirmed.services` | string[] | Services confirmed by user |
| `appinit.modify_add.count` | measurement | Services added during modification |
| `appinit.modify_remove.count` | measurement | Services removed during modification |
| `appinit.lastStep` | string | Last init step reached |
</details>

<details>
<summary><strong>Aspire</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `aspire.apphost.language` | string | Language of a detected but unsupported Aspire polyglot (non-C#) AppHost. Emitted on `aspire.apphost.unsupported`. Values: `typescript`, `python`, `go`, `java`, `rust`. |
</details>

<details>
<summary><strong>Hooks</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `hooks.name` | string | Hook name (e.g., `preprovision`, `postdeploy`). Custom hooks are SHA-256 hashed. |
| `hooks.type` | string | Scope: `project`, `service`, or `layer` |
| `hooks.kind` | string | Executor: `sh`, `pwsh`, `python`, `js`, `ts`, `dotnet` |
</details>

<details>
<summary><strong>Pipeline Config</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `pipeline.provider` | string | `github`, `azdo` — the resolved CI provider (after auto-detection) |
| `pipeline.auth` | string | `federated`, `client-credentials` (only emitted when `--auth-type` is set) |
</details>

<details>
<summary><strong>Infrastructure</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `infra.provider` | string or string[] | provision/up/down: sorted, de-duplicated string slice of resolved providers — `bicep`/`terraform`/`arm`/`pulumi`, or `custom` (extension providers; raw name not emitted); multi-layer projects record each distinct value (e.g. `["bicep","terraform"]`). generate/synth: the value read from azure.yaml's `infra.provider` as a single string (`bicep`/`terraform`/`arm`/`pulumi`, `auto` when unset, or `custom` for extension providers; raw name not emitted) |
</details>

<details>
<summary><strong>Deployment</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `deploy.appservice.attempt` | measurement | Retry attempt number for App Service zip deploy |
| `deploy.appservice.linux` | string | Whether deploying to Linux App Service |
</details>

<details>
<summary><strong>Provision Validation</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `validation.provision.outcome` | string | `passed`, `warnings_accepted`, `canceled_by_errors`, `canceled_by_user`, `skipped`, `error` |
| `validation.provision.diagnostics` | string[] | Diagnostic IDs emitted |
| `validation.provision.rules` | string[] | Rule IDs executed |
| `validation.provision.extension_rules` | string[] | Rule IDs executed from extension-provided validation checks |
| `validation.provision.check_type` | string | Dispatch site that emitted the event: `arm-provision` (Bicep provider) or `provision` (provider-agnostic). Distinguishes the two emissions so Bicep provisions are not double-counted |
| `validation.provision.warning.count` | measurement | Number of warnings |
| `validation.provision.error.count` | measurement | Number of errors |
</details>

<details>
<summary><strong>Provision Cancellation</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `provision.cancellation` | string | `none`, `leave_running`, `canceled`, `cancel_timed_out`, `cancel_timed_out_nested`, `cancel_raced_succeeded`, `cancel_raced_failed`, `cancel_raced_deleted`, `cancel_too_late`, `cancel_failed` |
</details>

<details>
<summary><strong>Multi-Layer Provision</strong></summary>

Emitted on `azd provision` / `azd up` to measure adoption and safety of `infra.layers[]` parallel provisioning.

| Field Key | Type | Description |
|-----------|------|-------------|
| `provision.layer.count` | measurement | Number of `infra.layers[]` declared (0 or 1 = single-layer legacy path) |
| `provision.layer.max_parallel` | measurement | Largest number of layers scheduled in one dependency level (max achievable parallelism) |
| `provision.layer.safe_fallback_count` | measurement | Layers forced to depend on all earlier layers by the safe-by-default detector |
| `provision.layer.explicit_dependson_count` | measurement | Layers using the explicit `infra.layers[].dependsOn` override |
</details>

<details>
<summary><strong>Foundry Private Networking</strong></summary>

Emitted at provision start by the `microsoft.foundry` provisioning provider (the `azure.ai.projects` extension) to measure secured-agent adoption and the BYO-vs-managed split.

| Field Key | Type | Description |
|-----------|------|-------------|
| `provision.network_mode` | string | `none` (public account, no `network:` block), `byo` (customer VNet), or `managed` (Foundry-managed VNet) |
</details>

<details>
<summary><strong>Environment Management</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `env.count` | measurement | Number of environments that exist for the current project (`env list`) |
</details>

<details>
<summary><strong>Container Build</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `container.remoteBuild.count` | measurement | Number of remote container builds performed |
| `container.remotebuild` | bool | Whether a remote (ACR) build was requested (the configured preference) rather than a local build. |
</details>

<details>
<summary><strong>AKS</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `skip.reason` | string | Why AKS postprovision Kubernetes context setup was skipped. Bounded enum: `cluster_not_provisioned`. Emitted on `aks.postprovision.skip`. |
</details>

<details>
<summary><strong>Copilot</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `copilot.session.id` | string | Session identifier |
| `copilot.session.isNew` | string | Whether this is a new session |
| `copilot.session.messageCount` | measurement | Messages in session |
| `copilot.init.isFirstRun` | string | First copilot run |
| `copilot.init.reasoningEffort` | string | Reasoning effort level |
| `copilot.init.model` | string | Model used |
| `copilot.init.consentScope` | string | Consent scope |
| `copilot.mode` | string | Copilot mode |
| `copilot.message.model` | string | Model for specific message |
| `copilot.message.inputTokens` | measurement | Input token count |
| `copilot.message.outputTokens` | measurement | Output token count |
| `copilot.message.billingRate` | measurement | Billing rate |
| `copilot.message.premiumRequests` | measurement | Premium request count |
| `copilot.message.durationMs` | measurement | Message duration |
| `copilot.consent.approvedCount` | measurement | Approved consent actions |
| `copilot.consent.deniedCount` | measurement | Denied consent actions |
</details>

<details>
<summary><strong>Extensions</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `extension.id` | string | Extension identifier |
| `extension.version` | string | Extension version |
| `extension.event` | string | Extension-chosen usage event on `ext.usage`, or the host-defined lifecycle event on a failed lifecycle-hook `cmd.*` span |
| `ext.<key>` | string | One extension-supplied attribute on an `ext.usage` span. The key after the `ext.` prefix and the value are chosen by the extension |
| `ext.route` | string | Local-client route selected by `azure.ai.agents`: `inspector`, `playground`, or `suppressed` (`local_client.route.selected`) |
| `ext.stage` | string | Agent Inspector funnel stage: currently `ui_ready` (`inspector.funnel.stage`) |
| `ext.outcome` | string | Agent Inspector funnel-stage outcome: currently `succeeded` (`inspector.funnel.stage`) |
| `extension.installed` | string[] | List of installed extensions (`id@version`) |
| `extension.installed.source.category` | string[] | Installed extension source categories (`id@category`) |
| `extension.version.from` | string | Version before an update or promotion (`ext.update`, `ext.promote`) |
| `extension.version.to` | string | Version after an update or promotion (`ext.update`, `ext.promote`) |
| `extension.source` | string | Registry source used for an update and admission check for `ext.usage` |
| `extension.source.category` | string | Fixed source category: `azd`, `dev`, `nightly`, `local`, `bundle`, `other`, or `unknown` (`ext.install`, `ext.update`, `azd extension source add`) |
| `extension.source.kind` | string | Kind of `--source` argument: `none`, `registered`, or `location` (`azd extension list`, `show`, `install`, `update`) |
| `extension.source.category.from` | string | Fixed source category before a promotion (`ext.promote`) |
| `extension.source.category.to` | string | Fixed source category after a promotion (`ext.promote`) |
| `extension.update.duration_ms` | measurement | Duration (ms) of a single update (`ext.update`) |
| `extension.update.outcome` | string | Update result status (`ext.update`) |
| `extension.dependency_of` | string | Parent extension ID when an extension is updated as a dependency (`ext.update`) |
| `extension.dependency_update_count` | measurement | Number of dependency extensions updated recursively (`ext.update`) |

Each `ext.usage` span contains `extension.id`, `extension.version`,
`extension.source`, `extension.event`, and any number of dynamic `ext.*`
fields. The host writes the identity fields and applies the `ext.` prefix; the
extension chooses the event name, the key suffixes, and the values. Failed
extension commands instead carry `extension.id` and `extension.version` on
the failed `ext.run` span and do not set `extension.event`. Failed lifecycle
hooks carry `extension.id`, `extension.version`, and the lifecycle event on the
enclosing `cmd.*` span. The whole class is classified as `SystemMetadata` for
`FeatureInsight`. Extension authors are responsible
for keeping usage values low cardinality and free of customer content, and for
having them privacy reviewed with their extension.

Only extensions whose configured `azd` source matches the verified official
registry name, type, and normalized URL produce these spans, which is what ties
the recorded values to that privacy review. A report from any other install
source succeeds but records nothing, as does any report past the limit of 100
spans per `azd` invocation. This is a configuration-based admission check, not
a cryptographic provenance guarantee.

Reviewed first-party extension usage events currently include:

| Extension | `extension.event` | Trigger | Dynamic attributes |
|-----------|-------------------|---------|--------------------|
| `azure.ai.agents` | `local_client.route.selected` | `azd ai agent run` resolves the service and protocol profile; emitted before client availability, agent startup, and client launch | `ext.route`: `inspector`, `playground`, or `suppressed`; suppression takes precedence |
| `azure.ai.inspector` | `inspector.funnel.stage` | The Inspector SPA sends `setViewReady` after mounting | `ext.stage=ui_ready`; `ext.outcome=succeeded`; this does not indicate agent connection |

Source-category fields are classified from the configured source type and location, not the user-defined source name.
Raw source names, URLs, paths, and hosts are not emitted in those fields.
</details>

<details>
<summary><strong>Tool Management (<code>azd tool</code>)</strong><a id="tool-management"></a></summary>

Fields for the `azd tool` feature, including active `install`/`update`/`check` operations and the reserved first-run contract for azd-managed developer tools. These are **distinct** from the [Tool Invocation Attributes](#tool-invocation-attributes-external-cli-tools) above (which describe external processes azd shells out to).

> **Privacy:** only built-in tool IDs (e.g. `az-cli`, `vscode-bicep`) and version strings are captured. No file paths, no user-identifiable data, and no raw per-tool error text — failed tool IDs are recorded, but error detail stays with the global error middleware.

Built-in tool IDs come from azd's curated tool manifest (run `azd tool list` to see the current set), e.g. `az-cli`, `github-copilot-cli`, `vscode-azure-tools`, `vscode-bicep`, `azure-mcp-server`.

**Dormant first-run experience (reserved):**

The first-run middleware is not currently registered, so these fields are not emitted. They remain reserved for a possible future redesign.

| Field Key | Type | Description |
|-----------|------|-------------|
| `tool.firstrun.skip_reason` | string | Why first-run was bypassed (`env_var`, `no_prompt`, `ci_cd`, `non_interactive`, `already_completed`, `config_error`). Mutually exclusive with `tool.firstrun.outcome` |
| `tool.firstrun.outcome` | string | Terminal state when first-run ran (`completed`, `declined`, `cancelled`, `detect_failed`, `install_failed`) |
| `tool.firstrun.opt_in` | string | Whether the user accepted the first-run prompt |
| `tool.firstrun.tools_detected` | measurement | Built-in tools already installed when the check ran |
| `tool.firstrun.tools_offered` | measurement | Recommended tools offered for installation |
| `tool.firstrun.tools_selected` | measurement | Tools the user selected to install |
| `tool.firstrun.tools_selected_names` | string | Comma-separated built-in tool IDs selected (e.g. `az-cli,vscode-bicep`) |
| `tool.firstrun.tools_deselected_names` | string | Comma-separated offered tool IDs the user deselected |
| `tool.firstrun.install_success_count` | measurement | Tools installed successfully during first-run |
| `tool.firstrun.install_failure_count` | measurement | Tools that failed to install during first-run |
| `tool.firstrun.install_failed_ids` | string | Comma-separated tool IDs that failed during first-run |
| `tool.firstrun.install_duration_ms` | measurement | Total first-run install duration (ms) |

**Install / update / uninstall / check operations:**

| Field Key | Type | Description |
|-----------|------|-------------|
| `tool.id` | string | Built-in tool ID for single-tool operations (e.g. `az-cli`, `vscode-bicep`) |
| `tool.ids` | string | Comma-separated tool IDs for a batch operation |
| `tool.dry_run` | string | Whether `--dry-run` was specified |
| `tool.install.strategy` | string | Install strategy used. Package-manager values come from the tool manifest (`winget`, `brew`, `apt`, `npm`, `code`); the installer may also report `direct-download`, `command`, or `manual` (no available manager) |
| `tool.install.success` | string | Whether a single-target install, update, or uninstall succeeded |
| `tool.install.success_count` | measurement | Tools that succeeded in a batch install/update/uninstall |
| `tool.install.failure_count` | measurement | Tools that failed in a batch install/update/uninstall |
| `tool.install.failed_ids` | string | Comma-separated tool IDs whose install/update/uninstall failed |
| `tool.install.duration_ms` | measurement | Total install/update/uninstall duration (ms) |
| `tool.update.from_version` | string | Previous version (single-target update) |
| `tool.update.to_version` | string | New version after a successful update (single-target) |
| `tool.check.updates_available` | measurement | Installed tools with an available update (`azd tool check`) |
</details>

<details>
<summary><strong>MCP</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `mcp.client.name` | string | MCP client name |
| `mcp.client.version` | string | MCP client version |
</details>

<details>
<summary><strong>Execution Graph</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `exegraph.step.count` | measurement | Total steps in graph |
| `exegraph.max_concurrency` | measurement | Effective concurrency limit |
| `exegraph.error_policy` | string | `fail_fast` or `continue_on_error` |
| `exegraph.step.name` | string | Step name. **SHA-256 hashed** — embeds user-defined service/layer names from `azure.yaml` |
| `exegraph.step.deps` | string[] | Step dependencies (other step names). **SHA-256 hashed** for the same reason |
| `exegraph.step.tags` | string[] | Step tags (fixed internal vocabulary; emitted raw) |
| `exegraph.step.timeout_s` | measurement | Per-step timeout in seconds, if set |
</details>

<details>
<summary><strong>Pack (Buildpacks)</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `pack.builder.image` | string | Builder image name |
| `pack.builder.tag` | string | Builder image tag |
</details>

<details>
<summary><strong>Update</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `update.channel` | string | Update channel |
| `update.installMethod` | string | Installation method |
| `update.fromVersion` | string | Version before update |
| `update.toVersion` | string | Version after update |
| `update.result` | string | Update outcome |
</details>

<details>
<summary><strong>JSON-RPC</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `rpc.method` | string | RPC method name |
| `rpc.jsonrpc.request_id` | string | Request ID |
| `rpc.jsonrpc.error_code` | measurement | Error code |
</details>

<details>
<summary><strong>Agent</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `agent.fix.attempts` | measurement | Number of fix attempts |
</details>

### Execution Environments

The `execution.environment` field identifies where azd is running. Format: `<environment>[;<modifier1>;<modifier2>...]`

| Value | Description |
|-------|-------------|
| `Desktop` | Direct terminal usage |
| `Visual Studio` | VS integration |
| `Visual Studio Code` | VS Code integration |
| `VS Code Azure GitHub Copilot` | Azure Copilot in VS Code |
| `GitHub Copilot VSCode` | GitHub Copilot in VS Code |
| `Azure CloudShell` | Azure Cloud Shell |
| `Claude Code` | Claude Code AI agent |
| `Claude Code Desktop` | Best-effort detection of Claude Code launched from Claude Desktop |
| `Claude Code VSCode` | Best-effort detection of the Claude Code VS Code integration |
| `Codex` | Codex CLI |
| `Codex Desktop` | Codex Desktop app |
| `Cursor` | Cursor AI agent |
| `GitHub Copilot CLI` | GitHub Copilot CLI |
| `GitHub Copilot App` | GitHub Copilot App |
| `GitHub Copilot Cloud Agent` | GitHub Copilot cloud agent |
| `Gemini` | Gemini AI agent |
| `OpenCode` | OpenCode AI agent |
| `Pi` | Pi coding agent |
| `GitHub Actions` | GitHub Actions CI |
| `Azure Pipelines` | Azure Pipelines CI |
| `GitHub Codespaces` | GitHub Codespaces |
| Other CI systems | `UnknownCI`, `AppVeyor`, `Bamboo`, `BitBucket Pipelines`, `Travis CI`, `Circle CI`, `GitLab CI`, `Jenkins`, `AWS CodeBuild`, `TeamCity`, `JetBrains Space` |

**Modifiers:** `Azure App Spaces Portal` and `Microsoft Foundry Skill` may be appended as modifiers (`;` separated).

## Data Nuances & Gotchas

Important things to know when working with azd telemetry data. These are sourced from real investigations and issues.

### `infra.provider` Is Multi-Valued and Type-Polymorphic (by design)

`infra.provider` is intentionally emitted with different shapes depending on the command, so consumers must handle both:

- **`provision` / `up` / `down`** emit a **string array** — the sorted, de-duplicated set of IaC providers the command's layers resolve to (e.g. `["bicep"]`, or `["bicep","terraform"]` for a multi-layer project that mixes providers). This deliberately replaces an earlier single `"mixed"` marker so the specific combination is preserved while staying low-cardinality (built-in provider names are a fixed enum). The deprecated wrappers `infra create` (delegates to `provision`) and `infra delete` (delegates to `down`) emit the same array on their `cmd.infra.create` / `cmd.infra.delete` spans.
- **`infra generate` / `infra synth`** emit a **single string** — the value read from `azure.yaml`'s `infra.provider` (`auto` when unset), with non-built-in (extension) providers bucketed to `custom` so a raw user-chosen name is never emitted.

Two consequences to be aware of:

- The same key is a scalar `string` on some commands and a `string[]` on others. Queries must accept both (e.g. treat a scalar as a one-element set).
- Non-built-in (extension) providers are bucketed to `custom` **before** de-duplication, so a project that combines two *different* extension providers records a single `["custom"]` — the raw names are never emitted and the two are not distinguished.

In all cases the value is attached **directly to that command's span** (not the process-global usage bag), so it is scoped to `cmd.provision` / `cmd.up` / `cmd.down` / `cmd.infra.generate` (and the deprecated wrappers `cmd.infra.create` / `cmd.infra.delete`) only (both `infra generate` and its `synth` alias resolve to the canonical `cmd.infra.generate` span). It is never copied onto sibling in-process child commands — for example, a custom `workflows.up` running `provision` then `deploy` does **not** tag `cmd.deploy` or `cmd.package` with `infra.provider`.

### OperationId Reuse in Retry/Troubleshoot Flows

When `cmd.up` triggers `agent.troubleshoot` after a failure, the troubleshoot agent may retry the failed operation (e.g., `cmd.deploy`). These retries share the **same OperationId** as the parent `cmd.up` span.

This means you may see multiple rows with the same `OperationId` and `Name` (e.g., two `cmd.deploy` rows). These are **not duplicate events** — they are retry attempts within a single user session.

**Example pattern:**
```
OperationId: 28ce1f2898a4fec84522107e36c22038
  cmd.up (511s, FAIL)
  ├── cmd.package ✅
  ├── cmd.provision ✅
  ├── cmd.deploy ❌ (service.arm.500)        ← attempt 1
  ├── agent.troubleshoot ✅ (471s)
  │   ├── cmd.mcp.start
  │   ├── cmd.package ✅ → cmd.provision ✅  ← retry
  ├── cmd.deploy ❌ (service.aad.failed)      ← attempt 2
  └── cmd.deploy ❌ (service.aad.failed)      ← attempt 3
```

**Impact on queries:**
```kql
// ❌ WRONG — counts retries as separate invocations
| where Name == 'cmd.deploy' | summarize count()

// ✅ CORRECT — count distinct OperationIds to get unique invocations
| where Name == 'cmd.deploy' | summarize dcount(OperationId)

// ✅ Or be explicit about only first attempts
| where Name == 'cmd.deploy'
| summarize arg_min(TimeGenerated, *) by OperationId
```

### `azd up` Synthetic `cmd.provision` / `cmd.package` / `cmd.deploy` Spans

Since **v1.25.0** the unified `azd up` graph runs provision, package, publish, and
deploy in-process as `exegraph.step`s rather than as child `azd provision` /
`azd package` / `azd deploy` commands. To preserve the historical nested-span
shape, `up` emits **synthetic** phase spans as children of `cmd.up`. The
`cmd.provision` and `cmd.package` spans have been emitted since **v1.25.0**;
`cmd.deploy` is emitted only **from the issue #9054 fix onward** (see the version
window below — before that fix no synthetic `cmd.deploy` span was recorded under
`up`).

**Success/ResultCode behavior differs by version — mind the window when reading dashboards:**

- **v1.25.0 → the release containing the issue #9054 fix (exclusive):** the
  synthetic `cmd.provision` and `cmd.package` spans were always closed with an
  Unset status, which the AppInsights exporter reads as **Success**. In this
  window their under-`up` success rate is **over-reported** (a failing provision
  or package still shows Success and drops its ARM/Bicep ResultCode), and **no
  `cmd.deploy` span is emitted under `up` at all**, so the `cmd.deploy` success
  rate is **deflated** (it misses the high-success under-`up` deploy population).
- **From the #9054 fix onward:** each synthetic span carries the **real**
  per-phase outcome — on failure it gets the status + ResultCode `cmd.MapError`
  produces for that phase's error. For most failures this matches what the
  stand-alone `azd provision` / `azd package` / `azd deploy` command reports. One
  deliberate exception: when a provision error is wrapped with a user-facing
  suggestion (`ErrorWithSuggestion`), stand-alone `azd provision` reports
  `error.suggestion`, whereas the synthetic `cmd.provision` span maps the
  underlying graph-step error so the specific ARM/Bicep ResultCode (e.g.
  `tool.bicep.failed`, `service.arm.deployment.failed`) is preserved for
  dashboards. `cmd.deploy` is emitted **only when the deploy phase actually
  ran**; when provision/package fails first (FailFast skips deploy) no
  `cmd.deploy` span is emitted, matching legacy `azd up` where the deploy
  sub-command never ran.

**Correcting historical dashboards for the affected window:** the underlying
`exegraph.step` spans recorded the correct per-step status throughout, so an
**approximate** correction is to redirect the provision, package, and deploy
panels to them, matched on their raw `exegraph.step.tags` (`provision`,
`package`, `deploy`, `publish`) — **not** on `exegraph.step.name`, which is
SHA-256 hashed and cannot be filtered by phase. This is only approximate:
pre/post lifecycle hook and event failures are tagged `cmdhook`/`event` rather
than a phase tag, and a step canceled by a fail-fast teardown ends its own span
as an error even when the synthetic phase span deliberately does not blame it.
For an exact figure, exclude under-`up` `cmd.provision` / `cmd.package` Success
from reliability aggregates for that version range.

### `validation.provision` Emitted Twice Per Bicep Provision

The `validation.provision` event is emitted from **two** dispatch sites:

- The provider-agnostic **`provision`** validation in `provisioning.Manager` (runs for every provider before provisioning), and
- The Bicep provider's **`arm-provision`** validation (runs only for Bicep, using the ARM template snapshot).

For a **Bicep** provision with a `validation-provider` extension loaded, **both** fire in a single run, producing two `validation.provision` rows (each with its own `outcome`, warning/error counts, and rule lists). Use the `validation.provision.check_type` field (`provision` vs `arm-provision`) to distinguish them.

**Impact on queries:**
```kql
// ❌ WRONG — double-counts Bicep provisions
| where Name == 'validation.provision' | summarize count()

// ✅ CORRECT — group/filter by the dispatch site
| where Name == 'validation.provision'
| summarize count() by tostring(customDimensions['validation.provision.check_type'])
```

### The `internal.unclassified` / `internal.errors_errorString` Catch-All

Many failed commands produce the catch-all result code `internal.errors_errorString` (being renamed to `internal.unclassified`). This happens because the error classifier inspects only the leaf error type, and `errors.New()` / `fmt.Errorf()` without `%w` produce `*errors.errorString`, which has no domain meaning.

**To investigate these errors:**
1. Check `error.chain.types` (if available) for the full error type chain
2. Correlate with `error.service.errorCode` or `error.service.statusCode` for Azure API failures
3. Look at surrounding span context (same `OperationId`) for additional detail

### Hashed Fields and Template Joins

Fields like `project.template.id`, `project.name`, `env.name`, `exegraph.step.name`, and `exegraph.step.deps` are **SHA-256 hashed** before emission to protect privacy. You cannot reverse them. (`hooks.name` is also hashed except for built-in lifecycle hook names.)

To resolve template IDs to human-readable names, join with a template lookup table using the hashed ID.

### Execution Time vs Duration

`DurationMs` includes time the user spent at prompts (confirmations, selections). To compute actual execution time:
```kql
| extend ExecutionTimeMs = DurationMs - toreal(Measurements['perf.interact_time'])
```

## Feature → Telemetry Mapping

How to find telemetry for a given feature area. Start here if you know the feature and want to know what to query.

| Feature Area | Key Events | Key Fields / Filters | What You Can Measure |
|-------------|------------|---------------------|---------------------|
| **Core Workflows (init/up/deploy/provision/down)** | `cmd.init`, `cmd.up`, `cmd.deploy`, `cmd.provision`, `cmd.down` | `cmd.entry`, `cmd.flags` | Adoption, success rate, duration, error patterns |
| **Deployment Targets** | `cmd.deploy`, `cmd.package` | `project.service.targets` (`appservice`, `containerapp`, `aks`, etc.) | Usage by target, success rate per target |
| **Container Apps (Aspire)** | `cmd.deploy`, `cmd.provision` | `project.service.targets` = `containerapp-dotnet`, `platform.type` = `aca` | Aspire-specific adoption and success |
| **Language Support** | `cmd.deploy`, `cmd.package`, `cmd.restore` | `project.service.languages`, `project.service.language` | Usage by language |
| **Templates** | `cmd.init`, `cmd.up` | `project.template.id` (hashed — join with template lookup to resolve) | Template adoption, success by template |
| **Provisioning (IaC)** | `cmd.provision`, `cmd.up`, `cmd.down`, `arm.deploy.*`, `arm.validate.*` | `infra.provider` (`bicep`/`terraform`/`arm`/`pulumi`/custom; slice of each distinct provider for multi-layer projects) | Provision success, ARM errors, duration |
| **Authentication** | `cmd.auth.login` | `auth.method` | Auth method usage, failure rates |
| **CI/CD Pipelines** | `cmd.pipeline.config` | `pipeline.provider` | Pipeline setup adoption |
| **Extensions** | `ext.run`, `cmd.*`, `ext.install`, `ext.update`, `ext.usage` | `extension.id`, `extension.version`, `extension.installed`, `extension.event` (lifecycle hooks), `error.chain.types`, `error.extension.cause_types`, `error.mapper.source.type`, `error.mapper.destination.type`, `error.tool.name`, dynamic `ext.*` fields | Extension adoption, command and lifecycle-hook errors, and usage events |
| **MCP** | `mcp.<tool_name>` | `mcp.client.name`, `mcp.client.version` | Tool usage by client |
| **Agentic (Copilot)** | `copilot.initialize`, `copilot.session` | `copilot.mode`, `copilot.init.model`, `copilot.message.*` | Session counts, token usage |
| **Agent Troubleshooting** | `agent.troubleshoot` | `agent.fix.attempts` | Auto-fix adoption, retry counts |
| **VS Code Extension** | `azure-dev.*` | `azure-dev.commands.<cmd>` | VS Code usage, command usage |
| **Execution Environment** | All events | `execution.environment` | Usage by environment, CI vs local |
| **Self-Update** | `cmd.update` | `update.installMethod`, `update.fromVersion` | Update adoption |
| **Hooks** | `hooks.exec` | `hooks.name`, `hooks.type`, `hooks.kind` | Hook usage by type |
| **Container Build** | `container.publish`, `container.remotebuild`, `tools.pack.build` | `pack.builder.image`, `container.remotebuild` | Build method usage (local vs. remote ACR build), success rates |
| **App Detection (Aspire polyglot)** | `aspire.apphost.unsupported` | `aspire.apphost.language` (`typescript`/`python`/`go`/`java`/`rust`) | How often an unsupported Aspire polyglot (non-C#) AppHost is encountered, by language. **Emitted only during app detection for `init` and fresh `up` (no existing `azure.yaml`)** — not for already-initialized projects, so absence does not mean zero unsupported AppHosts. |
| **Tool Management (`azd tool`)** | `cmd.tool.install`, `cmd.tool.update`, `cmd.tool.uninstall`, `cmd.tool.check` | `tool.id`, `tool.install.strategy` | Install/update/uninstall success, update availability |

## See Also

- [Architecture](../architecture/telemetry.md) — End-to-end telemetry flow
- [Feature Telemetry Guide](../guides/feature-telemetry.md) — How to add telemetry for new features
- [Telemetry Schema (canonical)](../specs/metrics-audit/telemetry-schema.md) — Source-of-truth schema in the codebase
- [Privacy Review Checklist](../specs/metrics-audit/privacy-review-checklist.md) — When and how to do privacy reviews

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
| `CopilotInitializeEvent` | `copilot.initialize` | Copilot initialization event |
| `CopilotSessionEvent` | `copilot.session` | Copilot session lifecycle event |

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
| Execution environment | `execution.environment` | SystemMetadata | BusinessInsight | CI system detection |
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

| Field | OTel Key | Classification | Purpose |
|-------|----------|----------------|---------|
| Error category | `error.category` | SystemMetadata | PerformanceAndHealth |
| Error code | `error.code` | SystemMetadata | PerformanceAndHealth |
| Error type | `error.type` | SystemMetadata | PerformanceAndHealth |
| Inner error | `error.inner` | SystemMetadata | PerformanceAndHealth |
| Error frame | `error.frame` | SystemMetadata | PerformanceAndHealth |

Error classification is handled by `MapError` in `internal/cmd/errors.go`, which categorizes
errors into: update errors, auth errors, service (Azure) errors, deployment errors, extension
errors, tool errors, sentinel errors, and network errors. Each receives an `error.code`,
`error.category`, and contextual attributes.

### Service Attributes

| Field | OTel Key | Classification | Purpose | Notes |
|-------|----------|----------------|---------|-------|
| Service host | `service.host` | SystemMetadata | PerformanceAndHealth | |
| Service name | `service.name` | SystemMetadata | PerformanceAndHealth | |
| Status code | `service.statusCode` | SystemMetadata | PerformanceAndHealth | **Measurement** |
| Method | `service.method` | SystemMetadata | PerformanceAndHealth | |
| Error code | `service.errorCode` | SystemMetadata | PerformanceAndHealth | **Measurement** |
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

| Field | OTel Key | Classification | Purpose |
|-------|----------|----------------|---------|
| Extension ID | `extension.id` | SystemMetadata | FeatureInsight |
| Extension version | `extension.version` | SystemMetadata | FeatureInsight |
| Extension installed | `extension.installed` | SystemMetadata | FeatureInsight |

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

## New Fields (Added by This Audit)

The following fields are being introduced to close telemetry gaps identified in the
[Feature-Telemetry Matrix](feature-telemetry-matrix.md).

| Field | OTel Key | Classification | Purpose | Values |
|-------|----------|----------------|---------|--------|
| Auth method | `auth.method` | SystemMetadata | FeatureInsight | `browser`, `device-code`, `service-principal-secret`, `service-principal-certificate`, `federated-github`, `federated-azure-pipelines`, `federated-oidc`, `managed-identity`, `external`, `oneauth`, `check-status` |
| Env count | `env.count` | SystemMetadata | FeatureInsight | **Measurement** — number of environments |
| Hooks name | `hooks.name` | SystemMetadata | FeatureInsight | Built-in hook name (raw) or SHA-256 hash for extension/custom hooks. Known values: `prebuild`, `postbuild`, `predeploy`, `postdeploy`, `predown`, `postdown`, `prepackage`, `postpackage`, `preprovision`, `postprovision`, `prepublish`, `postpublish`, `prerestore`, `postrestore`, `preup`, `postup` |
| Hooks type | `hooks.type` | SystemMetadata | FeatureInsight | `project`, `service` |
| Pipeline provider | `pipeline.provider` | SystemMetadata | FeatureInsight | `github`, `azdo`, `auto` (auto-detected) |
| Pipeline auth | `pipeline.auth` | SystemMetadata | FeatureInsight | `federated`, `client-credentials`, `auto` (auto-detected) |
| Infra provider | `infra.provider` | SystemMetadata | FeatureInsight | `bicep`, `terraform`, `auto` (auto-detected from files) |

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
| `CaseInsensitiveHash(value)` | Lowercases, then SHA-256 hashes |
| `StringHashed(key, value)` | Creates an OTel attribute with a case-insensitive SHA-256 hash |
| `StringSliceHashed(key, values)` | Hashes each element in a string slice independently |

Fields that are hashed: `project.template.id`, `project.template.version`, `project.name`, `env.name`.

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

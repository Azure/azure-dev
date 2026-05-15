# Telemetry Data Reference — Understanding & Querying azd Telemetry

> Schema reference for all azd telemetry events, fields, and Kusto tables.
> Use this to understand what data exists and how to query it.
>


## Kusto Tables

All azd telemetry lands in Azure Data Explorer (Kusto):

- **Cluster:** `DDAzureClients`
- **Database:** `DevCli`
- **Primary table:** `RawEventsAppRequests`
- **Supplementary tables:** `Templates`, `TemplateVersions`, `AzdKPIs`

### RawEventsAppRequests — Core Columns

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

Events are defined in `cli/azd/internal/tracing/events/events.go`. Each event becomes a span `Name` in Kusto.

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
| `ext.upgrade` | Extension upgrade attempt |
| `ext.promote` | Registry promotion (e.g., dev → main) |

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
| `validation.preflight` | Local preflight validation |
| `hooks.exec` | Lifecycle hook execution |
| `aks.postprovision.skip` | AKS postprovision hook skipped |
| `deploy.appservice.zip` | App Service zip deployment |
| `container.credentials` | Container registry credential retrieval |
| `container.publish` | Container image publish |
| `container.remotebuild` | Remote container build |
| `exegraph.run` | Execution graph run (parallel operations) |
| `exegraph.step` | Single step within execution graph |

### VS Code Extension Events (`azure-dev.*`)

These are emitted by the VS Code extension via the VS Code telemetry framework (separate from CLI telemetry).

| Event | Description |
|-------|-------------|
| `azure-dev.activate` | Extension activated |
| `azure-dev.deactivate` | Extension deactivated |
| `azure-dev.tasks.dotenv` | Dotenv task executed |
| `azure-dev.commands.<cmd>` | CLI command tasks (deploy, provision, up, down, init, login, restore, package) |
| `azure-dev.survey-check` | Survey eligibility check |
| `azure-dev.survey-prompt-response` | Survey prompt user response |

### VS RPC Events (`vsrpc.*`)

JSON-RPC events for VS Code ↔ azd communication. Follow the pattern `vsrpc.<method>`.

## Fields Reference

Fields appear as `Properties` (strings/bools) or `Measurements` (numbers) in the Kusto table.

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
| `containerapp-dotnet` | Azure Container Apps (.NET Aspire) |
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

> **Joining with template names:** Template IDs are hashed. To resolve to human-readable names,
> join with the `Templates` table using `project.template.id` = `Templates.Hash`.
> The `addTemplateColumns` Kusto function does this automatically.

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
| `error.chain.types` | string[] | Full Go error type chain, outermost first |

#### Error Classification (ResultCode Taxonomy)

The `ResultCode` field classifies errors into categories. Understanding this taxonomy is essential for querying failures.

| Pattern | Category | Example |
|---------|----------|---------|
| `Success` | No error | — |
| `user.canceled` | User cancelled the operation | — |
| `service.arm.<statusCode>` | ARM service error | `service.arm.500`, `service.arm.409` |
| `service.aad.<detail>` | Entra ID (AAD) error | `service.aad.failed` |
| `service.<name>.<code>` | Other Azure service error | `service.graph.403` |
| `tool.<name>.<exitCode>` | External tool error | `tool.docker.1` |
| `ext.service.<svc>.<code>` | Extension service error | `ext.service.arm.500` |
| `ext.validation.*` | Extension validation error | `ext.validation.config` |
| `ext.auth.*` | Extension auth error | `ext.auth.expired` |
| `ext.dependency.*` | Extension dependency error | `ext.dependency.missing` |
| `internal.unclassified` | Catch-all for unclassified errors | — |
| `internal.errors_errorString` | Legacy catch-all (being replaced by `internal.unclassified`) | — |

> **⚠️ Known gap:** Many errors historically fall into `internal.errors_errorString` / `internal.unclassified`
> because the error classifier only inspects the leaf error type. The `error.chain.types` field improves this
> by capturing the full error type chain.

### Service Attributes (Azure API Calls)

| Field Key | Type | Description |
|-----------|------|-------------|
| `service.host` | string | Azure service host |
| `service.name` | string | Azure service name |
| `service.statusCode` | measurement | HTTP status code |
| `service.method` | string | HTTP method |
| `service.errorCode` | measurement | Service-specific error code |
| `service.correlationId` | string | Azure correlation ID |

### Performance Fields

| Field Key | Type | Description |
|-----------|------|-------------|
| `perf.interact_time` | measurement | Time (ms) spent waiting for user input |

> **Computing execution time:** `ExecutionTimeMs = DurationMs - Measurements['perf.interact_time']`
> This gives you the actual processing time, excluding user interaction (prompts, confirmations).

### Feature-Specific Fields

<details>
<summary><strong>Authentication</strong></summary>

| Field Key | Type | Values |
|-----------|------|--------|
| `auth.method` | string | `browser`, `device-code`, `service-principal-secret`, `service-principal-certificate`, `federated-github`, `federated-azure-pipelines`, `federated-oidc`, `managed-identity`, `external`, `oneauth`, `check-status` |
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
| `pipeline.provider` | string | `github`, `azdo`, `auto` |
| `pipeline.auth` | string | `federated`, `client-credentials`, `auto` |
</details>

<details>
<summary><strong>Infrastructure</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `infra.provider` | string | `bicep`, `terraform`, `auto` |
</details>

<details>
<summary><strong>Deployment</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `deploy.appservice.attempt` | measurement | Retry attempt number for App Service zip deploy |
| `deploy.appservice.linux` | string | Whether deploying to Linux App Service |
</details>

<details>
<summary><strong>Preflight Validation</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `validation.preflight.outcome` | string | `passed`, `warnings_accepted`, `aborted_by_errors`, `aborted_by_user`, `skipped`, `error` |
| `validation.preflight.diagnostics` | string[] | Diagnostic IDs emitted |
| `validation.preflight.rules` | string[] | Rule IDs executed |
| `validation.preflight.warning.count` | measurement | Number of warnings |
| `validation.preflight.error.count` | measurement | Number of errors |
</details>

<details>
<summary><strong>Provision Cancellation</strong></summary>

| Field Key | Type | Description |
|-----------|------|-------------|
| `provision.cancellation` | string | `none`, `leave_running`, `canceled`, `cancel_timed_out`, `cancel_timed_out_nested`, `cancel_raced_succeeded`, `cancel_raced_failed`, `cancel_raced_deleted`, `cancel_too_late`, `cancel_failed` |
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
| `extension.installed` | string[] | List of installed extensions (`id@version`) |
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
| `exegraph.max_concurrency` | string | Effective concurrency limit |
| `exegraph.error_policy` | string | `fail_fast` or `continue_on_error` |
| `exegraph.step.name` | string | Step name |
| `exegraph.step.deps` | string[] | Step dependencies |
| `exegraph.step.tags` | string[] | Step tags |
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
| `agent.fix.attempts` | string | Number of fix attempts |
</details>

### Execution Environments

The `execution.environment` field identifies where azd is running. Format: `<environment>[;<modifier1>;<modifier2>...]`

| Value | Description |
|-------|-------------|
| `Desktop` | Direct terminal usage |
| `Visual Studio` | VS integration |
| `Visual Studio Code` | VS Code integration |
| `VS Code Azure GitHub Copilot` | Azure Copilot in VS Code |
| `Azure CloudShell` | Azure Cloud Shell |
| `Claude Code` | Claude Code AI agent |
| `GitHub Copilot CLI` | GitHub Copilot CLI |
| `Gemini` | Gemini AI agent |
| `OpenCode` | OpenCode AI agent |
| `GitHub Actions` | GitHub Actions CI |
| `Azure Pipelines` | Azure Pipelines CI |
| `GitHub Codespaces` | GitHub Codespaces |
| Other CI systems | `AppVeyor`, `Bamboo`, `BitBucket Pipelines`, `Travis CI`, `Circle CI`, `GitLab CI`, `Jenkins`, `AWS CodeBuild`, `Google Cloud Build`, `TeamCity`, `JetBrains Space` |

**Modifier:** `Azure App Spaces Portal` may be appended as a modifier (`;` separated).

## Data Nuances & Gotchas

Important things to know when querying azd telemetry data. These are sourced from real investigations and issues.

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
// ❌ WRONG — counts retries as separate users/invocations
getAzdEvents(...) | where Name == 'cmd.deploy' | summarize count()

// ✅ CORRECT — count distinct OperationIds to get unique invocations
getAzdEvents(...) | where Name == 'cmd.deploy' | summarize dcount(OperationId)

// ✅ Or be explicit about only first attempts
getAzdEvents(...)
| where Name == 'cmd.deploy'
| summarize arg_min(TimeGenerated, *) by OperationId
```

### The `internal.unclassified` / `internal.errors_errorString` Catch-All

Many failed commands produce the catch-all result code `internal.errors_errorString` (being renamed to `internal.unclassified`). This happens because the error classifier inspects only the leaf error type, and `errors.New()` / `fmt.Errorf()` without `%w` produce `*errors.errorString`, which has no domain meaning.

**To investigate these errors:**
1. Check `error.chain.types` (if available) for the full error type chain
2. Correlate with `service.errorCode` or `service.statusCode` for Azure API failures
3. Look at surrounding span context (same `OperationId`) for additional detail

### Hashed Fields and Template Joins

Fields like `project.template.id`, `project.name`, `env.name` are **SHA-256 hashed** before emission to protect privacy. You cannot reverse them.

To resolve template IDs to human-readable names, use the `Templates` table:
```kql
getAzdEvents(...)
| invoke addTemplateColumns()
| project TimeGenerated, TemplateName, Success
```

### Execution Time vs Duration

`DurationMs` includes time the user spent at prompts (confirmations, selections). Use:
```kql
| extend ExecutionTimeMs = DurationMs - toreal(Measurements['perf.interact_time'])
```

### Internal vs External Users

To distinguish Microsoft internal users from external:
```kql
// The addCustomerColumns function enriches with customer details
getAzdEvents(...) | invoke addCustomerColumns()

// Or filter by tenant/subscription patterns
getAzdEvents(...) | invoke flagTestAzSubs()
```

## Common Query Patterns

### Basic: Command Usage Over Time
```kql
getAzdEvents(startDate=ago(30d), endDate=now(), true, true)
| where Name startswith "cmd."
| summarize Users = dcount(tostring(Properties['machine.id'])), Executions = count() by Name
| order by Users desc
```

### Feature Adoption: Template Usage
```kql
getAzdEvents(startDate=ago(30d), endDate=now(), true, true)
| where Name == 'cmd.up' and Success
| invoke addTemplateColumns()
| summarize Users = dcount(tostring(Properties['machine.id'])) by TemplateName
| order by Users desc
```

### Error Analysis: Top Failure Reasons
```kql
getAzdEvents(startDate=ago(7d), endDate=now(), true, true)
| where Name == 'cmd.deploy' and not(Success)
| summarize Count = count() by ResultCode
| order by Count desc
```

### Performance: Command Duration (p50/p95)
```kql
getAzdEvents(startDate=ago(30d), endDate=now(), true, true)
| where Name == 'cmd.provision' and Success
| extend ExecutionTimeMs = DurationMs - toreal(Measurements['perf.interact_time'])
| summarize p50 = percentile(ExecutionTimeMs, 50), p95 = percentile(ExecutionTimeMs, 95) by bin(TimeGenerated, 1d)
```

### Funnel: Init → Provision → Deploy Success
```kql
let timeRange = ago(30d);
let events = getAzdEvents(startDate=timeRange, endDate=now(), true, true);
let initUsers = events | where Name == 'cmd.init' | summarize by MachineId = tostring(Properties['machine.id']);
let provisionUsers = events | where Name == 'cmd.provision' and Success | summarize by MachineId = tostring(Properties['machine.id']);
let deployUsers = events | where Name == 'cmd.deploy' and Success | summarize by MachineId = tostring(Properties['machine.id']);
print
    Init = toscalar(initUsers | count),
    Provision = toscalar(provisionUsers | count),
    Deploy = toscalar(deployUsers | count)
```

## Kusto Functions Reference

These reusable functions are deployed to `DDAzureClients.DevCli` and simplify common query patterns.
See [Dashboards & Reports](telemetry-dashboards.md) for full details.

| Function | Purpose |
|----------|---------|
| `getAzdEvents(...)` | Base query: filters `RawEventsAppRequests` by date, local clients, daily builds, min version |
| `getAzdArmEvents(...)` | ARM-specific event query |
| `addTemplateColumns` | Joins `Templates` table to resolve template hashes to names |
| `addCustomerColumns` | Enriches with customer/org details |
| `addAzSubColumns` | Adds Azure subscription metadata |
| `addExecutionTimeColumns` | Adds `ExecutionTimeMs` (duration minus interaction time) |
| `addAzdAndArmErrorDetails` | Enriches error rows with ARM error details |
| `flagTestAzSubs` | Flags known test/internal subscriptions |
| `calcAzdOperations(...)` | Calculates operation-level metrics |
| `calcFirstSuccessfulExecution(...)` | Finds first successful execution per user |
| `calcNeverBeforeSeenUsersForAzd(...)` | Identifies new users |

## Feature → Telemetry Mapping

How to find telemetry for a given feature area. Start here if you know the feature and want to know what to query.

| Feature Area | Key Events | Key Fields / Filters | What You Can Measure |
|-------------|------------|---------------------|---------------------|
| **Core Workflows (init/up/deploy/provision/down)** | `cmd.init`, `cmd.up`, `cmd.deploy`, `cmd.provision`, `cmd.down` | `cmd.entry`, `cmd.flags` | Adoption, success rate, duration, error patterns |
| **Deployment Targets** | `cmd.deploy`, `cmd.package` | `project.service.targets` (`appservice`, `containerapp`, `aks`, etc.) | Usage by target, success rate per target |
| **Container Apps (.NET / Aspire)** | `cmd.deploy`, `cmd.provision` | `project.service.targets` = `containerapp-dotnet`, `platform.type` = `aca` | Aspire-specific adoption and success |
| **Language Support** | `cmd.deploy`, `cmd.package`, `cmd.restore` | `project.service.languages`, `project.service.language` (`dotnet`, `python`, `java`, etc.) | Usage by language |
| **Templates** | `cmd.init`, `cmd.up` | `project.template.id` (hashed — use `addTemplateColumns` to resolve) | Template adoption, success by template |
| **Provisioning (IaC)** | `cmd.provision`, `arm.deploy.*`, `arm.validate.*` | `infra.provider` (`bicep`, `terraform`), ARM event details | Provision success, ARM errors, duration |
| **Authentication** | `cmd.auth.login` | `auth.method` | Auth method usage, failure rates |
| **CI/CD Pipelines** | `cmd.pipeline.config` | `pipeline.provider` | Pipeline setup adoption |
| **Extensions** | `ext.run`, `ext.install`, `ext.upgrade` | `extension.id`, `extension.version`, `extension.installed` | Extension adoption, install/upgrade rates, errors |
| **MCP** | `mcp.<tool_name>` | `mcp.client.name`, `mcp.client.version` | Tool usage by client, call volume |
| **Agentic (Copilot)** | `copilot.initialize`, `copilot.session`, `cmd.copilot.chat` | `copilot.mode`, `copilot.init.model`, `copilot.message.*` | Session counts, token usage, model selection |
| **Agent Troubleshooting** | `agent.troubleshoot` | `agent.fix.attempts` | Auto-fix adoption, retry counts |
| **VS Code Extension** | `azure-dev.*` | `azure-dev.commands.<cmd>` | VS Code usage, activation, command usage |
| **Execution Environment** | All events | `execution.environment` (`Desktop`, `GitHub Actions`, `Claude Code`, etc.) | Usage by environment, CI vs local |
| **Self-Update** | `cmd.update` | `update.installMethod`, `update.fromVersion`, `update.toVersion` | Update adoption |
| **Hooks** | `hooks.exec` | `hooks.name`, `hooks.type`, `hooks.kind` | Hook usage by type and executor |
| **Container Build** | `container.publish`, `container.remotebuild`, `tools.pack.build` | `pack.builder.image`, `pack.builder.tag` | Build method usage, success rates |

## See Also

- [Architecture](../architecture/telemetry.md) — End-to-end telemetry flow
- [Feature Telemetry Guide](../guides/feature-telemetry.md) — How to add telemetry for new features
- [Dashboards & Reports](telemetry-dashboards.md) — Power BI reports and Kusto functions
- [Telemetry Schema (canonical)](../../specs/metrics-audit/telemetry-schema.md) — Source-of-truth schema in the codebase
- [Privacy Review Checklist](../../specs/metrics-audit/privacy-review-checklist.md) — When and how to do privacy reviews

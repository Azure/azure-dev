# Init step timing telemetry

<!-- cspell:ignore todouble tostring percentiles leftouter countif isnotnull -->

`azd ai agent init` records individual business steps using the existing extension
usage telemetry API. `azure.ai.projects` records the project and deployment
authoring steps delegated to it by Init. The same project timings are also
available when those commands are run directly.

This does not change prompts, retry policies, command results, or the existing
Init completion record and its duration. No additional Azure, project, model,
or configuration queries are made to collect timing data.

## Events and correlation

All new events are host-created `ext.usage` spans:

| `extension.event` | Owning command |
|---|---|
| `agent.init.step` / `agent.init.summary` | `azd ai agent init` |
| `project.add.step` / `project.add.summary` | `azd ai project add` |
| `project.deployment.add.step` / `project.deployment.add.summary` | `azd ai project deployment add` |

The SDK forwards the original W3C trace context through the existing telemetry
RPC. Join on `operation_Id` (OperationId in cooked data). Use the native parent
span ID and `ext.init.flow.scope` to distinguish extension command invocations
within a trace. Sequence numbers restart for each command; they are not a
cross-process counter. A deployment action that calls project authoring inside
the same extension process reuses its tracker and does not generate a second
summary.

The Init command also records delegated Workflow calls as
`project_authoring` and `model_deployment_authoring` external steps. These include
the delegated command's internal work, prompts, and telemetry reporting. Do not
add those durations to the projects extension's internal step durations.

**Count Init samples from the existing command completion records, never from
step or summary events.** For the current host, the extension completion is
`ext.run` with `cmd.entry=cmd.ai.agent.init`; see
[operation statistics](operation-telemetry.md). The new events are not new Init
attempts.

## Field schema

The host prefixes every extension-supplied key with `ext.`. The usage API accepts
strings, so numeric values are string encoded. Durations have three decimal
places in milliseconds; convert them with `todouble` in queries.

All fields are un-hashed `SystemMetadata` with purpose `PerformanceAndHealth`
in the extension field catalog. They are string properties, not native telemetry
measurements; queries must convert numeric durations and counts explicitly.

| Final key | Events | Values / meaning |
|---|---|---|
| `ext.init.flow.version` | Both | Fixed schema version `1`. |
| `ext.init.flow.scope` | Both | `agent.init`, `project.add`, `project.deployment.add`. |
| `ext.init.flow.outcome` | Summary | `success`, `error`, `canceled`, `timeout`, from the returned command error. |
| `ext.init.step.id` | Step | Fixed business identifier; never derived from a question, answer, model, service, path, or resource name. |
| `ext.init.step.category` | Step | `prompt`, `execution`, `external`. |
| `ext.init.step.sequence` | Step | Increasing start order within the owning command, starting at 1. Repeated attempts retain their ID and receive new sequence numbers. |
| `ext.init.step.outcome` | Step | `success`, `error`, `canceled`, `timeout`. |
| `ext.init.step.input_source` | Step | Optional `user`, `flag`, `config`, `environment`, `default`; omitted when the origin is not known reliably. |
| `ext.init.step.timing_scope` | Step | `operation`, `prompt_rpc`, `prompt_resolution_rpc`, `composite_prompt_rpc`. |
| `ext.duration_ms` | Step | Monotonic elapsed duration of the recorded operation, **not** the host usage span duration. |
| `ext.init.prompt.count` | Summary | Simple interactive prompt RPC attempts, including retries, errors, and cancellations. |
| `ext.init.prompt.total_duration_ms` | Summary | Sum of those simple prompt RPC durations. |
| `ext.init.prompt.longest.id` | Summary | Longest simple prompt ID, or fixed sentinel `none` when there are no simple prompts. |
| `ext.init.prompt.longest_duration_ms` | Summary | Longest simple prompt RPC duration, or zero. |
| `ext.init.step.count` | Summary | Number of step attempts started in this command. |
| `ext.init.step.dropped_count` | Summary | Completed steps exceeding the local buffer limit; does not count host rejection or reporting/ingestion losses. |

### Timing boundaries

- Go `time.Time` subtraction retains its monotonic clock component. A command's
  own telemetry reporting is outside its step timers.
- Simple `Prompt`, `Select`, `Confirm`, and `MultiSelect` calls are measured from
  immediately before the RPC to immediately after it returns. Catalog loading
  before the call and validation after submission are separate operations.
  RPC and terminal-rendering overhead cannot be removed by an extension.
- **Composite host pickers are external, not pure user wait.**
  `PromptSubscription`, `PromptLocation`, `PromptAiModel`, `PromptAiDeployment`,
  and `PromptAiModelLocationWithQuota` may fetch data, show multiple questions,
  validate values, or auto-resolve without displaying a question. Their
  `composite_prompt_rpc` duration is recorded, but is excluded from the simple
  prompt summary. A long composite picker is actionable evidence, not proof that
  the backend or the user caused the delay.
- `--no-prompt` creates no `prompt` records. Any simple prompt RPC that resolves
  a default or returns a prompt-required error in that mode is
  `execution` / `prompt_resolution_rpc`; composite calls remain `external`.
- A successfully submitted answer is a successful prompt RPC, even if a
  subsequent local validator rejects it. The validator records an `error`
  execution step; another question produces another prompt sequence.
- Authentication and delegated processes can include user interaction. Do not
  interpret every external duration as backend latency.
- Local work is recorded at business-operation boundaries, not every filesystem
  syscall. An external operation can include small amounts of local parsing or
  serialization. Nested downloads and Workflow spans can overlap; do not infer
  Init duration by summing all step rows.

### Covered business steps

Conditional branches and cache hits are not turned into synthetic user waits.
Coverage includes:

| Area | Recorded step IDs |
|---|---|
| Initial choices and identity | `input_validation`, `init_mode_selection`, `template_language_selection`, `template_selection`, `agent_name_input`, `agent_description_input`, `agent_instructions_input`, `agent_name_conflict_confirmation`, `agent_name_conflict_resolution`, `project_reuse_confirmation` |
| Source and files | `manifest_loading`, `manifest_validation`, `template_catalog_loading`, `template_download`, `file_generation`, `infra_generation`, `post_init_validation` |
| Azure and project setup | `authentication`, `ai_service_capability_check`, `subscription_selection`, `tenant_lookup`, `location_selection`, `project_mode_selection`, `foundry_project_selection`, `foundry_project_name_input`, `foundry_project_discovery`, `foundry_project_lookup`, `project_scaffolding`, `project_authoring`, `project_configuration_loading`, `project_configuration_update`, `environment_configuration` |
| Models | `model_strategy_selection`, `model_selection`, `model_alternative_selection`, `model_location_selection`, `model_location_mismatch_selection`, `model_deployment_selection`, `model_deployment_name_input`, `model_catalog_loading`, `model_deployment_discovery`, `model_deployment_resolution`, `model_deployment_authoring` |
| Deployment and supporting resources | `deploy_mode_selection`, `runtime_selection`, `entry_point_input`, `dependency_resolution_selection`, `protocol_selection`, `startup_command_input`, `resource_collision_selection`, `resource_name_input`, `environment_value_input`, `project_connection_discovery`, `acr_connection_input`, `acr_connection_selection`, `app_insights_resource_input`, `app_insights_connection_input`, `app_insights_connection_selection`, `registry_connection_validation`, `rai_policy_selection`, `rai_policy_discovery` |

## Reporting, privacy, and limits

Each command buffers up to 96 completed step records. Its prompt summary still
includes completed prompt attempts beyond that limit, and the local dropped
count is reported explicitly. The summary is sent first; step records follow
in start order. One shared one-second deadline bounds the entire batch. The
reporting context preserves trace metadata but ignores business cancellation,
so failure and cancellation can still produce a summary.

Reporting does not retry or replace the business error. Diagnostics include only
fixed messages, event names, counts, and gRPC status codes. The telemetry opt-out,
official-registry policy, and shared 100-event azd invocation budget still apply.
Multiple delegated commands and existing usage events consume that same budget.
Reporting timeouts, host rejection, crashes, and ingestion losses can result in
missing rows even when the local dropped count is zero. Do not treat an absent
step as evidence that it did not execute. Failures before the Cobra handler and
forced process termination cannot be captured by the command's completion handler.

No answers, question text, credentials, URLs, paths, resource IDs, resource
names, model names, files, or error messages are recorded. A privacy review of
the new events and field declarations is still required before release.

The timer and prompt decorator are mirrored under each extension's
`internal/inittelemetry`: the extensions are independently released Go modules.
The agents test suite checks byte-for-byte alignment of the implementation and
shared tests, without introducing a new dependency or editing the core SDK.

## Example KQL

These examples use the existing Application Insights `requests` shape, not a new
ingestion function. Adapt aliases for cooked data and select verified host and
extension release cohorts. They have not been run against production data.

### Per-step P75 / P90

```kusto
requests
| where timestamp >= ago(7d)
| where name == "ext.usage"
| where tostring(customDimensions["extension.event"]) in
    ("agent.init.step", "project.add.step", "project.deployment.add.step")
| where tostring(customDimensions["ext.init.flow.version"]) == "1"
| extend scope = tostring(customDimensions["ext.init.flow.scope"]),
         step = tostring(customDimensions["ext.init.step.id"]),
         category = tostring(customDimensions["ext.init.step.category"]),
         boundary = tostring(customDimensions["ext.init.step.timing_scope"]),
         outcome = tostring(customDimensions["ext.init.step.outcome"]),
         elapsedMs = todouble(customDimensions["ext.duration_ms"])
| where isnotnull(elapsedMs)
| summarize attempts=count(), percentiles(elapsedMs, 75, 90)
    by scope, step, category, boundary, outcome
```

To analyze only project authoring belonging to Init, first join the step rows
to the existing Init completion records on `operation_Id`. Project events can
also arise from standalone project commands. If a trace contains multiple Init
completions, retain an ambiguous-attribution bucket rather than duplicating the
same step across every completion.

### Init duration alongside simple prompt wait

```kusto
let Init = requests
| where timestamp >= ago(7d)
| where name == "ext.run"
| where tostring(customDimensions["cmd.entry"]) == "cmd.ai.agent.init"
| where tostring(customDimensions["extension.id"]) == "azure.ai.agents"
| extend initMs = duration / 1ms;
let Summaries = requests
| where timestamp >= ago(7d)
| where name == "ext.usage"
| where tostring(customDimensions["extension.event"]) == "agent.init.summary"
| where tostring(customDimensions["ext.init.flow.version"]) == "1"
| project operation_Id,
    promptMs=todouble(customDimensions["ext.init.prompt.total_duration_ms"]),
    longestPrompt=tostring(customDimensions["ext.init.prompt.longest.id"]);
Init
| join kind=leftouter Summaries on operation_Id
| project operation_Id, success, initMs, promptMs, longestPrompt
```

The second query assumes one Init completion and summary per trace; check that
assumption before using it in a dashboard. `promptMs` is only the agents
command's simple prompt waits. Projects summaries are separate and exclude
composite picker waits too. Do not label `initMs - promptMs` as backend time.

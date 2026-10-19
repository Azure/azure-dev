# Init step timings

<!-- cspell:ignore todouble tostring percentiles -->

`azd ai agent init` reports how long each business step took. Each step includes
all of its work: data loading, user interaction, validation, and retries. No
attempt is made to separate user waiting from model loading or backend latency.

## Event and fields

The existing extension telemetry API records an `ext.usage` span with
`extension.event=agent.init.step` and three string properties:

| Property | Meaning |
|---|---|
| `ext.init.step.id` | Fixed step identifier, such as `template_selection`, `foundry_project_selection`, or `model_selection`. |
| `ext.init.step.sequence` | Step start order within this Init invocation, beginning at 1. Repeated calls have the same ID and different sequence numbers. |
| `ext.duration_ms` | Monotonic elapsed time in milliseconds, with three decimal places. |

There are no categories, input sources, per-step outcomes, or prompt summaries.
Success and failure remain on the existing Init completion record. The usage
span's native duration measures reporting, not the business step; queries must
use `ext.duration_ms`.

Steps cover input validation, authentication, init mode, template selection and
loading/staging, project scaffolding, agent configuration, deploy mode, code and
protocol configuration, subscription/location/project/model selection, environment
configuration, RAI policy configuration, project updates, and file/infra generation.
Steps are measured at existing business-function boundaries, not per function
or filesystem operation. A selection that retries internally produces one timing
including those retries. A skipped branch produces no timing.

The Init call into `azure.ai.projects` is timed as `project_authoring` or
`model_deployment_authoring`. No internal or standalone project-command telemetry
is added. The SDK preserves the trace context, so step rows correlate with Init
through `operation_Id`. Count Init samples from the original completion records,
not from these step rows.

Some business steps contain smaller steps. Their durations are inclusive and
must not be added together to calculate total Init duration. `--no-prompt`
measures only the code that executes, without adding synthetic user-wait records.

## Reliability and privacy

Timings are buffered in memory and sent on return, including error and cancellation
paths. One second bounds the entire reporting batch. Reporting uses the existing
best-effort reporter; it does not retry or replace the command's result. It makes
no extra Azure, model, project, or configuration queries.

Each Init buffers at most 96 steps, leaving room under the host's shared
100-event invocation budget for existing events. Exceeding the local limit or
reporting deadline produces a fixed debug diagnostic. The host budget, telemetry
opt-out, nonofficial installation, process termination, or ingestion failures can
still cause missing rows; absence is not proof that a step did not execute.

Only fixed step IDs, sequence numbers, and durations are reported. No question
text, answers, paths, URLs, credentials, file contents, or resource names are sent.
Fields are un-hashed `SystemMetadata` / `PerformanceAndHealth` string properties,
not native measurements. New telemetry requires privacy review before release.

## Per-step P75 / P90

```kusto
requests
| where timestamp >= ago(7d)
| where name == "ext.usage"
| where tostring(customDimensions["extension.id"]) == "azure.ai.agents"
| where tostring(customDimensions["extension.event"]) == "agent.init.step"
| extend step = tostring(customDimensions["ext.init.step.id"]),
         elapsedMs = todouble(customDimensions["ext.duration_ms"])
| summarize samples=count(), percentiles(elapsedMs, 75, 90) by step
```

Select a verified extension-version cohort when comparing releases. This example
uses the Application Insights requests shape; adapt aliases for cooked data.

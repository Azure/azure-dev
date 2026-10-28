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
| `ext.agent.init.step.id` | Fixed step identifier, such as `template_selection`, `foundry_project_selection`, or `model_selection`. |
| `ext.agent.init.step.sequence` | Step start order within this Init invocation, beginning at 1. Repeated calls have the same ID and different sequence numbers. |
| `ext.agent.init.step.duration_ms` | Monotonic elapsed time in milliseconds, with three decimal places. |

There are no categories, input sources, per-step outcomes, or prompt summaries.
Success and failure remain on the existing Init completion record. The usage
span's native duration measures reporting, not the business step; queries must
use `ext.agent.init.step.duration_ms`.

Steps cover input validation, authentication, init mode, template selection and
loading/staging, project scaffolding, agent configuration, deploy mode, code and
protocol configuration, subscription/location/project/model selection, environment
configuration, RAI policy configuration, project updates, and file/infra generation.
Steps are measured at existing business-function boundaries, not per function
or filesystem operation. A selection that retries internally produces one timing
including those retries. A skipped branch produces no timing.

`template_loading` covers the initial read and validation of both local explicit
templates and remote inputs; cached local bytes are not loaded or timed again.
`project_scaffolding` covers core project initialization for both adopted templates
and generated projects; an existing project emits no scaffolding sample, and
delegation to another Init helper does not create a second sample.
`location_selection` covers supported-region loading (including fallback),
configured-location validation, any prompt, and environment persistence. Direct
callers that already supply allowed locations receive one sample without loading
the region catalog.

`input_validation` records separate validation groups as they execute, including
branch-specific checks after authentication; no validation timer spans authentication.
`init_mode_selection` also covers the decision to reuse existing project agents,
including the automatic `--no-prompt` decision, but not subsequent environment work.
Generated voice agents use the same inclusive `agent_configuration` step as the
other generated-agent paths. `infra_generation` starts only when ejection runs,
after project/Foundry skip checks and environment loading, on both standalone and
post-Init paths.

The Init call into `azure.ai.projects` is timed as `project_authoring` or
`model_deployment_authoring`. No internal or standalone project-command telemetry
is added. The SDK preserves the trace context, so step rows correlate with Init
through `operation_Id`. Count Init samples from the original completion records,
not from these step rows.

Some business steps contain smaller steps. Their durations are inclusive and
must not be added together to calculate total Init duration. `--no-prompt`
measures only the code that executes, without adding synthetic user-wait records.

## Reliability and privacy

Timings are buffered in memory. Successful Init reports existing agent contexts
and operation markers in the root post-run hook before flushing timings, so the
new timing events cannot consume their remaining host budget first. Errors and
cancellations flush on return because the success post-run hook does not run.
One second bounds the entire reporting batch. Reporting uses the existing
best-effort reporter; it does not retry or replace the command's result. It makes
no extra Azure, model, project, or configuration queries.

Each Init buffers at most 96 steps. Existing post-run events have priority under
the host's shared 100-event invocation budget; timing events use the remaining
slots and some may be dropped. Exceeding the local limit or
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
| extend step = tostring(customDimensions["ext.agent.init.step.id"]),
         elapsedMs = todouble(customDimensions["ext.agent.init.step.duration_ms"])
| summarize samples=count(), percentiles(elapsedMs, 75, 90) by step
```

Select a verified extension-version cohort when comparing releases. This example
uses the Application Insights requests shape; adapt aliases for cooked data.

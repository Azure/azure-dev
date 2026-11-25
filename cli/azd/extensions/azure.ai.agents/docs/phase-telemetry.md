# Project authoring and provider timing telemetry

<!-- cspell:ignore todouble tostring leftouter RFC3339Nano -->

This first version extends the same simple business-step timing approach as
[Init telemetry](init-telemetry.md), without changing its event or three fields.
It adds actual operation times and an opaque attempt ID to distinguish multiple
provider invocations in one trace. It does not modify azd core or the formal
journey/E2E queries.

## Observed operations

| Extension | Phase name | Scope / exact boundary |
|---|---|---|
| `azure.ai.projects` | `project_authoring` | `command`: project add handler entry through action return. Includes calls delegated from Init and direct `azd ai project add`. |
| `azure.ai.projects` | `model_deployment_authoring` | `command`: project deployment add handler entry through action return. A nested project-add action stays in this attempt. |
| `azure.ai.projects` | `provision` | `provider`: `FoundryProvisioningProvider.Deploy` entry through return, including reuse, failure, and cancellation. |
| `azure.ai.agents` | `package`, `publish`, `deploy` | `provider`: the corresponding `AgentServiceTargetProvider` method entry through return, including preview validation and intentional no-op branches. |

The provider methods are shared by `azd up` and standalone `azd provision` /
`azd deploy`. There is no separate `up` event or alternate instrumentation path.
A provider completion does not establish that core's entire command or its
subsequent hooks succeeded. Initialization, core work outside the methods, and
other extensions are not included in these provider intervals.

Project authoring is measured **inside the projects extension**, not by refining
the Init query. Existing Init `project_authoring` / `model_deployment_authoring`
steps still include the delegated command as a whole. They contain, and must not
be summed with, the projects command's phase and internal timings.

## Events and fields

Both events use the existing `ext.usage` channel, with `extension.event` equal to
`agent.phase.completed` or `agent.phase.step`. The host supplies extension ID,
extension version, and trace correlation. All new properties are string encoded,
un-hashed `SystemMetadata` / `PerformanceAndHealth`, with endpoint `N/A`.
They are not native measurements.

| Final property | Event | Meaning |
|---|---|---|
| `ext.agent.phase.name` | Both | Fixed phase name from the table above. |
| `ext.agent.phase.scope` | Both | `command` or `provider`; neither is a whole core command. |
| `ext.agent.phase.attempt_id` | Both | Random, opaque ID generated for every actual command/provider invocation. Contains no project, service, or layer identifier. |
| `ext.agent.phase.start_time` | Completed | Actual UTC start, RFC3339Nano. |
| `ext.agent.phase.end_time` | Completed | Actual UTC return, captured before telemetry reporting. |
| `ext.agent.phase.duration_ms` | Completed | Inclusive monotonic elapsed milliseconds, with three decimals. |
| `ext.agent.phase.outcome` | Completed | `success`, `error`, `canceled`, `timeout`, from the operation's returned error. |
| `ext.agent.phase.step.dropped_count` | Completed | Steps exceeding the local 24-step limit; **not** host rejection, reporting loss, or overall completeness. |
| `ext.agent.phase.step.id` | Step | Fixed business identifier from the coverage table below. |
| `ext.agent.phase.step.sequence` | Step | Start order within one attempt, beginning at 1. |
| `ext.agent.phase.step.start_offset_ms` | Step | Monotonic offset from the phase start. |
| `ext.agent.phase.step.duration_ms` | Step | Inclusive business-step elapsed milliseconds, with three decimals. |

There are no started events, lifecycle pre/post matching, entity hashes, parent
attempt matching across processes, step outcomes/categories, retry/429 counters,
or diagnostic summaries in this first version.

## Internal step boundaries

Steps include loading, interaction, validation, and retries within their business
operation. Their intervals can nest and overlap; they are not an additive
breakdown of the parent duration. Repeated calls retain the same ID with a new
sequence number. Cached credential/context guards do not add a synthetic wait.

| Area | Step IDs / boundaries |
|---|---|
| Project resolution | `project_resolution`: ensure/select the project, discover its service, or resolve the target. Target resolution can include nested choices and Azure queries. |
| Subscription/location | `subscription_selection`: resolve subscription and user-access tenant. `location_selection`: project location resolution, including its picker and validation, or the deployment-add `PromptLocation` call when a location is missing. Deployment picker timing closes before response validation and subsequent authoring, including on picker error/cancellation; cached/headless deployment paths add no picker timing. Automatically resolved project values remain part of project resolution/configuration. |
| Azure lookups | `azure_query`: project enumeration/lookup, deployment candidate resolution, or live deployment enumeration. Explicit project resource IDs are validated before lookup timing; malformed IDs produce no query sample. |
| Deployment checks | `deployment_reconciliation`: compare declared/live deployments and select candidates. Adopted reconciliation starts only for an existing-ID target with a resource ID; new-project, endpoint-only, missing-ID, and nil targets skip it without a sample. A successful no-change result after eligibility checks means reconciliation succeeded, not that a deployment was submitted. |
| Project persistence | `configuration_save`: compare/reconcile project service, environment, or deployment configuration, including any existing no-change return and internal rollback. |
| Agent context | `validation`: project command inputs or provider gating/definition checks. `deploy_context_resolution`: uncached target/context resolution. `configuration_loading`: environment values. `credential_acquisition`: tenant/credential setup, not proof of a token network request. |
| Provision | `template_resolution`, `template_compilation`: select parameters/template; compile only when on-disk Bicep requires it. `resource_lookup`: supporting resource-group check. `resource_deployment_submit`: existing ARM submission call. `resource_deployment_wait`: existing LRO wait, including service execution, network and polling. |
| Package/publish | `code_packaging`, `image_build`, `image_packaging`, `image_push`: the existing code or container operations that actually execute. Core-owned work outside these calls is not observed. |
| Agent deployment | `dependency_validation`: hosted dependency checks or prompt dependency graph resolution. `agent_create_or_update`: hosted/container, prompt, or voice creation/version operations. `artifact_upload`: only the actual `CreateAgentFromZip` / `UpdateAgentFromZip` call, including a failed call; lookup errors, activity-profile validation and other preflight work are excluded. `agent_activation_wait`: hosted/prompt activation polling. `endpoint_update`: existing agent environment/endpoint persistence. `voice_telephony_configuration`: existing voice binding reconciliation. |

Poll waits and credential setup may contain network, cached work, or user
interaction. SDK-hidden retry counts are unknown, not zero. Voice activation
behavior remains in its existing creation/binding operations; there is no new
polling loop or retry policy.

## Correlation, capacity, and limitations

- Join by operation ID, extension ID, and attempt ID. Step sequence is local to
  the attempt. Separate provision/deploy commands can have different operation
  IDs; these fields do not join their cross-command journey.
- Calculate step start as phase start plus the offset. Do not use the delayed
  usage event timestamp or its native duration as the business time.
- Completions are sent before their buffered steps with one shared one-second
  reporting deadline per command/provider return. Reporting does not retry,
  replace business errors, or make additional Azure/auth/config queries.
- The host's shared 100-event invocation budget still applies across extensions
  and services. Local clipping is explicit, but transport/host losses are not
  knowable from the completion alone. Multiple services may exhaust the budget;
  absence must remain unknown, not zero or skipped.
- A forced process exit has no completion event. This version does not detect
  started-only operations or repair synthetic Provision/Deploy timing in core.
- Existing command completion spans remain authoritative for command counts,
  success rates, and whole-command duration. Provider-only observations must not
  replace the existing E2E metric without a separate coverage/query review.
- No answers, question text, configuration contents, paths, names, tenant or
  subscription IDs, URLs, credentials, or error messages are emitted.

The small recorder is mirrored in the two independently released extension
modules, using only the existing SDK reporter and standard library. No shared
SDK or new Go module is introduced.

## Example payloads

The following are properties inside an `ext.usage` record. Times and IDs are
illustrative; the ID is generated anew for each invocation.

```json
{
  "extension.event": "agent.phase.completed",
  "ext.agent.phase.name": "provision",
  "ext.agent.phase.scope": "provider",
  "ext.agent.phase.attempt_id": "opaque-random-attempt",
  "ext.agent.phase.start_time": "2026-11-02T05:00:00Z",
  "ext.agent.phase.end_time": "2026-11-02T05:02:00Z",
  "ext.agent.phase.duration_ms": "120000.000",
  "ext.agent.phase.outcome": "success",
  "ext.agent.phase.step.dropped_count": "0"
}
```

```json
{
  "extension.event": "agent.phase.completed",
  "ext.agent.phase.name": "deploy",
  "ext.agent.phase.scope": "provider",
  "ext.agent.phase.attempt_id": "another-opaque-attempt",
  "ext.agent.phase.start_time": "2026-11-02T05:02:00Z",
  "ext.agent.phase.end_time": "2026-11-02T05:02:03Z",
  "ext.agent.phase.duration_ms": "3000.000",
  "ext.agent.phase.outcome": "error",
  "ext.agent.phase.step.dropped_count": "0"
}
```

## Inventory, approval coverage, and rollout

The shared fields registered in
[#10810](https://github.com/Azure/azure-dev/pull/10810) are emitted by this
implementation. The [public reference](../../../../../docs/reference/telemetry-data.md),
[registered schema](../../../../../docs/specs/metrics-audit/telemetry-schema.md#reserved-project-and-provider-phase-timing-contract),
and [feature matrix](../../../../../docs/specs/metrics-audit/feature-telemetry-matrix.md)
retain the prerequisite's pending-emission status. Updating these shared
inventories is a separate core-review follow-up, outside this extension-only PR.
Implemented source coverage is separate from release and production adoption.

Approval coverage (maintainer): **pending assessment**. The
[privacy review checklist](../../../../../docs/specs/metrics-audit/privacy-review-checklist.md)
defines the process; the PR description records sources, handling, examples,
ownership, and the required telemetry checklist. An azd maintainer must record whether existing approval
covers this data and handling, or coordinate additional approval if needed.
No completed signoff is claimed; collection beyond approved scope waits for the
required approval.

The author owns source/tests/inventories. Maintainers and release owners own
approval coverage and metadata/artifact rollout; the query owner coordinates
production query/table/report validation. Existing command counts and duration
queries remain unchanged. These downstream and release checks are separate
statuses, not evidence inferred from code completion.

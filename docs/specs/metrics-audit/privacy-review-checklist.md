# Privacy review checklist

Use this checklist when adding, changing, or removing azd telemetry, including first-party extension telemetry.

## Start here

1. **Name the question you want to answer.** Prefer a count, duration, boolean, or small set of code-defined values. For example, record `build` or `deploy` rather than a customer's step name.
2. **Show what will be sent.** List the affected events and fields, their value sources, and before/after examples. Use made-up values. Include custom inputs, errors, and invalid inputs, not just defaults.
3. **Add the PR template below.** Ask an azd maintainer to confirm approval coverage. Leave that decision pending until they respond.

## Who does the privacy review?

The PR author describes the collection and completes the code, documentation, and tests.
An azd maintainer checks it against the service's existing approved collection and handling, and records the coverage decision in the PR.
If additional approval is needed or coverage is unclear, the maintainer works with the service's privacy owner and privacy reviewers to obtain the required approval through the service's privacy process.
A short summary in the PR is enough. Internal approval documents stay out of public PRs.

## Check whether existing approval covers your change

Existing approval is the service's approved description of what data may be collected and how it is used and handled.
Maintainers check that record against the change described in the PR.

| Coverage decision | What happens next |
|---|---|
| **Pending maintainer assessment** | The author supplies the changed values and purpose; the maintainer checks coverage. |
| **Covered by existing approval** | The maintainer records why the approved data, purpose, sources, destinations, and handling cover the change. A separate formal review is not required solely because a field or event is new. |
| **Additional review required** | The maintainer coordinates with the privacy owner and records the approval or remaining work. Collection beyond the approved scope waits for the required approval. |

Apply this check to changed values and sources, not just new keys.
A new enum value can reveal different customer activity, and an unchanged key can start accepting customer content.
Bug fixes, removals, renames, hashing changes, and API or dependency migrations need a check of the actual payload and handling changes, but do not automatically require a separate formal review.

## Keep values small and safe

**Avoid CustomerContent telemetry.** Data classified as `CustomerContent` is dropped during GDPR processing, so it cannot provide the intended insights.
Use counts, booleans, or fixed categories instead. Hashing does not change the classification.

- Do not add credentials, prompts, file contents, raw error messages, or free-form input, even as hashes.
- Apply validation and omission rules on every recording path. An invalid input must not reach telemetry just because the command later fails.
- Explain what a value means and which inputs produce it. Being hashed, editable, private, or usually harmless does not settle its classification.
- Keep existing opt-out behavior unchanged. Do not add an opt-out counter or another reporting path when telemetry is disabled.

If a name or identifier is needed, explain why a count or fixed category would not answer the question.
Have a maintainer help confirm the classification and identifier-handling metadata.

## PR checklist template for telemetry changes

Copy this into a telemetry PR. Mark inapplicable work with a reason, and assign an owner to work outside the PR.

```markdown
### Telemetry change

What changed: <events/fields, value sources, and before/after examples>
Why it is needed: <the product or reliability question it answers>
Approval coverage (maintainer): pending assessment

- [ ] Values are limited to what is needed; custom and invalid inputs are handled.
- [ ] Field/event declarations, classification, purpose, identifier metadata, hashing rules, and affected telemetry docs are updated.
- [ ] Focused tests check emitted and excluded values, including hashes where relevant. Opt-out behavior is unchanged.
- [ ] Known affected queries, tables, reports, or exports are listed, with an owner for follow-up or migration.
- [ ] Code changes, metadata deployment, and downstream checks have separate statuses and owners.

Open questions or follow-ups: <none, or the question, owner, and link>
```

The maintainer replaces the pending coverage line with their decision and a short explanation or a link to their PR comment.
For first-party extension `ReportUsage` changes, run `go test ./extensions/telemetry`.

## For reviewers

Review the changed values alongside the maintainer's coverage decision.

- If coverage is missing or pending, ask the maintainer for an assessment before requesting a separate formal review.
- A new field, event, or bounded value can fit existing approval. Look for changes in the data, purpose, or handling, including values sent through existing `ReportUsage` calls.
- Explain concrete issues with the affected field and code path: customer content, secrets, other unexpected values, incorrect metadata, missing validation, or collection outside the approved scope.
- For a classification correction, check that the label matches the values and that a maintainer has assigned the metadata rollout. Assess that separately from a new collection path.

Automated reviewers should build on the recorded coverage decision and revisit it when the payload or evidence changes.
For GitHub Copilot code review, [repository instructions come from the PR's head branch](https://docs.github.com/en/copilot/how-tos/copilot-on-github/customize-copilot/add-custom-instructions/add-repository-instructions#enabling-or-disabling-custom-instructions-for-copilot-code-review) when custom instructions are enabled.
After updating those instructions, request a new review rather than treating an old comment as the current rule.

## Recorded scope for `azd env unset`

For Azure/azure-dev#9431, `cmd.env.unset` reuses the existing command span and adds only the following fields:

| Field | Classification / purpose | Proposed handling |
|-------|--------------------------|-------------------|
| `env.unset.outcome` | SystemMetadata / FeatureInsight | Fixed enum: `success`, `no_change`, `user_declined`, `failed`; not hashed |
| `env.unset.removed_count` | SystemMetadata / FeatureInsight | Numeric measurement of distinct keys removed by a fully successful save; not hashed |
| `env.unset.missing_count` | SystemMetadata / FeatureInsight | Numeric measurement of distinct requested keys observed missing; not hashed |

No key names, stored values, or raw environment identifiers are added to telemetry.
The fields are attached directly to the command span rather than the process-global usage bag.
Existing global command metadata and telemetry opt-out behavior are unchanged.
Help and flag-parser failures do not execute the action; action-level validation failures record `failed` with zero counts.
A maintainer must assess approval coverage before rollout. This records the proposed scope, not completed approval.

## Recorded scope for project and provider phase timings

For [Azure/azure-dev#10811](https://github.com/Azure/azure-dev/pull/10811), the
projects and agents extensions report `agent.phase.completed` and
`agent.phase.step` through the existing `ext.usage` / `ReportUsage` channel.
The question is which internal project-authoring or extension provider operation
accounts for long Init, provision, package, publish, or deploy elapsed time.

Approval coverage (maintainer): **pending assessment**. This records the author's
collection scope, not a completed privacy signoff. A general code approval or the
merged field-registration PR is not an explicit approval-coverage decision.
An azd maintainer must record the decision and rationale in #10811. Collection
beyond approved scope waits for any required additional approval; internal
approval documents must not be attached to the public PR.

| Values | Sources and proposed handling |
|---|---|
| Phase name, scope, outcome, step ID | Code-defined phase/scope/step constants and outcome classification of the returned error or an observed polling timeout. No raw error text; invalid input does not become an attribute value. |
| Attempt ID | New opaque random string per actual invocation, not a hash or copy of a customer, service, project, subscription, or resource identifier. Used only to distinguish attempts within an operation/extension. |
| UTC start/end | Local clock at method/handler entry and return, before reporting; string-encoded RFC3339Nano. |
| Duration, step offset/duration | Monotonic clock differences, string-encoded nonnegative milliseconds with three decimals; inclusive, not additive. |
| Sequence, dropped count | Local bookkeeping only: string-encoded step start order 1-24 and nonnegative count of starts exceeding the local buffer. Not transport loss or customer entity counts. |

All twelve keys remain un-hashed `SystemMetadata` / `PerformanceAndHealth`,
endpoint `N/A`, `IsMeasurement=false`. The
[schema](telemetry-schema.md#project-and-provider-phase-timing-contract) lists
the exact field/event maps and allowed values. The
[extension guide](../../../cli/azd/extensions/azure.ai.agents/docs/phase-telemetry.md#example-payloads)
contains made-up success/error payloads.

Before: Init's existing step events measured delegated authoring as a whole, with
no phase completion/step records from the projects handlers or these provider
methods. After: a custom-named service may produce `phase.name="deploy"`,
`phase.scope="provider"`, and `step.id="agent_create_or_update"`, never its name.
Invalid authoring inputs can produce `phase.outcome="error"` and
`step.id="validation"`, but omit their values and any later unexecuted steps.
Picker cancellation produces `phase.outcome="canceled"` with the completed fixed
`location_selection` step, never the question or selected region text. Headless
or cached deployment locations produce no picker step. All example properties
are string values, not new command samples or native measurements.

Opt-out, official-registry eligibility, host identity/correlation, reporting
destination, and the shared 100-event budget are unchanged. This change does not
add an exporter, Azure query, credential request, retry, or customer-content path.
Steps are locally capped at 24 per attempt and reporting has one shared
one-second deadline. An opaque attempt ID is generated, not a customer hash, so
no new customer-identifier hashing rule is introduced.

| Work / status | Owner |
|---|---|
| Code, emitted/excluded-value regressions, and all shared inventories: included in #10811 | PR author |
| Shared field registration: merged in #10810; deployed metadata coverage not inferred from that merge | azd maintainers / metadata release owner |
| Approval coverage: pending assessment; no signoff recorded | azd maintainer, with the service privacy owner if coverage is unclear |
| Agent/projects artifact publication and version/eligibility validation: not established by this PR | Extension release owners |
| Production Kusto/LENS Init and provision/deploy queries, derived tables/reports, retained copies and exports: no query changes in this PR; migration and coverage checks outstanding | Query/report owner designated by the author; coordinate with azd maintainers |

No named downstream function, table, or dashboard was supplied with this change.
The query/report owner must identify consumers before rollout. Continue counting
existing command records; phase/step rows must not inflate command counts or be
summed into a command duration.

## Related references

| Need | Reference |
|---|---|
| Coding examples and instrumentation steps | [Feature telemetry guide](../../guides/feature-telemetry.md) |
| Classification, identifier metadata, and hashing concepts | [Telemetry schema](telemetry-schema.md#privacy-concepts) |
| Field/event declarations and resource-attribute rules | [Declaration contracts](telemetry-schema.md#telemetry-metadata-declarations) |
| Query, table, report, retained-copy, and rollout checks | [Audit process](audit-process.md) |

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

## Related references

| Need | Reference |
|---|---|
| Coding examples and instrumentation steps | [Feature telemetry guide](../../guides/feature-telemetry.md) |
| Classification, identifier metadata, and hashing concepts | [Telemetry schema](telemetry-schema.md#privacy-concepts) |
| Field/event declarations and resource-attribute rules | [Declaration contracts](telemetry-schema.md#telemetry-metadata-declarations) |
| Query, table, report, retained-copy, and rollout checks | [Audit process](audit-process.md) |

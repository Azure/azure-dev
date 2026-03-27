# Telemetry Audit Process

This document defines the recurring audit process for `azd` telemetry, including cadence,
ownership, checklists, downstream validation, and automation.

## Quarterly Review Cadence

Telemetry audits run on a quarterly cycle aligned with fiscal quarters.

| Quarter | Audit Window | Report Due |
|---------|-------------|------------|
| Q1 | Weeks 1–2 of quarter | End of Week 3 |
| Q2 | Weeks 1–2 of quarter | End of Week 3 |
| Q3 | Weeks 1–2 of quarter | End of Week 3 |
| Q4 | Weeks 1–2 of quarter | End of Week 3 |

### Audit Phases

1. **Discovery** (Week 1) — Automated scan identifies new commands, changed telemetry fields,
   and coverage gaps.
2. **Review** (Week 2) — Manual review of scan results, privacy classification check, and
   downstream validation.
3. **Report** (Week 3) — Publish audit report, file issues for gaps, update documentation.

## Ownership

| Role | Responsibility |
|------|---------------|
| **Telemetry Lead** | Owns the audit process, runs scans, publishes reports |
| **Feature Developers** | Respond to gap issues, implement telemetry for new commands |
| **Privacy Contact** | Reviews new classifications, approves changes to hashing |
| **Data Engineering** | Validates downstream Kusto functions and cooked tables |
| **PM / Analytics** | Reviews audit report, prioritizes gap closures |

## Audit Checklist

### 1. Command Coverage Scan

- [ ] Run the command inventory scan against the current `main` branch
- [ ] Compare results with the [Feature-Telemetry Matrix](feature-telemetry-matrix.md)
- [ ] Identify new commands added since last audit
- [ ] Identify commands that had telemetry added since last audit
- [ ] Flag commands still missing command-specific telemetry

### 2. Field Inventory

- [ ] Diff `fields/fields.go` against the previous audit snapshot
- [ ] Identify new fields added without documentation
- [ ] Verify all fields have correct classification and purpose
- [ ] Verify hashing is applied to all user-provided values
- [ ] Cross-reference with the [Telemetry Schema](telemetry-schema.md)

### 3. Event Inventory

- [ ] Diff `events/events.go` against the previous audit snapshot
- [ ] Identify new events added without documentation
- [ ] Verify event naming follows conventions (`prefix.noun.verb`)

### 4. Privacy Review

- [ ] Review all new fields against the [Privacy Review Checklist](privacy-review-checklist.md)
- [ ] Confirm no `CustomerContent` is emitted
- [ ] Confirm no unhashed user-provided values
- [ ] Spot-check 5 random existing fields for classification accuracy

### 5. Disabled Telemetry Check

- [ ] Verify `version` still has `DisableTelemetry: true`
- [ ] Verify `telemetry upload` still has `DisableTelemetry: true`
- [ ] Check for any new commands with `DisableTelemetry: true` — confirm intent

### 6. Opt-Out Rate Estimation

When `AZURE_DEV_COLLECT_TELEMETRY=no`, the entire telemetry pipeline is disabled — no
spans are created and no data is sent. This means **opted-out users are invisible** in
telemetry data and we cannot directly measure the opt-out rate.

**Estimation approach** (indirect):

- [ ] Compare total install/download counts (from package manager stats, GitHub releases,
  winget/brew/apt download logs) against distinct active telemetry users in the same period
- [ ] Estimate: `opt-out rate ≈ 1 − (active telemetry users / total installs)`
- [ ] Track this ratio over time to detect trends

> **⚠️ Open question for @AngelosP / Privacy team**: Should azd send a single anonymous
> opt-out counter signal (containing zero identifying information — no machine ID, no IP,
> just an increment) when the user has `AZURE_DEV_COLLECT_TELEMETRY=no`? This is a gray
> area: GDPR Article 7(3) requires stopping processing on consent withdrawal, but a
> zero-identifier counter may not constitute "personal data." The .NET SDK installer does
> send a telemetry entry on successful installation even before the user sets the opt-out
> variable. A decision from the privacy team would clarify whether this approach is
> acceptable for azd.

### 7. Data Pipeline Health

- [ ] Verify telemetry upload process is functioning (check error rates)
- [ ] Confirm data arrives in Azure Monitor within expected latency
- [ ] Validate sample spans contain expected attributes

## Downstream Validation

### LENS Jobs

LENS jobs consume raw telemetry and produce aggregated metrics. Each audit must verify:

- [ ] All active LENS jobs are running without errors
- [ ] New fields referenced by LENS jobs exist in the telemetry stream
- [ ] Deprecated fields referenced by LENS jobs have been migrated or removed
- [ ] LENS job output matches expected schema

### Kusto Functions

Kusto functions parse and transform raw telemetry into queryable tables.

- [ ] All Kusto functions compile without errors
- [ ] New fields are extracted correctly (spot-check with sample data)
- [ ] Renamed or removed fields have been updated in function definitions
- [ ] Function output types match downstream dashboard expectations

### Cooked Tables

Cooked tables are pre-aggregated views used by dashboards and reports.

- [ ] Cooked table materialization is running on schedule
- [ ] New columns from new fields are populated correctly
- [ ] Historical data migration is complete (if field was renamed)
- [ ] Dashboard queries return expected results

## Automation Suggestions

### CI Scan: Telemetry Coverage Gate

Add a CI check that fails the build when a new command is added without telemetry instrumentation.

**Implementation approach:**

1. Write a Go analysis pass (or script) that:
   - Walks all `ActionDescriptor` registrations in `internal/cmd/`
   - Checks each for either `DisableTelemetry: true` or a `SetUsageAttributes` call
   - Reports commands that have neither

2. Add the check to the existing CI pipeline:
   ```yaml
   - name: Telemetry Coverage Check
     run: go run ./eng/scripts/telemetry-coverage-check/main.go
   ```

3. Allow exemptions via a `// telemetry:exempt <reason>` comment on the `ActionDescriptor`.

### GitHub Action: Quarterly Audit Issue

Automate the creation of a quarterly audit issue with the full checklist.

**Implementation approach:**

```yaml
name: Quarterly Telemetry Audit
on:
  schedule:
    # First Monday of each quarter (Jan, Apr, Jul, Oct)
    - cron: '0 9 1-7 1,4,7,10 1'

jobs:
  create-audit-issue:
    runs-on: ubuntu-latest
    steps:
      - uses: actions/checkout@v4

      - name: Create Audit Issue
        uses: actions/github-script@v7
        with:
          script: |
            const fs = require('fs');
            const checklist = fs.readFileSync(
              'docs/specs/metrics-audit/audit-process.md', 'utf8'
            );

            // Extract the checklist sections
            const quarter = Math.ceil((new Date().getMonth() + 1) / 3);
            const year = new Date().getFullYear();

            await github.rest.issues.create({
              owner: context.repo.owner,
              repo: context.repo.repo,
              title: `Telemetry Audit Q${quarter} ${year}`,
              body: `## Quarterly Telemetry Audit — Q${quarter} ${year}\n\n` +
                    `Audit window: Weeks 1–2\n` +
                    `Report due: End of Week 3\n\n` +
                    `### Checklist\n\n` +
                    `See [audit-process.md](docs/specs/metrics-audit/audit-process.md) for full details.\n\n` +
                    `- [ ] Command coverage scan\n` +
                    `- [ ] Field inventory\n` +
                    `- [ ] Event inventory\n` +
                    `- [ ] Privacy review\n` +
                    `- [ ] Disabled telemetry check\n` +
                    `- [ ] Data pipeline health\n` +
                    `- [ ] LENS job validation\n` +
                    `- [ ] Kusto function validation\n` +
                    `- [ ] Cooked table validation\n` +
                    `- [ ] Audit report published\n`,
              labels: ['telemetry', 'audit']
            });
```

### PR Label Automation

Automatically label PRs that modify telemetry files for review.

**Trigger files:**
- `cli/azd/internal/tracing/fields/fields.go`
- `cli/azd/internal/tracing/events/events.go`
- `cli/azd/internal/tracing/fields/key.go`
- `cli/azd/internal/tracing/resource/resource.go`
- Any file containing `SetUsageAttributes`

**Implementation:** Use a CODEOWNERS entry to require telemetry team review:

```
# .github/CODEOWNERS (telemetry-related files)
cli/azd/internal/tracing/ @AzureDevCLI/telemetry-reviewers
```

This is preferred over a separate GitHub Actions workflow because it integrates directly
with the existing PR review flow and requires no additional CI configuration.

## Telemetry Validation Pipeline

### 1. Local Validation

Use `--trace-log-file <path>` to dump all telemetry spans to a JSON file, then inspect for
expected attributes.

```bash
azd pipeline config --trace-log-file telemetry-dump.json
# Then inspect telemetry-dump.json for pipeline.provider, pipeline.auth.type fields
```

```bash
azd infra synth --trace-log-file telemetry-dump.json
# Inspect for infra.provider field
```

This flag is available on all azd commands and writes the full span tree (with all attributes)
to the specified file. Use `jq` or similar tools to filter for specific keys.

### 2. Functional Tests

The repo has existing functional telemetry tests at
`cli/azd/test/functional/telemetry_test.go` that run real commands and validate trace
attributes. New telemetry fields should be covered here.

When adding a new field, add a test case that:
1. Runs the command that emits the field.
2. Reads the trace output.
3. Asserts the expected attribute key and value are present.

### 3. PR Builds

Azure Pipelines publishes PR-specific builds via `eng/pipelines/release-cli.yml`. Install a
PR build with:

```bash
azd version install pr/<PRNumber>
```

Then manually test commands and inspect `--trace-log-file` output to verify the new telemetry
attributes are present with expected values.

### 4. Pre-Production Checklist

Before merging telemetry changes:

- [ ] Unit tests pass (`go test ./cmd/... ./internal/tracing/...`)
- [ ] Functional telemetry tests pass
- [ ] Local `--trace-log-file` validation for each new field
- [ ] PR build smoke test with real Azure subscription
- [ ] Dev telemetry endpoint receives expected attributes (non-prod builds auto-target dev App Insights)

### Telemetry Diff Report

Generate a diff report on every PR that modifies telemetry, showing:
- New fields added (with classification)
- Fields removed
- Classification changes
- New events

This can be implemented as a Go script that parses `fields.go` and `events.go` ASTs and
compares against the base branch.

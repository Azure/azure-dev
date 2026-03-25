# Privacy Review Checklist

This document defines when a privacy review is required for telemetry changes in `azd`,
the data classification framework, hashing requirements, and a PR checklist template.

## When to Trigger a Privacy Review

A privacy review **must** be triggered when any of the following conditions are met:

1. **New telemetry field** — Any new attribute key added to `fields/fields.go` or emitted
   via `SetUsageAttributes` / `tracing.SetSpanAttributes`.

2. **New event** — Any new event constant added to `events/events.go` or new span name
   introduced via `tracing.Start`.

3. **Classification change** — Any change to an existing field's `Classification` or `Purpose`.

4. **New data source** — Telemetry that captures data from a source not previously instrumented
   (e.g., a new Azure service response, user input, file system content).

5. **Hashing removal or weakening** — Any change that removes `StringHashed` / `StringSliceHashed`
   from a field that was previously hashed.

6. **Cross-boundary data flow** — Telemetry that propagates trace context to external processes
   (e.g., extension child processes) or receives context from external sources.

7. **Measurement → String conversion** — Changing a field from a numeric measurement to a
   string value (strings have higher re-identification risk).

A privacy review is **not** required for:

- Bug fixes to existing telemetry (e.g., fixing a typo in an attribute name).
- Removing telemetry fields entirely.
- Adding new values to an existing enum field (e.g., a new `auth.method` value) — unless
  the new value captures data from a new source.

## Raw Telemetry Data Shape Changes

> "Any time any of the incoming raw data changes, your team needs to review and understand
> what needs to change to keep calculating things correctly" — AngelosP

When the shape of raw telemetry data changes, ALL downstream consumers must be reviewed.
This is a **standalone mandatory checklist item** that applies whenever any of the following
occur:

- [ ] **Field renamed** — A telemetry attribute key is renamed (e.g., `auth.type` → `auth.method`).
      Review all Kusto functions, cooked table queries, LENS jobs, and dashboards that reference
      the old key name.
- [ ] **Field type changed** — A field changes from string to int, or from single-value to array,
      etc. Review all downstream parsers, `extend`/`project` statements in KQL, and any schema
      validations.
- [ ] **Allowed values changed** — An enum field gains, removes, or renames values (e.g.,
      `auth.method` adding `"logout"`). Review all `case`/`iff`/`countif` expressions in Kusto
      that filter or bucket by the old value set.
- [ ] **Field removed or deprecated** — A field is no longer emitted. Review all queries that
      reference it and add null-handling or migration logic.
- [ ] **Measurement semantics changed** — Units change (seconds → milliseconds), counting
      methodology changes, or aggregation expectations change. Review all KPI calculations,
      percentile computations, and alerting thresholds.
- [ ] **Hashing algorithm changed** — Hash function or salt changes break join-ability with
      historical data. Review all queries that correlate hashed fields across time ranges.

**Action required**: Before merging any PR that changes raw telemetry data shape, the author
must verify that all downstream Kusto functions and KPI calculations still compute correctly
with the new shape. This includes cooked tables, LENS jobs, dashboards, and alerts.

## Data Classifications

All telemetry fields must be assigned exactly one classification from the table below.
Classifications are defined in `internal/telemetry/fields/fields.go`.

| Classification | Description | Examples | Retention |
|----------------|-------------|----------|-----------|
| **PublicPersonalData** | Data the user has intentionally made public | GitHub username | Standard |
| **SystemMetadata** | Non-personal metadata about the system or environment | OS type, Go version, feature flags | Standard |
| **CallstackOrException** | Stack traces, panic details, error frames | `error.frame` | Reduced |
| **CustomerContent** | Content created by the user | File contents, messages | Highest restriction — avoid in telemetry |
| **EndUserPseudonymizedInformation** | User identifiers that have been pseudonymized | Hashed MAC address (`machine.id`), SQM User ID | Standard |
| **OrganizationalIdentifiableInformation** | Identifiers scoped to an organization | Azure subscription ID, tenant ID | Standard |

### Classification Decision Tree

```
Is the data created by the user (file content, messages)?
  └─ Yes → CustomerContent (do NOT emit in telemetry)
  └─ No →
      Can the data identify a specific person?
        └─ Yes →
            Is it already public?
              └─ Yes → PublicPersonalData
              └─ No →
                  Can it be hashed?
                    └─ Yes → Hash it → EndUserPseudonymizedInformation
                    └─ No → Do NOT emit — escalate to privacy team
        └─ No →
            Can it identify an organization?
              └─ Yes → OrganizationalIdentifiableInformation
              └─ No →
                  Is it a stack trace or exception detail?
                    └─ Yes → CallstackOrException
                    └─ No → SystemMetadata
```

## Hashing Requirements

Any field that could identify a user, project, or environment **must** be hashed before emission.

### Hash Functions

All hashing functions are in `internal/telemetry/fields/key.go`.

| Function | Signature | Behavior |
|----------|-----------|----------|
| `CaseInsensitiveHash` | `func CaseInsensitiveHash(value string) string` | Lowercases the input, then computes SHA-256. Returns hex-encoded digest. |
| `StringHashed` | `func StringHashed(key, value string) attribute.KeyValue` | Creates an OTel `attribute.KeyValue` with the value replaced by its case-insensitive SHA-256 hash. |
| `StringSliceHashed` | `func StringSliceHashed(key string, values []string) attribute.KeyValue` | Creates an OTel `attribute.KeyValue` where each element in the slice is independently hashed. |

### Fields That Must Be Hashed

| Field | Hash Function | Reason |
|-------|---------------|--------|
| `project.template.id` | `StringHashed` | Template IDs may contain repo URLs or user-chosen names |
| `project.template.version` | `StringHashed` | Version strings may be user-defined |
| `project.name` | `StringHashed` | Project names are user-chosen |
| `env.name` | `StringHashed` | Environment names may contain identifying information |

### When to Hash New Fields

A new field **must** be hashed if any of the following are true:

- The value is user-provided (typed by the user or read from a user-authored file).
- The value could contain a project name, repository URL, or path.
- The value could be used to correlate across users or organizations.

A new field should **not** be hashed if:

- The value is from a fixed enum (e.g., `auth.method` = `"browser"`).
- The value is a count or duration (measurements).
- The value is system-generated metadata (e.g., OS type).

## Data Catalog Classification Process

When adding a new telemetry field:

1. **Define the field** in `internal/telemetry/fields/fields.go` using the `NewKey` pattern.
2. **Assign classification** — use the decision tree above to determine the correct classification.
3. **Assign purpose** — select one or more from: `FeatureInsight`, `BusinessInsight`, `PerformanceAndHealth`.
4. **Determine hashing** — apply hashing rules above.
5. **Register in Data Catalog** — update the [Telemetry Schema](telemetry-schema.md) with:
   - OTel key name
   - Classification
   - Purpose
   - Whether it is hashed
   - Whether it is a measurement
   - Allowed values (if enum)
6. **Update LENS/Kusto** — if the field will be queried downstream, coordinate with the
   data engineering team to update Kusto functions and cooked tables.

## PR Checklist Template for Telemetry Changes

Copy this checklist into your PR description when making telemetry changes.

```markdown
## Telemetry Change Checklist

### New Fields
- [ ] Field defined in `fields/fields.go` with correct classification and purpose
- [ ] Field documented in `docs/specs/metrics-audit/telemetry-schema.md`
- [ ] Hashing applied where required (user-provided values, names, paths)
- [ ] Measurement fields use correct OTel type (Int64, Float64)
- [ ] Enum values documented with allowed value set

### New Events
- [ ] Event constant defined in `events/events.go`
- [ ] Event documented in `docs/specs/metrics-audit/telemetry-schema.md`
- [ ] Event follows naming convention (`prefix.noun.verb`)

### Privacy
- [ ] Classification assigned using decision tree
- [ ] No `CustomerContent` emitted in telemetry
- [ ] No unhashed user-provided values
- [ ] No PII in string attributes (names, emails, paths)
- [ ] Privacy review triggered (if required per triggers above)

### Testing
- [ ] Unit test verifies attributes are set on the span
- [ ] Integration test confirms end-to-end emission (if applicable)
- [ ] Verified field appears correctly in local telemetry output

### Downstream
- [ ] LENS job updated (if field is queried in dashboards)
- [ ] Kusto function updated (if field is used in cooked tables)
- [ ] Dashboard updated (if field powers a new metric)

### Documentation
- [ ] Feature-telemetry matrix updated (if gap is being closed)
- [ ] Telemetry schema updated with new field/event
- [ ] This checklist is complete
```

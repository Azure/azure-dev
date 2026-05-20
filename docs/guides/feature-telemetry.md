# Feature Telemetry Guide — Adding Telemetry to New Features

> End-to-end guide for instrumenting telemetry when building new azd features.
> Ensures telemetry is designed alongside the feature, not bolted on after.


## Why This Matters

Telemetry is not a separate system — it's part of the feature. When you ship a feature without telemetry:
- Product can't measure adoption or success
- Engineering can't diagnose failures in production
- Dashboards show gaps that require scrambling to fill later

This guide covers the instrumentation side — defining events, fields, and wiring them into your code.

> Microsoft-internal data pipelines (GDPR classification, Kusto functions, Power BI reports) are documented separately for internal maintainers.

## Step 1: Define Your Events

**File:** `cli/azd/internal/tracing/events/events.go`

Add a constant for your feature's event name(s). Follow the naming conventions:

| Pattern | When to Use | Example |
|---------|------------|---------|
| `cmd.<command.path>` | Automatically generated for commands | `cmd.deploy` (via `GetCommandEventName`) |
| `<domain>.<action>` | Non-command operations | `container.publish`, `hooks.exec` |
| `<domain>.<scope>` | Scoped events | `arm.deploy.subscription` |

```go
// In events.go — add your event constant
const (
    // MyFeatureEvent tracks the execution of the my-feature operation.
    MyFeatureEvent = "myfeature.execute"
)
```

> **Note:** Command events (`cmd.*`) are created automatically by the telemetry middleware via
> `events.GetCommandEventName(...)`. You only need to define explicit event constants for
> non-command operations (sub-spans, background work, etc.).

## Step 2: Define Your Fields

**File:** `cli/azd/internal/tracing/fields/fields.go`

Add `AttributeKey` variables for any new properties your feature emits. Every field must have:

1. **A key name** — descriptive, dot-separated, lowercase
2. **A classification** — what kind of data is this (see [Data Classifications](#data-classifications))
3. **A purpose** — why are we collecting it (see [Purposes](#purposes))
4. **`IsMeasurement: true`** if the value is numeric (goes to `Measurements` column, not `Properties`)

```go
// In fields.go — add your field keys
var (
    // The strategy used by my feature.
    MyFeatureStrategyKey = AttributeKey{
        Key:            attribute.Key("myfeature.strategy"),
        Classification: SystemMetadata,
        Purpose:        FeatureInsight,
    }

    // The number of items processed.
    MyFeatureItemCountKey = AttributeKey{
        Key:            attribute.Key("myfeature.item.count"),
        Classification: SystemMetadata,
        Purpose:        PerformanceAndHealth,
        IsMeasurement:  true,
    }
)
```

### Data Classifications

| Classification | Use When |
|----------------|----------|
| `SystemMetadata` | Non-personal system/config data (most common) |
| `EndUserPseudonymizedInformation` | User identifiers that are hashed (e.g., machine ID) |
| `OrganizationalIdentifiableInformation` | Org identifiers (subscription ID, tenant ID) |
| `PublicPersonalData` | Data the user made public |
| `CallstackOrException` | Stack traces or error details |
| `CustomerContent` | User-created content — highest sensitivity, avoid if possible |

### Purposes

| Purpose | Use When |
|---------|----------|
| `FeatureInsight` | Understanding feature adoption and usage patterns |
| `BusinessInsight` | Business metrics (users, orgs, growth) |
| `PerformanceAndHealth` | Performance, errors, reliability |

### Hashing Sensitive Values

If your field contains user-generated names or identifiers, **hash it**:

```go
// In your code, use StringHashed instead of regular attribute setting
tracing.SetUsageAttributes(
    fields.StringHashed(fields.MyFeatureNameKey, userProvidedName),
)
```

## Step 3: Instrument Your Code

### For Command Actions

The telemetry middleware (`cmd/middleware/telemetry.go`) automatically creates a span for every command. You just need to add your feature-specific attributes:

```go
func (a *myAction) Run(ctx context.Context) (*actions.ActionResult, error) {
    // Set usage attributes — these get attached to the command span
    tracing.SetUsageAttributes(
        fields.MyFeatureStrategyKey.String("incremental"),
        fields.MyFeatureItemCountKey.Int(len(items)),
    )

    // ... do work ...

    return &actions.ActionResult{...}, nil
}
```

### For Sub-Operations (Child Spans)

If your feature has distinct sub-operations worth tracking separately:

```go
func (s *myService) ProcessItems(ctx context.Context, items []Item) error {
    ctx, span := tracing.Start(ctx, events.MyFeatureEvent)
    defer span.End()

    // Set attributes on this span
    span.SetAttributes(
        fields.MyFeatureItemCountKey.Int(len(items)),
    )

    // ... do work ...

    if err != nil {
        span.SetStatus(codes.Error, err.Error())
        return err
    }

    return nil
}
```

### For Extension Commands

Extension commands automatically get `ext.run` events. To add structured error reporting:

```go
// Extensions report structured errors back to the host
return &azdext.ServiceError{
    Message:     fmt.Sprintf("deployment failed: %s", resp.Error.Message),
    ErrorCode:   fmt.Sprintf("deploy.%s", resp.Error.Code),
    StatusCode:  resp.StatusCode,
    ServiceName: "arm",
}
```

This maps to result codes like `ext.service.arm.500` in telemetry. See `cli/azd/pkg/azdext/extension_error.go` for the full structured error types.

## Step 4: Update the Telemetry Schema Doc

**File:** `docs/specs/metrics-audit/telemetry-schema.md`

Add your new events and fields to the canonical schema reference. This document is the source of truth for what telemetry azd collects and is reviewed during privacy audits.

## Step 5: Privacy Review

Every new field must have correct `Classification` and `Purpose` values:

- If your field has sensitivity higher than `SystemMetadata`, consult the [Privacy Review Checklist](../specs/metrics-audit/privacy-review-checklist.md)
- If you're adding `CustomerContent` or unhashed PII, a formal privacy review is required before merge

## Step 6: Product Review

Before merging your feature PR:

1. **Share the telemetry spec** with your PM — explain what events/fields you're adding and why
2. **Show sample queries** — demonstrate how the data answers product questions
3. **Update the data reference** — add your feature's fields to `docs/reference/telemetry-data.md`

This ensures product can provide feedback during development, not scramble after launch.

## Quick Reference: Where Things Live

| What | File/Path |
|------|-----------|
| Event name constants | `cli/azd/internal/tracing/events/events.go` |
| Field key definitions | `cli/azd/internal/tracing/fields/fields.go` |
| Hashing helpers | `cli/azd/internal/tracing/fields/key.go` |
| Telemetry middleware | `cli/azd/cmd/middleware/telemetry.go` |
| Telemetry pipeline init | `cli/azd/internal/telemetry/telemetry.go` |
| Error classification | `cli/azd/internal/cmd/errors.go` (MapError) |
| Canonical schema | `docs/specs/metrics-audit/telemetry-schema.md` |
| Privacy review checklist | `docs/specs/metrics-audit/privacy-review-checklist.md` |

## See Also

- [Architecture](../architecture/telemetry.md) — How the telemetry system works end-to-end
- [Data Reference](../reference/telemetry-data.md) — Complete schema, events, fields
- [Privacy Review Checklist](../specs/metrics-audit/privacy-review-checklist.md) — When to do privacy reviews

# Extension Telemetry

<!-- cspell:ignore tostring -->

This guide is for extension authors whose telemetry has passed privacy review
and whose extension is published to the official `azd` registry. It explains
how to ask `azd` to record a usage signal on the extension's behalf — for
example, which deployment mode a user picked.

> [!IMPORTANT]
> Any telemetry must go through a privacy review to ensure that it respects
> user privacy before you start collecting it. See [Your responsibility for
> content](#your-responsibility-for-content) below.

Telemetry is a service `azd` offers to extensions installed from the verified
official `azd` registry source. Call `ReportUsage` with an event name and the
attributes you care about. `azd` core owns the identity fields, namespaces your
attributes, and bounds their size and number. It does not inspect what they
mean.

The reserved `azd` source is eligible only when its name, source type, and
normalized URL match the official registry. This is a configuration-based
admission check, not a cryptographic provenance guarantee.

See [ADR-001](../../../../docs/architecture/adr-001-extension-telemetry-events.md)
for the reasoning behind this design.

## Require a host with telemetry support

A published extension version that depends on this service should declare the
first `azd` release that includes `TelemetryService` in its `extension.yaml`:

```yaml
requiredAzdVersion: ">=1.31.0"
```

Use the first released `azd` version containing the service as the lower bound.
`azd` uses this field while resolving extension installs and updates, so new
installations do not select the extension on an older host. See
[Extension Resolution and Versioning](./extension-resolution-and-versioning.md#azd-version-compatibility)
for the compatibility behavior.

This version requirement is the normal compatibility mechanism. Keep the RPC
call best-effort for already-installed versions and non-registry sources, which
may still run on an older host and receive `Unimplemented`.

## What `azd` records

| Attribute name | Attribute value |
|---|---|
| `extension.id` | ID of the current extension, added automatically |
| `extension.version` | Version of the current extension, added automatically |
| `extension.source` | Source of the current extension, added automatically |
| `extension.event` | Event name chosen by the extension |
| `ext.*` | Unstructured collection of extension-chosen keys and values |

The extension chooses the event name and every `ext.*` key and value. `azd`
enforces the `ext.` prefix, the bounds below, and the official-registry
requirement.

`azd` cannot write your attribute keys unprefixed, and you cannot overwrite the
identity fields — a key of `extension.id` is recorded as `ext.extension.id`.

## Reporting an event

```go
if _, err := client.Telemetry().ReportUsage(
    ctx,
    &azdext.ReportUsageRequest{
        EventName: "deploy.completed",
        Attributes: map[string]string{
            "deploy.mode": "container",
            "retries":     "2",
        },
    },
); err != nil {
    log.Printf("telemetry unavailable: %v", err)
}
```

Treat the call as best-effort. A malformed request is a plain error, and an
older host can return `Unimplemented` when the extension was already installed
or came from a non-registry source. Log the error, but never let it change
command behavior or retry. Report the event immediately after the fact it
represents is known, rather than waiting until the command completes, so a
later unrelated failure does not lose the signal.

An event with no attributes is valid — "this happened" is a legitimate signal.

There is a complete working example in the demo extension:
[`extensions/microsoft.azd.demo/internal/cmd/telemetry.go`](../../extensions/microsoft.azd.demo/internal/cmd/telemetry.go),
runnable with `azd demo telemetry`.

## Bounds

| Rule | Limit |
|---|---|
| Attributes per event | 32 |
| Event name length | 1–128 UTF-8 bytes |
| Attribute key length | 1–128 UTF-8 bytes |
| Attribute value length | 512 UTF-8 bytes |
| Recorded events per `azd` invocation | 100 |

There are no charset rules. Exceeding a per-event bound rejects the whole call
and records nothing, so a partially-valid event never lands as a
complete-looking one. The per-invocation budget behaves differently: see
[When your event is not recorded](#when-your-event-is-not-recorded).

## Your responsibility for content

`azd` does not review your values at runtime. Registry admission is where that
review happens, which makes the content rules your responsibility as the
extension author:

- **Never send customer content.** No file paths, resource names, connection
  strings, prompts, URLs, or anything a user typed. If you are unsure whether a
  value qualifies as customer content, always assume that it does to be safe.
- **Keep values low cardinality.** Prefer a small enum such as
  `code | container | unknown`. Unbounded values make the data expensive and
  unusable for aggregation.
- **Document your events** the way `azd` core documents its own fields: what
  each event and attribute means and why you need it.
- **Get a privacy review** as part of reviewing your extension, following the
  [telemetry privacy review checklist](../../../../docs/specs/metrics-audit/privacy-review-checklist.md).

## When the call returns an error

| Status | Cause |
|---|---|
| `Unauthenticated` | The request did not carry the host-issued extension token |
| `PermissionDenied` | The calling extension is not installed |
| `InvalidArgument` | The event name is missing, or a per-event bound was exceeded |
| `Unimplemented` | The `azd` host predates this service |

Error messages do not echo attribute values. When a valid key has an oversized
value, the key is included so you can identify the failing field; invalid keys
are not echoed. Use the status code plus your own call site to diagnose. Hosts
that support dropped-report telemetry also record the bounded reason described
in [Diagnosing dropped reports](#diagnosing-dropped-reports).

## When your event is not recorded

Two outcomes are deliberately **not** errors. The call succeeds and
`ReportUsageResponse.Accepted` is `false`:

| Cause | Why |
|---|---|
| Your configured source does not match the verified official `azd` registry | Attribute values are never reviewed at runtime, so registry admission is what keeps unchecked content out of `azd`'s pipeline |
| The per-invocation event budget is spent | `ReportUsage` can be called in a loop, and the per-event bounds do not limit how many events arrive |

Run `azd` with `--debug` to see which one applied. Hosts that support
dropped-report telemetry also record the bounded reason described in
[Diagnosing dropped reports](#diagnosing-dropped-reports).

This means **your events are not recorded while you develop locally**, because
an extension installed with `--source dev` or from a file path does not pass the
gate. You can still verify your integration end to end: the call succeeds and
`Accepted` comes back `false`. Because it is not an error, your code runs the
same path in development as in production — do not branch on `Accepted`.

## Diagnosing dropped reports

`azd` aggregates rejected and dropped calls on the command span that hosted the
extension. It records two fields:

| Attribute | Meaning |
|---|---|
| `extension.usage.dropped` | Unique `<extension-id>@<reason>` values for the invocation |
| `extension.usage.dropped.count` | Total reports dropped during the invocation |

The reason is always one of these host-defined values:

| Reason | Cause |
|---|---|
| `event_name_invalid` | The event name is missing or exceeds 128 UTF-8 bytes |
| `attribute_count_exceeded` | The report contains more than 32 attributes |
| `attribute_key_invalid` | An attribute key is empty or exceeds 128 UTF-8 bytes |
| `attribute_value_too_long` | An attribute value exceeds 512 UTF-8 bytes |
| `not_installed` | The calling extension is not installed |
| `lookup_failed` | `azd` could not read the installed extension record |
| `source_check_failed` | `azd` could not verify the configured registry source |
| `source_ineligible` | The configured source is not the verified official registry |
| `budget_exhausted` | The invocation has already recorded 100 extension usage events |
| `unauthenticated` | The request did not contain validated extension claims |

No event name, key, value, source, or other caller-controlled content is copied
into the signal. Repeated failures add to the count but do not add duplicate
values to the list, so a reporting loop cannot create an unbounded property.

Use this query to find how many invocations were affected by each extension and
reason:

```kusto
requests
| where isnotempty(tostring(customDimensions["extension.usage.dropped"]))
| extend dropped = parse_json(tostring(customDimensions["extension.usage.dropped"]))
| mv-expand dropped
| extend parts = split(tostring(dropped), "@")
| summarize affected_invocations=dcount(operation_Id)
    by extension_id=tostring(parts[0]), reason=tostring(parts[1])
```

To inspect total volume separately, sum
`customMeasurements["extension.usage.dropped.count"]`. The count covers all
reasons in an invocation, so do not assign it to one reason when the list
contains several values.

Accepted `ext.usage` spans and the command span share `operation_Id`. Use that
field with `extension.id` to compare affected invocations with invocations that
successfully recorded at least one report.

For a long-running server command, such as `azd vs-server`, the invocation ends
when the `azd` process exits. Its aggregate therefore covers the process
lifetime rather than one RPC.

## Where the data lands

Each accepted event becomes an `ext.usage` span carrying `extension.id`,
`extension.version`, `extension.source`, `extension.event`, and one `ext.<key>`
attribute per entry in your map. The span shares the command's trace, so it
joins to the originating command on `operation_Id` in Application Insights:

```kusto
requests
| where name == "ext.usage"
| where customDimensions["extension.id"] == "contoso.tools"
| where customDimensions["extension.event"] == "deploy.completed"
| summarize count() by tostring(customDimensions["ext.deploy.mode"])
```

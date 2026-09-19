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

Telemetry is a preview service `azd` offers to eligible extensions installed
from the official `azd` registry. Import
`github.com/azure/azure-dev/cli/azd/pkg/azdext/contracts/v1beta` for its request
and response types. Call `ReportUsage` with an event name and the attributes
you care about. `azd` core owns the identity fields, namespaces your attributes,
and bounds their size and number. It does not inspect what they mean.

First-party extensions in this repository must declare every concrete
`ext.*` field in
[`extensions/telemetry/fields.go`](../../extensions/telemetry/fields.go).
A PR check verifies telemetry payload construction and checks every attribute
against that inventory before the extension is released. This keeps
classification failures in development and out of the user's command path.

Runtime recording is limited to eligible official-registry installations.

See [ADR-001](../../../../docs/architecture/adr-001-extension-telemetry-events.md)
for the reasoning behind this design.

## Require a host with telemetry support

A published extension version that depends on this service should declare the
first `azd` release that includes `TelemetryService` in its `extension.yaml`:

```yaml
requiredAzdVersion: ">=1.33.0"
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
| `ext.*` | Extension-chosen keys and values; first-party fields are declared and classified in `extensions/telemetry/fields.go` |

The extension chooses the event name and every `ext.*` key and value. `azd`
enforces the `ext.` prefix, the bounds below, and the eligibility
requirement at runtime. The repository source test separately enforces the
first-party field inventory during development.

`azd` cannot write your attribute keys unprefixed, and you cannot overwrite the
identity fields — a key of `extension.id` is recorded as `ext.extension.id`.

## Reporting an event

Microsoft Foundry extensions should use the shared reporter in
`pkg/foundry/telemetry` instead of calling the generated gRPC client directly:

```go
import "github.com/azure/azure-dev/cli/azd/pkg/foundry/telemetry"

reporter := telemetry.NewReporter(client.Telemetry(), nil)
reporter.Report(ctx, telemetry.Event{
  Name: "deploy.completed",
  Attributes: map[string]string{
    "deploy.mode": "container",
    "retries":     "2",
  },
})
```

`Report` is best effort and has no return value. It applies a one-second
timeout, does not retry, treats `Accepted: false` as a normal result, and writes
only the event name and gRPC status code to the debug log when reporting fails.
It never logs attribute values or raw transport error details. Each Foundry
extension should keep its approved event builders and bounded value types in
the extension that owns those product semantics, and declare each attribute in
the shared field inventory.

Other extension families can call the generated client directly until they
have an appropriate shared package:

```go
if _, err := client.Telemetry().ReportUsage(
    ctx,
    &v1beta.ReportUsageRequest{
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

## Declare and validate attributes

Every first-party attribute must have one exported, package-level
`fields.AttributeKey` declaration in
[`extensions/telemetry/fields.go`](../../extensions/telemetry/fields.go). The
declaration uses the final property name recorded by the host:

```go
var DeployMode = fields.AttributeKey{
	Key:            attribute.Key("ext.deploy.mode"),
	Classification: fields.SystemMetadata,
	Purpose:        fields.FeatureInsight,
	Endpoint:       "N/A",
}
```

Declarations form a shared first-party field schema and are not exclusive to
the extension that introduced them. Another first-party extension may reuse an
existing key when its meaning, allowed values, classification, and purpose are
identical. If any of those differ, declare a distinct key.

The extension still sends the suffix:

```go
Attributes: map[string]string{
	"deploy.mode": "container",
}
```

Choose classification, purpose, and endpoint from what the property actually
contains and why it is collected. Do not copy `SystemMetadata` or
`FeatureInsight` merely because another extension field uses them. Declaring the
`CustomerContent` classification is permitted but requires a completed privacy
review before merge; still avoid raw customer content whenever a
lower-sensitivity value works. Use the
classification and purpose constants defined for core `azd` telemetry and
follow the privacy review checklist when selecting endpoint metadata.
`SystemMetadata` must use `N/A`; every other classification must use a
non-`N/A` endpoint.

Set `Attributes` only in the telemetry payload literal, using `nil` or an
inline `map[string]string` literal. Post-construction access through
`Attributes` or `GetAttributes` is rejected because aliases and helper
mutations can hide fields from static validation. Payload literals must use
keyed fields. Attribute keys must be string literals or same-package
compile-time string constants so repository validation can resolve them.
Generic container literals, re-exported positional payload types, and
type-elided payloads inside named wrapper containers are not supported in
packages that define extension telemetry. Use a concrete keyed telemetry
payload literal instead. Run the validation from `cli/azd`:

```bash
go test ./extensions/telemetry
```

The test scans production Go source under `extensions/`, verifies every final
`ext.*` key has one valid declaration, and fails with the extension, file, and
line for an undeclared key. It analyzes repository source only and does not run
extensions. The extension CI workflow runs the same test.

The inventory is not a runtime allowlist. Metadata changes do not require a new
`azd` release, while released extensions continue to use the existing
best-effort `ReportUsage` behavior.

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

`azd` does not inspect your values at runtime. Privacy review and the content
rules below remain your responsibility as the extension author:

- **Never send customer content.** No file paths, resource names, connection
  strings, prompts, URLs, or anything a user typed. If you are unsure whether a
  value qualifies as customer content, always assume that it does to be safe.
- **Keep values low cardinality.** Prefer a small enum such as
  `code | container | unknown`. Unbounded values make the data expensive and
  unusable for aggregation.
- **Document your events** the way `azd` core documents its own fields: what
  each event and attribute means and why you need it.
- **Declare every attribute** in `extensions/telemetry/fields.go` with the
  classification, purpose, and endpoint that match its actual semantics.
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
are not echoed. Use the status code plus your own call site to diagnose.

## When your event is not recorded

Two outcomes are deliberately **not** errors. The call succeeds and
`ReportUsageResponse.Accepted` is `false`:

| Cause | Why |
|---|---|
| The extension is not eligible for telemetry recording | Recording is limited to reviewed official-registry installations |
| The per-invocation event budget is spent | `ReportUsage` can be called in a loop, and the per-event bounds do not limit how many events arrive |

Run `azd` with `--debug` to see which one applied.

This means **your events are not recorded while you develop locally**, because
an extension installed with `--source dev` or from a file path does not pass the
gate. You can still verify your integration end to end: the call succeeds and
`Accepted` comes back `false`. Because it is not an error, your code runs the
same path in development as in production — do not branch on `Accepted`.

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

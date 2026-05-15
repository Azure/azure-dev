# Tracing in `azd`

<!-- cspell:ignore Kusto predeploy pwsh -->

## Overview

`azd` uses [OpenTelemetry](https://opentelemetry.io/docs/concepts/signals/traces/) to collect **anonymous usage information** and **success metrics**.
Internally, `azd` defines its own wrapper package — [`cli/azd/internal/tracing`](../../azd/internal/tracing) — which simplifies working with the standard OpenTelemetry library.

All `azd` commands automatically create a **root command event** with a namespace prefix of `cmd.` (for example, `cmd.provision`, `cmd.up`, etc.).

---

## Background Concepts

- **Trace** – Represents an entire operation or command (e.g., running `azd up`).
- **Span** (also **Event**) – Represents a single unit of work within that operation (e.g., deploying resources).
- **Attribute** – Metadata attached to a span (e.g., environment name, subscription ID).

For general OpenTelemetry background, see the official [Traces documentation](https://opentelemetry.io/docs/concepts/signals/traces/).

---

## How to Add New Attributes or Events

### 1. Identify What You Want to Add

Decide whether you are adding:

* A new **attribute** (metadata attached to a span), or
* A new **event** (a time-stamped span representing an operation or milestone).

| Type          | File to Update                                           | Description                          |
| ------------- | -------------------------------------------------------- | ------------------------------------ |
| **Attribute** | [`fields.go`](../internal/tracing/fields/fields.go) | Defines standardized attribute keys. |
| **Event**     | [`events.go`](../internal/tracing/events/events.go) | Defines standardized event names.    |

### 2. Recording a New Event

Use the `tracing.Start` helper to start a new span:

```go
ctx, span := tracing.Start(ctx, events.MyNewEvent)
// ... your logic here
//  don't forget to span.SetStatus(codes.Err, "<description>) to mark success or failure
span.End()
```

* `span.End()` marks completion of the event.
* `span.EndWithStatus(err)` sets an appropriate error code if something failed.
* Timing and duration are automatically recorded.

> Note: Traces are automatically exported and uploaded asynchronously. Simply logging the span will be sufficient.

### 3. Adding Attributes

You can set attributes in different ways depending on scope:

| Method                                         | Scope                   | Usage                                                                         |
| ---------------------------------------------- | ----------------------- | ----------------------------------------------------------------------------- |
| `tracing.*UsageAttribute`                      | Root command span       | For attributes that apply to the entire command (e.g., environment, service). |
| `span.SetAttributes(...)`                      | Current span            | For attributes specific to the current operation.                             |
| `tracing.SetGlobalAttributes(...)`             | All spans               | To attach a property globally across all spans.                               |
| `tracing.SetBaggageInContext(ctx, key, value)` | Current and child spans | Propagates an attribute across child contexts.                                |

Example:

```go
tracing.SetUsageAttributes(fields.EnvName.StringHashed(envName))
```

This example sets a usage attribute to be included in the root command event.

---

## Existing Event Taxonomy

The following events already exist in [`events.go`](../internal/tracing/events/events.go). Use them instead of
adding new events for extension and hook lifecycle telemetry.

| Event | Lifecycle | Attributes to expect | Sample row |
| ----- | --------- | -------------------- | ---------- |
| `ext.run` | Running an installed extension command through `azd`. | Command attributes such as `cmd.entry`, `cmd.flags`, `cmd.args.count`, plus `extension.installed` on the root span. | `name=ext.run`, `cmd.entry=cmd.ai.chat`, `cmd.flags=["model"]`, `cmd.args.count=0` |
| `ext.install` | Installing one extension version. | `extension.id` (set as soon as installation begins); `extension.version` (set after the version is resolved). On failure the span uses OpenTelemetry status `Error`; `EndWithStatus` derives the status description from the error type. | `name=ext.install`, `extension.id=microsoft.azd.ai`, `extension.version=1.2.0`, `status=Ok` |
| `ext.upgrade` | Upgrading one extension attempt. | `extension.id`, `extension.version.from`, `extension.version.to`, `extension.source`, `extension.upgrade.duration_ms`, `extension.upgrade.outcome`. | `name=ext.upgrade`, `extension.id=microsoft.azd.ai`, `extension.version.from=1.1.0`, `extension.version.to=1.2.0`, `extension.upgrade.outcome=upgraded` |
| `ext.promote` | Promoting an extension registry entry, such as dev to main. | `extension.id`, `extension.version.from`, `extension.version.to`, `extension.source.from`, `extension.source.to`. | `name=ext.promote`, `extension.id=microsoft.azd.ai`, `extension.source.from=dev`, `extension.source.to=main`, `status=Ok` |
| `hooks.exec` | Executing a project, layer, or service lifecycle hook. | `hooks.name`, `hooks.type`, `hooks.kind`; status description uses hook-specific codes such as `hook.validation_failed`. | `name=hooks.exec`, `hooks.name=predeploy`, `hooks.type=service`, `hooks.kind=sh`, `status=Ok` |

### Extension Attributes

Extension telemetry attributes are defined in [`fields.go`](../internal/tracing/fields/fields.go).

| Attribute | Description | Example |
| --------- | ----------- | ------- |
| `extension.id` | Extension identifier. | `microsoft.azd.ai` |
| `extension.version` | Installed extension version. | `1.2.0` |
| `extension.installed` | Installed extensions on a command span, each formatted as `id@version`. | `["microsoft.azd.ai@1.2.0"]` |
| `extension.version.from` | Version before an upgrade or promotion. | `1.1.0` |
| `extension.version.to` | Version after an upgrade or promotion. | `1.2.0` |
| `extension.source` | Registry source used for an upgrade. | `main` |
| `extension.source.from` | Registry source before a promotion. | `dev` |
| `extension.source.to` | Registry source after a promotion. | `main` |
| `extension.upgrade.duration_ms` | Upgrade duration in milliseconds. | `1532` |
| `extension.upgrade.outcome` | Upgrade result status. | `upgraded` |

### Hook Attributes

`hooks.exec` spans should include the hook name and scope as soon as they are known, then add the executor kind after hook
validation succeeds.

| Attribute | Description | Example |
| --------- | ----------- | ------- |
| `hooks.name` | Hook name. The `azd hooks run` root command hashes unknown hook names before recording usage attributes; `hooks.exec` child spans record the resolved hook name. | `predeploy` |
| `hooks.type` | Hook run scope. | `project`, `layer`, or `service` |
| `hooks.kind` | Executor kind used to run the hook. | `sh`, `pwsh`, `python`, `js`, `ts`, or `dotnet` |

### Error Attribute Conventions

Use `MapError` from [`internal/cmd/errors.go`](../internal/cmd/errors.go) for command, extension, JSON-RPC, MCP, and
agent spans so that error status and attributes stay consistent.

| Convention | Description | Example |
| ---------- | ----------- | ------- |
| Span status | Failed spans mapped through `MapError` set OpenTelemetry status `Error`; the status description is the primary error code. Codes use stable families such as `auth.*`, `ext.*`, `internal.*`, `service.*`, `tool.*`, or `user.*`. | `ext.run.failed`, `service.arm.deployment.failed`, `user.canceled` |
| `error.category` | Broad local error category, used when the error is local rather than returned by an external service. | `auth` |
| `error.code` | Normalized local or extension error code. | `invalid_payload` |
| `error.type` | Go error type for unclassified or suggestion-wrapped errors. | `*os.PathError` |
| `error.service.name` | External service name after `fields.ErrorKey` prefixes `service.name` for error details. Only set this when an external service returned the error. | `arm`, `aad`, `storage` |
| `error.service.errorCode` | Error code returned by an external service, after `fields.ErrorKey` prefixes `service.errorCode`. For ARM deployment errors this is a JSON array describing the nested error chain (see below). | `AuthorizationFailed` |
| `error.service.statusCode` | Status code returned by an external service, after `fields.ErrorKey` prefixes `service.statusCode`. | `403` |

For nested ARM deployment failures, `MapError` walks the inner error tree and encodes each level as an entry in the
JSON array stored on `error.service.errorCode`. Each entry has the shape
`{"error.code": "<code>", "error.arm.frame_index": <n>}`, where `error.arm.frame_index` is the depth in the nested
chain (0 for the outermost error). For example:

```json
[
  {"error.code": "InvalidTemplateDeployment", "error.arm.frame_index": 0},
  {"error.code": "AuthorizationFailed", "error.arm.frame_index": 1}
]
```

Do not attach arbitrary user input or secrets to `error.*` attributes. Prefer the standardized field constants from
`fields.go`; if you need to include a service-related field on an error span, pass it through `fields.ErrorKey` so it is
reported under the `error.` namespace.

---

## 🧪 Observing New Traces

### 1. Local Observation

You can log traces to a file during local development:

```bash
azd up --trace-log-file trace.json
```

Then open the file in your favorite text editor.

### 2. Remote Observation (Released Builds)

When using a released (`daily` or `official`) build of `azd`, traces are automatically uploaded to the shared **DevCli Kusto cluster**.

> **Note:** Newly added attributes or events may require **classification** in the **Data Catalog portal** before they appear in dashboards or reports.

---

## Examples - Previous Work

These example PRs include adding both new spans and events and can be used as reference.

- [#2707](https://github.com/Azure/azure-dev/commit/9b48d014444a56a975d29eb7ecb7bdaad5290dda#diff-feb2d561d3f1e4b74cd988e268b873ce48501f00f29ea6433d37a0f7fb63b705)
- [#5957](https://github.com/Azure/azure-dev/commit/9a6eabe61d07f05f0621d6455a21ee4c8b11d885)

---

## Quick Reference

| Task                  | API                           | Notes                                   |
| --------------------- | ------------------------------------ | --------------------------------------- |
| Start a span          | `tracing.Start(ctx, events.MyEvent)` | Returns a `Span` object.                |
| End a span            | `span.End()`                         | Marks completion of span.               |
| End with status       | `span.EndWithStatus(err)`            | Records success/failure automatically.  |
| Set attribute         | `span.SetAttributes(...)`            | Attach metadata to the span.            |
| Set global attributes | `tracing.SetGlobalAttributes(...)`   | Affects all spans.                      |
| Add root attributes   | `tracing.*UsageAttribute`            | For top-level command attributes.       |

---

## General Tips

* Keep attribute names **consistent and descriptive**. Follow OpenTelemetry naming best practices. Use constants from `fields.go`.
* Always **end spans** and **set status** for the ones you start; otherwise, they appear as incomplete traces.
* Avoid logging any user data. Traces are collected anonymously.
* Use trace logs locally to verify correctness before merging changes.

---

**Next Steps:**

* Explore the [`tracing` package](../../azd/internal/tracing) for more helpers and conventions.
* Learn about [OpenTelemetry Trace Context](https://opentelemetry.io/docs/concepts/context/) to understand how trace IDs propagate through the CLI.

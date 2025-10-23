# 🧭 Tracing in `azd`

## Overview

`azd` uses [OpenTelemetry](https://opentelemetry.io/docs/concepts/signals/traces/) to collect **anonymous usage information** and **success metrics**.
Internally, `azd` defines its own wrapper package — [`cli/azd/internal/tracing`](cli/azd/internal/tracing) — which simplifies working with the standard OpenTelemetry library.

All `azd` commands automatically create a **root command event** with a namespace prefix of `cmd.` (for example, `cmd.provision`, `cmd.up`, etc.).

---

## 🔍 Background Concepts

**Trace** – Represents an entire operation or command (e.g., running `azd up`).
**Span** (used interchangably with **Event**) – Represents a single unit of work within that operation (e.g., deploying resources).
**Attribute** – Metadata attached to a span (e.g., environment name, subscription ID).

For general OpenTelemetry background, see the official [Traces documentation](https://opentelemetry.io/docs/concepts/signals/traces/).

---

## How to Add New Attributes or Events

### 1. Identify What You Want to Add

Decide whether you are adding:

* A new **attribute** (metadata attached to a span), or
* A new **event** (a time-stamped span representing an operation or milestone).

| Type          | File to Update                                           | Description                          |
| ------------- | -------------------------------------------------------- | ------------------------------------ |
| **Attribute** | [`fields.go`](cli/azd/internal/tracing/fields/fields.go) | Defines standardized attribute keys. |
| **Event**     | [`events.go`](cli/azd/internal/tracing/events/events.go) | Defines standardized event names.    |

### 2. Recording a New Event

Use the `tracing.Start` helper to start a new span:

```go
ctx, span := tracing.Start(ctx, events.MyNewEvent)
// ... your logic here ...
span.End() // or span.EndWithStatus(err)
```

* `span.End()` marks completion of the event.
* `span.EndWithStatus(err)` sets an appropriate error code if something failed.
* Timing and duration are automatically recorded.

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
span.SetAttributes(attribute.String(fields.EnvName, envName))
```

---

## 🧪 Observing New Traces

### 1. Local Observation

You can log traces to a file during local development:

```bash
azd up --trace-log-file trace.json
```

Then open the file in your favorite JSON viewer or tracing visualization tool (e.g., VS Code, Jaeger, Zipkin).

### 2. Remote Observation (Released Builds)

When using a released (`daily` or `official`) build of `azd`, traces are automatically uploaded to the shared **DevCli Kusto cluster**.

> **Note:** Newly added attributes or events may require **classification** in the **Data Catalog portal** before they appear in dashboards or reports.

---

## Quick Reference

| Task                  | API / File                           | Notes                                   |
| --------------------- | ------------------------------------ | --------------------------------------- |
| Start a span          | `tracing.Start(ctx, events.MyEvent)` | Returns a `Span` object.                |
| End a span            | `span.End()`                         | Marks completion of span.               |
| End with status       | `span.EndWithStatus(err)`            | Records success/failure automatically.  |
| Set attribute         | `span.SetAttributes(...)`            | Attach metadata to the span.            |
| Set global attributes | `tracing.SetGlobalAttributes(...)`   | Affects all spans.                      |
| Add root attributes   | `tracing.*UsageAttribute`            | For top-level command attributes.       |
| Write traces locally  | `--trace-log-file <path>`            | Saves trace output for local debugging. |

---

## General Tips

* Keep attribute names **consistent and descriptive**. Follow OpenTelemetry naming best practices. Use constants from `fields.go`.
* Always **end spans** you start; otherwise, they appear as incomplete traces.
* Avoid logging sensitive data. Traces are collected anonymously.
* Use trace logs locally to verify correctness before merging changes.

---

**Next Steps:**

* Explore the [`tracing` package](cli/azd/internal/tracing) for more helpers and conventions.
* Learn about [OpenTelemetry Trace Context](https://opentelemetry.io/docs/concepts/context/) to understand how trace IDs propagate through the CLI.

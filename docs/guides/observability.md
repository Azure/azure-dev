# Observability and Tracing

This guide covers how to add telemetry, inspect traces, and debug the Azure Developer CLI.

## Tracing Architecture

azd uses [OpenTelemetry](https://opentelemetry.io/) to collect usage, performance, and reliability telemetry. The internal tracing package at `cli/azd/internal/tracing` wraps the standard OpenTelemetry library.

Before changing collection, follow the [privacy review checklist](../specs/metrics-audit/privacy-review-checklist.md).

All commands automatically create a root command event with a `cmd.` namespace prefix (e.g., `cmd.provision`, `cmd.up`).

## Key Concepts

- **Trace** — Represents an entire operation or command (e.g., `azd up`)
- **Span** — A single unit of work within an operation (e.g., deploying a resource)
- **Attribute** — Metadata attached to a span (e.g., environment name, subscription ID)

## Viewing Traces Locally

The file and OTLP outputs use the same resource policy as azd's Application Insights telemetry. azd exports only its
declared application resource fields and standard OpenTelemetry SDK metadata. Environment-provided resource values,
including `OTEL_RESOURCE_ATTRIBUTES` and `OTEL_SERVICE_NAME`, are not included in the exported resource. This does
not remove declared span attributes or first-party extension `ext.*` usage attributes.

### Write to file

```bash
azd provision --trace-log-file traces.json
```

### Send to Jaeger

Start a local Jaeger instance:

```bash
docker run -d --name jaeger \
  -e COLLECTOR_OTLP_ENABLED=true \
  -e JAEGER_DISABLED=true \
  -p 16686:16686 \
  -p 4318:4318 \
  jaegertracing/all-in-one
```

Then pass the endpoint to any command:

```bash
azd provision --trace-log-url http://localhost:4318
```

> **Note:** `--trace-log-url localhost` also works as a shorthand — azd automatically expands it to `http://localhost`.

View results at [http://localhost:16686/search](http://localhost:16686/search).

## Adding New Telemetry

### Event Name Prefixes

Telemetry prefixes are centralized in `internal/tracing/events`:

| Prefix | Usage |
|---|---|
| `cmd.` | CLI command events |
| `vsrpc.` | VS Code RPC events |
| `mcp.` | MCP tool events |

Use the constants from `internal/tracing/events/events.go` rather than string literals.

### Adding an Attribute

Use a declared field and follow its hashing rules in the [schema](../specs/metrics-audit/telemetry-schema.md#hashing).
For example, the existing environment-name field requires hashing:

```go
span.SetAttributes(fields.StringHashed(fields.EnvNameKey, envName))
```

### Adding a Span

For a new event, first declare an exported event constant as described in the [feature telemetry guide](feature-telemetry.md#step-1-define-your-events).
Then create a span with that constant:

```go
ctx, span := tracing.Start(ctx, events.MyFeatureEvent)
defer span.End()
```

## Detailed Reference

- [Tracing in azd](../../cli/azd/docs/tracing-in-azd.md) — Full tracing implementation guide
- [Observability](../../cli/azd/docs/observability.md) — Trace output configuration

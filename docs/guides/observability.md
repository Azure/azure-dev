# Observability and Tracing

This guide covers how to add telemetry, inspect traces, and debug the Azure Developer CLI.

## Tracing Architecture

azd uses [OpenTelemetry](https://opentelemetry.io/) to collect anonymous usage information and success metrics. The internal tracing package at `cli/azd/internal/tracing` wraps the standard OpenTelemetry library.

All commands automatically create a root command event with a `cmd.` namespace prefix (e.g., `cmd.provision`, `cmd.up`).

## Key Concepts

- **Trace** — Represents an entire operation or command (e.g., `azd up`)
- **Span** — A single unit of work within an operation (e.g., deploying a resource)
- **Attribute** — Metadata attached to a span (e.g., environment name, subscription ID)

## Viewing Traces Locally

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

Attach metadata to an existing span:

```go
span.SetAttributes(attribute.String("my.attribute", value))
```

### Adding a Span

Create a new span for a unit of work:

```go
ctx, span := tracing.Start(ctx, "mypackage.operation")
defer span.End()
```

## Detailed Reference

- [Tracing in azd](../../cli/azd/docs/tracing-in-azd.md) — Full tracing implementation guide
- [Observability](../../cli/azd/docs/observability.md) — Trace output configuration

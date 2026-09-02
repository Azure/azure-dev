# Foundry Toolboxes

Manage Microsoft Foundry Toolboxes from your terminal. (Preview)

## Extension telemetry API

Extension code can report best-effort usage events through
`internal/telemetry`:

```go
reporter := telemetry.NewReporter(azdClient.Telemetry(), nil)
reporter.Report(ctx, telemetry.Event{
  Name: "toolbox.operation.completed",
  Attributes: map[string]string{
    "operation": "publish",
  },
})
```

The example is illustrative; this extension does not currently emit a product
usage event. Add an event only after its product question, bounded values,
documentation, and privacy review are agreed.

`Report` has no return value and never changes command or service-target
behavior. It uses a one-second timeout, does not retry, and does not log
attribute values or transport error details. Put approved event builders and
finite-value types in `internal/telemetry/events.go`; do not call `ReportUsage`
directly. Never include toolbox, connection, or skill names, tool definitions,
IDs, endpoints, paths, URLs, or other customer content. The azd host records
events only for extensions installed from the official registry.

## Reuse an existing toolbox in `azure.yaml`

A `host: azure.ai.toolbox` service normally creates a new toolbox version from
its `tools` on each `azd deploy`. To reuse a toolbox that already exists (for
example one shared across projects, or created with `azd ai toolbox create`),
set `endpoint` to its MCP endpoint instead. azd then publishes that endpoint for
agents without creating a new version. This mirrors the `azure.ai.project`
`endpoint` field: omit it to create, set it to reuse.

```yaml
services:
  research-tools:
    host: azure.ai.toolbox
    endpoint: ${RESEARCH_TOOLBOX_ENDPOINT}
    env:
      RESEARCH_TOOLBOX_ENDPOINT: ${RESEARCH_TOOLBOX_ENDPOINT}
```

Get the endpoint value from `azd ai toolbox show <name>` (the `Endpoint:` line).
The value may contain `${VAR}` references. Declare each referenced variable
in the service-level `env` object; azd falls back to the active environment
only when the service declares no `env`. Because a toolbox version is immutable, `endpoint` cannot be
combined with `tools` or `description`.


# Foundry Routines

Manage Microsoft Foundry Routines from your terminal. (Preview)

## Extension telemetry API

Extension code can report best-effort usage events through
`internal/telemetry`:

```go
reporter := telemetry.NewReporter(azdClient.Telemetry(), nil)
reporter.Report(ctx, telemetry.Event{
  Name: "routine.operation.completed",
  Attributes: map[string]string{
    "operation": "dispatch",
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
directly. Never include routine names, definitions, schedules, inputs, outputs,
IDs, endpoints, paths, URLs, or other customer content. The azd host records
events only for extensions installed from the official registry.

## Reference a routine manifest

An `azure.ai.routine` service can load its routine definition from a local YAML
or JSON file. Properties beside `$ref` override values from the referenced file.

```yaml
services:
  nightly-summary:
    host: azure.ai.routine
    $ref: ./routines/nightly-summary.yaml
    enabled: false
```

References are resolved during `azd deploy`. Remote URLs are not supported.

## Timeout configuration

Routine read API calls default to a 30-second HTTP request timeout.
Routine write API calls default to a two-minute timeout to allow cold
recurring routine creates to finish AgentIdentity binding. Override both
defaults with the root `--timeout` flag, using Go duration syntax:

```bash
azd ai routine --timeout 3m create my-routine ...
```

Set `AZURE_AI_ROUTINES_HTTP_TIMEOUT` to apply the same override when
the extension runs without command flags, such as during `azd deploy`
service target upserts. The `--timeout` flag wins when both are
provided.

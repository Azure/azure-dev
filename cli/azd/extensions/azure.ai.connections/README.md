# Foundry Connections

Manage Microsoft Foundry Connections from your terminal. (Preview)

## Extension telemetry API

Extension code can report best-effort usage events through
`internal/telemetry`:

```go
reporter := telemetry.NewReporter(azdClient.Telemetry(), nil)
reporter.Report(ctx, telemetry.Event{
	Name: "connection.operation.completed",
	Attributes: map[string]string{
		"operation": "create",
	},
})
```

The example is illustrative; this extension does not currently emit a product
usage event. Add an event only after its product question, bounded values,
documentation, and privacy review are agreed.

`Report` has no return value and never changes command behavior. It uses a
one-second timeout, does not retry, and does not log attribute values or
transport error details. Put approved event builders and finite-value types in
`internal/telemetry/events.go`; do not call `ReportUsage` directly from command
or service-target code. Never include connection names, endpoints, credentials,
IDs, resource names, paths, URLs, or other customer content. The azd host records
events only for extensions installed from the official registry.

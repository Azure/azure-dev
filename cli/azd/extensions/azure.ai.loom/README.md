# Foundry Loom

Inspect and ingest Microsoft Foundry experiment-tracking data from `azd`. (Preview)

## Installation

```sh
azd extension install azure.ai.loom
```

The extension can reuse the project endpoint persisted by `azure.ai.projects`,
but it can also run independently with `--project-endpoint` or an environment
variable.

## Authentication and project resolution

Commands authenticate through `azd auth login` using the
`https://ai.azure.com/.default` scope. To use a Foundry account API key accepted
by the project data plane, set `AZURE_AI_PROJECT_API_KEY` in the current process.
The API key takes precedence over bearer authentication and must not be stored
in project files or source control.

The project endpoint is resolved in this order:

1. `--project-endpoint`
2. `FOUNDRY_PROJECT_ENDPOINT` or `AZURE_AI_PROJECT_ENDPOINT` in the active azd environment
3. The endpoint saved by `azd ai project set <endpoint>`
4. `FOUNDRY_PROJECT_ENDPOINT` in the host shell

The project ID is derived from `/api/projects/<project>` in the endpoint. Use
`--project-id` only when an API-compatible endpoint requires an override.

## Commands

### Inspect runs

```sh
azd ai loom run list
azd ai loom run history-keys --run-id <run-id>
azd ai loom run summary --run-id <run-id>
azd ai loom run metrics --run-id <run-id>
azd ai loom run system-metrics --run-id <run-id> --name system/cpu
azd ai loom run logs --run-id <run-id>
azd ai loom run log-records --run-id <run-id>
azd ai loom run compare \
  --run-id <first-run> --run-id <second-run> \
  --metric loss --min 0 --max 100
```

### Traces and spans

```sh
azd ai loom run trace list --run-id <run-id>
azd ai loom run trace show --run-id <run-id> --trace-id <trace-id>
azd ai loom run trace chat --run-id <run-id> --trace-id <trace-id>
azd ai loom run span query \
  --run-id <run-id> \
  --filter-file ./span-filter.json \
  --include-details \
  --limit 10
```

A span filter contains the query expression only:

```json
{
  "$expr": {
    "$eq": [
      { "$getField": "span_name" },
      { "$literal": "chat" }
    ]
  }
}
```

When no filter is provided, the command uses `{"$expr":true}`. Use
`--request-file` to send a complete span-query or trace-chat request body.

### Ingest OpenTelemetry data

```sh
azd ai loom run ingest metrics --run-id <run-id> --file ./metrics.pb
azd ai loom run ingest logs --run-id <run-id> --file ./logs.pb
azd ai loom run ingest traces --run-id <run-id> --file ./traces.pb
azd ai loom run ingest agent-traces --run-id <run-id> --file ./agent-traces.json
```

The OTLP metrics, logs, and traces commands require binary protobuf payloads.
Agent traces require JSON. Use `--file -` to read from stdin. Empty payloads are
rejected before a service request is made.

### W&B compatibility

```sh
azd ai loom run wandb graphql --file ./graphql-request.json
azd ai loom run wandb file-stream \
  --run-id <run-id> \
  --file ./file-stream-request.json
```

All commands emit complete JSON responses for automation.

## Development

```sh
azd x build
go test ./... -count=1
```

To exercise every command against a project from PowerShell:

```powershell
.\test-all.ps1 `
  -ProjectEndpoint "https://<account>.services.ai.azure.com/api/projects/<project-id>" `
  -RunId "<run-id>" `
  -SecondRunId "<second-run-id>" `
  -TraceId "<trace-id>" `
  -MetricsFile .\testdata\metrics.pb `
  -LogsFile .\testdata\logs.pb `
  -TracesFile .\testdata\traces.pb `
  -AgentTracesFile .\testdata\agent-traces.json `
  -GraphQLFile .\testdata\graphql-request.json `
  -FileStreamFile .\testdata\file-stream-request.json
```

The script builds and installs the extension, runs all commands, and prints a
pass/fail summary. Set `AZURE_AI_PROJECT_API_KEY` before running it to use API
key authentication. Otherwise, it uses the current `azd auth login` session.
Use `-SkipWriteOperations` to test only inspection, trace, and span commands.

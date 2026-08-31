# Foundry Projects

Manage Microsoft Foundry Project resources from your terminal. (Preview)

## `azure.yaml` ownership

This extension owns `host: azure.ai.project` services and the `microsoft.foundry` provisioning provider. A project service carries account-level settings such as an existing project endpoint, model deployments, and private networking.

```yaml
infra:
  provider: microsoft.foundry

services:
  my-project:
    host: azure.ai.project
    endpoint: https://my-account.services.ai.azure.com/api/projects/my-project
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          name: GlobalStandard
          capacity: 50
```

When `endpoint` is omitted, `azd provision` creates a Foundry account and project. When it is set, provisioning reuses that project and reconciles the declarations that can be applied to an existing account.

To reconcile deployments, connections, or a pending container registry on an existing project, set the project's full ARM resource ID in the active azd environment:

```sh
azd env set AZURE_AI_PROJECT_ID "/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account>/projects/<project>"
```

`azd ai agent init` sets this value when initialized against an existing project. An endpoint-only service with no resources to reconcile does not require it.

When provisioning reports insufficient Cognitive Services quota, check usage for the target region with
`az cognitiveservices usage list --location <region>` or request a quota increase in the Azure portal. If an
existing Foundry project should be reused instead, configure its endpoint and set `AZURE_AI_PROJECT_ID` to the
full project resource ID before retrying.

The `azd ai project set`, `show`, and `unset` commands manage the default Foundry project endpoint context. They do not currently author the project service in `azure.yaml`.

## Experiment tracking

The extension exposes the Foundry project experiment-tracking APIs. Commands
reuse the project endpoint context described above and authenticate through
`azd auth login` with the `https://ai.azure.com/.default` scope.

To use the Foundry account API key accepted by the project data plane, set
`AZURE_AI_PROJECT_API_KEY` in the current process. The environment variable
takes precedence over bearer authentication and should not be persisted in
`azure.yaml`, azd environment files, or shell profiles.

The project ID is derived from the final path segment of the resolved endpoint:

```text
https://my-account.services.ai.azure.com/api/projects/my-project
                                                        └─ project ID
```

Use `--project-id` only when calling a nonstandard endpoint whose path does not
contain the project ID.

### Runs

```sh
azd ai project run list
azd ai project run summary --run-id <run-id>
azd ai project run metrics --run-id <run-id>
azd ai project run system-metrics --run-id <run-id> --name system/cpu
azd ai project run logs --run-id <run-id>
azd ai project run log-records --run-id <run-id>
azd ai project run traces --run-id <run-id>
azd ai project run trace show --run-id <run-id> --trace-id <trace-id>
azd ai project run compare \
  --run-id <first-run> --run-id <second-run> \
  --metric loss --min 0 --max 100
```

All experiment-tracking commands emit JSON so automation receives the complete
service response without a lossy table projection.

### Span filters

Pass a filter inline or in a JSON file. When neither is provided, the command
uses `{"$expr":true}`.

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

```sh
azd ai project run spans query \
  --run-id <run-id> \
  --filter-file ./span-filter.json \
  --include-details \
  --limit 10
```

The CLI wraps the filter with the resolved project ID:

```json
{
  "project_id": "my-project",
  "query": {
    "$expr": {
      "$eq": [
        { "$getField": "span_name" },
        { "$literal": "chat" }
      ]
    }
  },
  "include_details": true,
  "limit": 10
}
```

Use `--request-file` to send a complete span-query or trace-chat request body.

### Ingestion and W&B compatibility

OTLP commands require an explicit payload and never send an empty no-op request:

```sh
azd ai project ingest metrics --run-id <run-id> --file ./metrics.pb
azd ai project ingest logs --run-id <run-id> --file ./logs.pb
azd ai project ingest traces --run-id <run-id> --file ./traces.pb
azd ai project ingest agent-traces --run-id <run-id> --file ./agent-traces.json
```

Use `--file -` to read a payload from stdin. Advanced W&B-compatible requests
accept complete JSON request bodies:

```sh
azd ai project wandb graphql --file ./graphql-request.json
azd ai project wandb file-stream \
  --run-id <run-id> \
  --file ./file-stream-request.json
```

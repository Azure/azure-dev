# Foundry Routines

Manage Microsoft Foundry Routines from your terminal. (Preview)

## Timeout configuration

Routine API calls default to a two-minute HTTP request timeout. Override
it with the root `--timeout` flag, using Go duration syntax:

```bash
azd ai routine --timeout 3m create my-routine ...
```

Set `AZURE_AI_ROUTINES_HTTP_TIMEOUT` to apply the same override when the
extension runs without command flags, such as during `azd deploy` service
target upserts. The `--timeout` flag wins when both are provided.

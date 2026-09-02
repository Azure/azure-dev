# Foundry Routines

Manage Microsoft Foundry Routines from your terminal. (Preview)

## Add a routine to azure.yaml

Use `routine add` to declare a local YAML or JSON routine manifest in the
current project's `azure.yaml`. The command only updates local project
configuration; run `azd deploy <name>` or `azd up` to create or update the
routine in Microsoft Foundry.

```bash
azd ai routine add nightly-summary --file ./routines/nightly-summary.yaml
azd deploy nightly-summary
```

The manifest must be inside the azd project. The command writes a portable
`$ref`, produces the same declaration when repeated, and adds `uses:` when the
manifest invokes an `azure.ai.agent` service in the same project. Existing
service fields not owned by the routines extension are preserved. Convert or
remove an inline routine service before replacing it with a file-backed
declaration.

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

# Foundry Routines

Manage Microsoft Foundry Routines from your terminal. (Preview)

## Add a routine to azure.yaml

Pass `--add-to-project` to `routine create` or `routine update` to declare the
routine in the current project's `azure.yaml` as a `host: azure.ai.routine`
service. When the routine invokes an `azure.ai.agent` service declared in the
same project, the command also adds the corresponding `uses:` dependency.
The routine name must be a valid azd service name: 1-63 characters, starting
with a letter or number and containing only letters, numbers, `.`, `_`, or `-`.

```bash
azd ai routine create nightly-summary \
  --trigger recurring \
  --cron "0 2 * * *" \
  --agent-name summarizer \
  --add-to-project
```

Repeated updates modify the existing service instead of adding another one.
After authoring, `azd deploy nightly-summary` reconciles the routine from
`azure.yaml`. Routine trigger and action types are immutable after creation;
delete and recreate the routine to change either type.

The command validates the local project before changing the remote routine.
If a later `azure.yaml` write fails after the remote operation succeeds, fix
the reported project error and rerun `azd ai routine update "<name>" --add-to-project`.

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

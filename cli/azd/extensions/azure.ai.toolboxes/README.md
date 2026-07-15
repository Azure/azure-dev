# Foundry Toolboxes

Manage Microsoft Foundry Toolboxes from your terminal. (Preview)

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
```

Get the endpoint value from `azd ai toolbox show <name>` (the `Endpoint:` line).
The value may contain `${VAR}` references, which resolve against the azd
environment. Because a toolbox version is immutable, `endpoint` cannot be
combined with `tools` or `description`.

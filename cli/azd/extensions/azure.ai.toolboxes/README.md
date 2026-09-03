# Foundry Toolboxes

Manage Microsoft Foundry Toolboxes from your terminal. (Preview)

## `azure.yaml` ownership

This extension owns the runtime lifecycle for `host: azure.ai.toolbox`
services. During the staged ownership migration, `azd ai agent init` may still
author the service block, but the Toolboxes extension reads it, resolves its
Connection and Skill references, creates the Toolbox version during deploy, and
publishes the resulting MCP endpoint.

`azd deploy <toolbox-service>` reconciles one Toolbox. In `azd up`, `uses`
orders the Project and Connection services before the Toolbox, and the Toolbox
before an Agent that consumes it. The Agents extension does not parse split
Toolbox service blocks at provision or deploy time. Its standalone legacy
`azd ai agent deploy` path delegates a sibling `toolbox.yaml` to
`azd ai toolbox deploy`.

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


# Foundry Toolboxes

Manage Microsoft Foundry Toolboxes from your terminal. (Preview)

## Deploy a toolbox through `azure.yaml`

This extension owns `host: azure.ai.toolbox` services. Core `azd deploy` and
`azd up` call its service target to create a new immutable toolbox version and
write the version-specific MCP endpoint to the azd environment.

### Migrate standalone Toolbox deployment

`azd ai toolbox deploy [path]` has been removed. For ongoing deployment, move the
definition fields inline into an `azure.ai.toolbox` service and use
`azd deploy <service>` instead:

```yaml
services:
  research-tools:
    host: azure.ai.toolbox
    description: Research tools
    tools:
      - type: web_search
```

The service key (`research-tools` here) is the remote toolbox name. When migrating
an existing toolbox, use its name as the service key and omit the file's `name`
field. Copy `description`, `connections`, `skills`, `tools`, `policies`, and
`metadata` as needed. Toolbox services do not currently load a root `$ref` or
automatically read the local definition file. Editing that file alone does not
update the service definition.

Configure a Foundry project endpoint before deploying. If the project or
referenced Connections are also managed as services, declare those dependencies
with `uses`. Run `azd provision` for the project, then `azd deploy --all` for
Connections, Toolboxes, and Agents (or use `azd up`). To update only this toolbox
after its dependencies are ready, run `azd deploy research-tools`; the argument
is the service key, not a definition file path.

For direct resource operations:

- `azd ai toolbox create <name> --from-file <path>` creates a new toolbox and
  its initial version. It rejects a toolbox that already exists.
- `azd ai toolbox add connection` and `add skill` only edit the local definition
  file, which can still be used with `create --from-file`.
- `azd ai toolbox connection add/remove` and `skill add/remove` create versions
  of an existing toolbox for those specific changes. They do not replace the
  complete definition from a file; use a service deployment for that workflow.
- Creating a version does not promote it to the default version. Use
  `azd ai toolbox publish <toolbox> <version>` when a default-version change is
  needed. `list`, `show`, `versions list`, and `delete` remain available.

Removing a service from `azure.yaml` stops managing the toolbox but does not
delete the remote resource; use `azd ai toolbox delete` to delete it.

## Extension telemetry API

Extension code can report best-effort usage events through the shared
`pkg/foundry/telemetry` package:

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


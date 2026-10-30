# Foundry Toolboxes

<!-- cspell:ignore strconv -->

Manage Microsoft Foundry Toolboxes from your terminal. (Preview)

Requires azd 1.37.0 or later.

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
    connections:
      - name: bing-search
        instanceName: docs-config
    tools:
      - type: web_search
    policies:
      raiConfig:
        raiPolicyName: Microsoft.Default
```

The service key (`research-tools` here) is the remote toolbox name. When migrating
an existing toolbox, use its name as the service key and omit the file's `name`
field. Copy `description`, `connections`, `skills`, `tools`, `policies`, and
`metadata` as needed. Toolbox services do not currently load a root `$ref` or
automatically read the local definition file. Editing that file alone does not
update the service definition.

Toolbox-owned authoring properties use camelCase, including
`connections[].instanceName`, `policies.raiConfig`, and
`policies.raiConfig.raiPolicyName`. Entries under `tools` are Foundry API-owned
pass-through payloads and retain the field names and discriminator values defined
by the corresponding service contract.

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

### Project access for deploy and create

Managed `azure.ai.toolbox` deployment and standalone `azd ai toolbox create`
validate local inputs, then inspect ARM roles **before the first Foundry request**
(including connection resolution). For a verified matching active-environment
project, they reuse sufficient inherited data-plane roles or attempt to assign
**Foundry User** to the signed-in user or service principal at that exact project
scope. Azure subscription Owner and Contributor alone are not data-plane roles.
New grants get an initial 15-second settling delay before readiness is probed;
access propagation and project synchronization polling are bounded to five minutes.

This ARM-first ordering avoids priming Foundry's negative authorization cache,
but can add the built-in role even when a custom role already provides access.
ARM inspection or assignment failures do not block working custom-role access
and produce no advisory warning on success. When ARM cannot confirm a sufficient
role and a readiness read is forbidden by an authorization failure, managed
deployment attempts the actual write, preserving write-only custom-role access.
Standalone create still requires its read to check whether the toolbox exists.
Other readiness failures and confirmed-role propagation checks remain blocking.
Actual write failures include project-access remediation when applicable.
Deployment reports transient status through azd progress; standalone create
writes status to stderr so JSON on stdout remains machine-readable.

Automatic grants require the selected endpoint to match the active environment's
`FOUNDRY_PROJECT_ENDPOINT` (falling back to `AZURE_AI_PROJECT_ENDPOINT` when blank),
an exact `AZURE_AI_PROJECT_ID` under
`Microsoft.CognitiveServices/accounts/projects`, and a consistent
`AZURE_SUBSCRIPTION_ID`. An explicit matching `--project-endpoint` participates;
an unrelated endpoint never borrows the active project's grant scope or tenant.
The offline identity check verifies ordinary account-name hostnames. A custom
subdomain that differs from the ARM account name cannot be verified this way,
so automatic grants are conservatively skipped without rejecting that endpoint.
Missing or unverified identity also uses existing access.

Set `AZD_AGENT_SKIP_ROLE_ASSIGNMENTS=true` to opt out. A nonempty active-environment
value overrides the process value; only values parsed as true by Go's
`strconv.ParseBool` opt out (`true`, `TRUE`, `True`, `1`, `t`, or `T`).
Provisioning, bring-your-own toolbox endpoint reuse, local authoring, read/list/show/delete,
and other standalone write verbs do not invoke automatic grants.

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
documentation, and privacy review are agreed. Before emitting, declare each
`ext.*` attribute in `cli/azd/extensions/telemetry/fields.go` — reuse an existing
declaration only when its meaning, allowed values, classification, and purpose are
identical, otherwise declare a distinct key — as enforced by
`go test ./extensions/telemetry`.

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

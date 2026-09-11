# Foundry Connections

Manage Microsoft Foundry Connections from your terminal. (Preview)

## `azure.yaml` ownership

This extension owns `host: azure.ai.connection` services. Each service block
describes one Foundry project Connection and is reconciled by `azd deploy` or
`azd up` after the referenced `azure.ai.project` service is provisioned.

```yaml
services:
  search:
    host: azure.ai.connection
    uses:
      - my-project
    category: CognitiveSearch
    target: ${SEARCH_ENDPOINT}
    authType: ApiKey
    credentials:
      key: ${SEARCH_KEY}
    env:
      SEARCH_ENDPOINT: ${SEARCH_ENDPOINT}
      SEARCH_KEY: ${SEARCH_KEY}
```

The service key identifies the dependency in `uses`. When the block also has a
`name`, that value is the Foundry Connection name; otherwise the service key is
used. Local `$ref` files and nested credential values are resolved by this
extension. Removing the service from `azure.yaml` stops managing it but does
not delete the remote Connection; use `azd ai connection delete` to delete it.

Effective Connection names must be unique across Connection services, including
names loaded through `$ref`; names that differ only by case also conflict.
Deployment rejects duplicate names before writing to ARM.

Unknown definition fields are rejected instead of silently defaulting misspelled
authentication settings. Referenced files contain only Connection definition
fields. Keep core-owned settings such as `host`, `uses`, and `env` on the service
entry in `azure.yaml`; even `env: {}` in a referenced file is rejected rather than
ignored. Arbitrary keys inside `credentials` and `metadata` remain supported.

After resolving file references and environment variables, deployment validates
the category, target, and authentication configuration before any ARM write.
API key and custom-key authentication require credentials; OAuth2 requires either
a managed connector or complete BYO OAuth2 fields, not both. Invalid definitions
do not publish readiness markers. This validation is shared with standalone
create/deploy commands and preserves nested service credential payloads.

Standalone commands reject explicitly supplied authentication flags that cannot
apply to the selected auth type, including explicitly empty values. Metadata
must use `key=value` entries with non-blank keys; empty values are allowed.

The `microsoft.foundry` provider no longer provisions declared Connection
services in any mode. Embedded, ejected Bicep, and ejected Terraform templates
contain no generic Connection resources or credential parameters. The Projects
extension still owns the system ACR connection associated with its registry.

### Breaking migration

Upgrade the Agents, Projects, Connections, and Toolboxes extensions together.
Move bundled Connection and Toolbox definitions into independent
`azure.ai.connection` and `azure.ai.toolbox` services, and wire Agent dependencies
using `uses`, `azd ai agent connection add <service> --agent <agent>`, or
`azd ai agent toolbox add <service> --agent <agent>`.

For previously ejected infrastructure, remove the old generic Connection modules,
resources, `connections` / `connectionCredentials` parameters and aggregate
readiness outputs, or regenerate the infrastructure after saving custom changes.
Upgrading extensions alone does not rewrite existing IaC. The Foundry provider
rejects generic Foundry Connection resources found in compiled on-disk Bicep,
including inline nested deployments. It does not reject unrelated parameters
merely named `connections` or `connectionCredentials`; user-owned Terraform
must be updated before applying it. When removing Terraform resources from
configuration, plan a state handoff so Terraform does not destroy Connections
now managed by this extension. Existing Azure Connections are not deleted by
the migration.

Run `azd provision` for the Project, then `azd deploy --all` for Connections,
Toolboxes, and Agents (or use `azd up`). A targeted Agent deployment does not
automatically deploy all of its dependencies.

### Deployment environment isolation

Lifecycle deployment reads `FOUNDRY_PROJECT_ENDPOINT` (or
`AZURE_AI_PROJECT_ENDPOINT`) only from the selected azd environment's persisted
values. If neither is set, deployment fails before creating the Connection;
it does not fall back to a global project context, process variables, or the
service's `env` block. Provision the selected environment or set its endpoint
before retrying. Standalone `azd ai connection` commands retain their existing
endpoint fallback cascade.

During lifecycle deployment, the project ARM ID (`AZURE_AI_PROJECT_ID`) and
subscription used for tenant lookup (`AZURE_SUBSCRIPTION_ID`) are also read
together from the selected environment's persisted values, without
process-variable fallback. Standalone commands retain their previous per-key
process-variable fallback after finding the active azd environment; persisted
values take precedence. These context values remain optional: missing values or
read failures still allow ARM discovery and default credential behavior. If the
azd daemon or active environment is unavailable, standalone commands retain
those defaults without reading process context values.

For `azd ai connection delete --environment <name>`, the selected environment is
used for both the remote Connection context and local readiness cleanup. Without
an explicit `--project-endpoint`, its persisted project endpoint is required;
a missing endpoint fails before any Connection lookup, marker change, or deletion
instead of falling back to the default environment or global/process context.
An explicit `--project-endpoint` still wins, while ARM context and tenant lookup
use only the selected environment's persisted values. Omitting `--environment`
retains the standalone fallback behavior described above.

For an existing project with no Connections, discovery cannot infer its ARM
subscription and resource group from the endpoint alone. Persist the full
`AZURE_AI_PROJECT_ID` in the selected azd environment, or adopt the project with
`azd ai project add --project-id <project-resource-id>`, before deploying its
first Connection. The error includes environment-specific setup guidance.

### Readiness markers

Each deployment attempt first clears the service's previous readiness marker.
Only after a successful ARM write does the extension publish
`CONNECTION_V2_<UPPERCASE_HEX_SERVICE_NAME>_PROJECT_ENDPOINT` to the selected azd
environment. The encoded portion contains the exact UTF-8 service-key bytes,
preserving punctuation and case so distinct services cannot share a marker.
The Agents extension uses the same encoding for dependency validation.

`azd ai connection delete` clears matching local service markers before deleting
the resource, matching both the effective Connection name and project endpoint.
This includes payload-name overrides and file references; markers for other
projects or environments are left unchanged. Failure to maintain local markers
stops deletion rather than leaving a deleted Connection marked ready.

If the Connection is already absent (including deletion outside azd), delete
still clears matching markers and succeeds without confirmation. Other lookup
errors are returned without changing markers; marker-cleanup errors remain failures.

Old normalized per-service markers are not trusted. Redeploy Connections after
updating the extensions to regenerate their readiness markers. Legacy aggregate
markers from infrastructure provisioning are no longer accepted.

## Extension telemetry API

Extension code can report best-effort usage events through the shared
`pkg/foundry/telemetry` package:

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

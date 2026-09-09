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

### Readiness markers

After deployment, the extension writes
`CONNECTION_V2_<UPPERCASE_HEX_SERVICE_NAME>_PROJECT_ENDPOINT` to the selected azd
environment. The encoded portion contains the exact UTF-8 service-key bytes,
preserving punctuation and case so distinct services cannot share a marker.
The Agents extension uses the same encoding for dependency validation.

Old normalized per-service markers are not trusted. Redeploy Connections after
updating the extensions to regenerate their readiness markers. Legacy aggregate
markers from infrastructure provisioning are no longer accepted.

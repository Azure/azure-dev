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

The embedded `microsoft.foundry` provider no longer provisions split
`azure.ai.connection` services. Existing ejected or user-owned infrastructure
continues to receive its declared Connection inputs for compatibility.

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
updating both extensions to regenerate their readiness markers. Legacy aggregate
markers from infrastructure provisioning remain supported.

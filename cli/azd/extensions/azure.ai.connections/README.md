# Foundry Connections

Manage Microsoft Foundry Connections from your terminal. (Preview)

## `azure.yaml` ownership

This extension owns the runtime lifecycle for `host: azure.ai.connection`
services. During the staged ownership migration, `azd ai agent init` may still
author the service block, but the Connections extension reads and reconciles it.

```yaml
services:
  search:
    host: azure.ai.connection
    uses: [ai-project]
    category: CognitiveSearch
    target: ${SEARCH_ENDPOINT}
    authType: ApiKey
    credentials:
      key: ${SEARCH_KEY}
    env:
      SEARCH_ENDPOINT: ${SEARCH_ENDPOINT}
      SEARCH_KEY: ${SEARCH_KEY}
```

`azd deploy search` creates or replaces this Connection. `azd up` provisions
the referenced Project first, then runs the same Connection deploy target before
dependents such as Toolboxes and Agents. A standalone `azd provision` creates
the Project infrastructure but does not reconcile split Connection services.

The target resolves `${VAR}` references from the service-level `env` block. If
the service omits `env`, it falls back to the environment selected for the
current invocation, including an explicit `-e` / `--environment`. Project
endpoint, project ID, subscription, and tenant lookup use that same environment.
An explicit empty `env: {}` creates an isolated scope.

Declarative services preserve every authentication type accepted by the schema,
including `AAD`, `PAT`, `ServicePrincipal`, `UsernamePassword`, `AccessKey`,
`AccountKey`, and `SAS`. The deploy target sends the complete credentials object
through the generic ARM request path instead of narrowing it to command-specific
credential flags.

Pre-split projects that bundle Connections on an Agent or legacy
`microsoft.foundry` service continue through the legacy provisioning adapter.
User-ejected Bicep or Terraform also retains its existing Connection inputs.

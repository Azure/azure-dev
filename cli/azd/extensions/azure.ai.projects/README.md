# Foundry Projects

Manage Microsoft Foundry Project resources from your terminal. (Preview)

## `azure.yaml` ownership

This extension owns `host: azure.ai.project` services and the
`microsoft.foundry` provisioning provider. A project service carries
account-level settings such as an existing project endpoint, model
deployments, and private networking.

```yaml
infra:
  provider: microsoft.foundry

services:
  my-project:
    host: azure.ai.project
    endpoint: https://my-account.services.ai.azure.com/api/projects/my-project
    deployments:
      - name: gpt-4.1-mini
        model:
          format: OpenAI
          name: gpt-4.1-mini
          version: "2025-04-14"
        sku:
          name: GlobalStandard
          capacity: 50
```

When `endpoint` is omitted, `azd provision` creates a Foundry account
and project. When it is set, provisioning reuses that project and
reconciles the declarations that can be applied to an existing account.

The `azd ai project set`, `show`, and `unset` commands manage the
default Foundry project endpoint context. They do not currently author
the project service in `azure.yaml`.

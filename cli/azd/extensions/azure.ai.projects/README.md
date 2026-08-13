# Foundry Projects

Manage Microsoft Foundry Project resources from your terminal. (Preview)

## `azure.yaml` ownership

This extension owns `host: azure.ai.project` services and the `microsoft.foundry` provisioning provider. A project service carries account-level settings such as an existing project endpoint, model deployments, and private networking.

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

When `endpoint` is omitted, `azd provision` creates a Foundry account and project. When it is set, provisioning reuses that project and reconciles the declarations that can be applied to an existing account.

## Project authoring

Initialize a new Foundry project in the current azd workspace:

```sh
azd ai project init
```

For a new project, managed deployment declarations can be added before the
first provision:

```sh
azd ai project init
azd ai project deployment add --model <model-name>
azd provision
```

To use an existing project in automation, initialize it with its full ARM
resource ID. This stores the project identity in the active azd environment
and allows managed deployment declarations to be reconciled:

```sh
azd ai project init --project-id "<project-resource-id>"
azd ai project deployment add --model <model-name>
```

An endpoint-only project is suitable for configuration that does not manage
resources on the existing project. Use the full project resource ID before
adding managed deployments.

To reconcile deployments, connections, or a pending container registry on an existing project, set the project's full ARM resource ID in the active azd environment:

```sh
azd env set AZURE_AI_PROJECT_ID "/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account>/projects/<project>"
```

`azd ai agent init` sets this value when initialized against an existing project. An endpoint-only service with no resources to reconcile does not require it.

The `azd ai project set`, `show`, and `unset` commands manage the default Foundry project endpoint context. They do not currently author the project service in `azure.yaml`.

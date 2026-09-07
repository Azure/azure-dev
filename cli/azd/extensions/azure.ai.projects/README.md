# Foundry Projects

Manage Microsoft Foundry Project resources from your terminal. (Preview)

See the shared [AI extension non-interactive input reference](../ai-non-interactive.md)
for every prompt's flag, environment/configuration input, or deterministic
no-prompt behavior.

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

### Managed deployments and references

`deployments` is azd-managed desired state: provisioning can create or update
those deployments. `deploymentReferences` is for user-owned deployments that
an agent should use without transferring ownership to azd. Reference metadata
is used only to resolve and validate the selected deployment for the active
environment; it never creates, updates, or deletes an Azure deployment.

For example, keep a deployment managed by another process outside of
`deployments`:

```yaml
services:
  my-project:
    host: azure.ai.project
    endpoint: https://my-account.services.ai.azure.com/api/projects/my-project
    deploymentReferences:
      - name: ${AZURE_AI_MODEL_DEPLOYMENT_NAME}
        model:
          name: ${AZURE_AI_MODEL_NAME}
          format: ${AZURE_AI_MODEL_FORMAT}
          version: ${AZURE_AI_MODEL_VERSION}
        sku:
          name: ${AZURE_AI_MODEL_SKU_NAME}
          capacity: ${AZURE_AI_MODEL_SKU_CAPACITY}
```

Each azd environment resolves its own complete tuple. A reference-only
configuration requires an existing target Foundry project and a deployment
already present in that account. If the active environment has no matching
tuple, interactive provisioning lets you select an existing target deployment;
it does not offer to create one.

Hosted-agent initialization stores generated deployment settings in the active
azd environment and writes references into `azure.yaml`. The first deployment
uses these keys; additional deployments use `_2`, `_3`, and so on:

```text
AZURE_AI_MODEL_DEPLOYMENT_NAME
AZURE_AI_MODEL_NAME
AZURE_AI_MODEL_FORMAT
AZURE_AI_MODEL_VERSION
AZURE_AI_MODEL_SKU_NAME
AZURE_AI_MODEL_SKU_CAPACITY
```

Provisioning validates each complete tuple against the selected subscription,
region, model catalog, and remaining quota. If the tuple is missing or no
longer compatible, interactive provisioning selects and persists a replacement.
Static literal deployments and custom environment references remain supported.

To reconcile deployments, deployment references, connections, or a pending
container registry on an existing project, set the project's full ARM resource
ID and matching endpoint in the active azd environment:

```sh
azd env set AZURE_AI_PROJECT_ID "/subscriptions/<subscription-id>/resourceGroups/<resource-group>/providers/Microsoft.CognitiveServices/accounts/<account>/projects/<project>"
azd env set FOUNDRY_PROJECT_ENDPOINT "https://<account>.services.ai.azure.com/api/projects/<project>"
```

`azd ai agent init` sets this value when initialized against an existing project. An endpoint-only service with no resources to reconcile does not require it.

When provisioning reports insufficient Cognitive Services quota, check usage for the target region with
`az cognitiveservices usage list --location <region>` or request a quota increase in the Azure portal. If an
existing Foundry project should be reused instead, configure its endpoint and set `AZURE_AI_PROJECT_ID` to the
full project resource ID before retrying.

The `azd ai project set`, `show`, and `unset` commands manage the default Foundry project endpoint context. They do not currently author the project service in `azure.yaml`.

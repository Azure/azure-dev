# AI Model Quota Preflight Test Samples

These samples exercise the `ai_model_quota` preflight validation check added to the
Bicep provider. They are used by functional tests in `preflight_quota_test.go`.

## Samples

### `rg-deployment/`

A **resource-group scoped** Bicep template that deploys a `Microsoft.CognitiveServices/accounts`
resource and two model deployments (a GPT model and an embedding model). All model parameters
(name, version, SKU, capacity) are configurable via Bicep parameters, which map to azd
environment variables.

Because this is an RG-scoped deployment, the resource location is inherited from
`resourceGroup().location`. An optional `aiDeploymentsLocation` parameter lets the test
override the deployment location independently.

### `sub-deployment/`

A **subscription-scoped** Bicep template that creates a resource group and deploys the same
cognitive services account + model deployments inside it via a module. The deployment location
is controlled by the `location` parameter (mapped to `AZURE_LOCATION`), and an optional
`aiDeploymentsLocation` parameter allows deploying models in a different region.

## What the tests cover

| Scenario | Expected diagnostic / behavior |
|---|---|
| Default parameters (capacity = 99999) | `ai_model_quota_exceeded` — capacity is absurdly high |
| Invalid model name (e.g. `gpt-nonexistent`) | `ai_model_not_found` — not in catalog |
| Invalid model version | `ai_model_not_found` — version not found |
| Subscription-scoped deployment with default location | Location resolved from `AZURE_LOCATION` |
| Subscription-scoped deployment with invalid model | `ai_model_not_found` in the subscription location |
| Subscription-scoped deployment with different `aiDeploymentsLocation` | Quota checked against the overridden deployment location |

## Parameter mapping

| Bicep parameter | Env var | Default |
|---|---|---|
| `gptModelName` | `GPT_MODEL_NAME` | `gpt-4o` |
| `gptModelVersion` | `GPT_MODEL_VERSION` | `2024-08-06` |
| `gptDeploymentType` | `GPT_DEPLOYMENT_TYPE` | `GlobalStandard` |
| `gptDeploymentCapacity` | `GPT_DEPLOYMENT_CAPACITY` | `99999` |
| `embeddingModelName` | `EMBEDDING_MODEL_NAME` | `text-embedding-3-small` |
| `embeddingDeploymentCapacity` | `EMBEDDING_DEPLOYMENT_CAPACITY` | `99999` |
| `aiDeploymentsLocation` | `AI_DEPLOYMENTS_LOCATION` | _(empty — uses RG/subscription location)_ |

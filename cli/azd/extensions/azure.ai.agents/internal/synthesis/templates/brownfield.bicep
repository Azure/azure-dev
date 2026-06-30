// Resource-group-scoped template: create/upsert model deployments on an
// EXISTING Foundry (AIServices) account. The account is REFERENCED, never
// created, so its owner-managed settings are untouched -- only the declared
// model deployments are reconciled. Deployed into the project's existing
// resource group by the provider's brownfield path (endpoint: + deployments:).

targetScope = 'resourceGroup'

// User-defined types (match the deploymentType in main.bicep).

@description('Shape of one model deployment entry in azure.yaml.')
type deploymentsType = deploymentType[]

@description('Shape of a single model deployment.')
type deploymentType = {
  name: string
  model: {
    name: string
    format: string
    version: string
  }
  sku: {
    name: string
    capacity: int
  }
}

// Parameters

@description('Name of the existing Foundry (AIServices) account.')
@minLength(2)
@maxLength(64)
param accountName string

@description('Model deployments to create or update on the existing account.')
param deployments deploymentsType = []

// Resources

resource foundryAccount 'Microsoft.CognitiveServices/accounts@2025-06-01' existing = {
  name: accountName
}

// Sequential creation; ARM throttles concurrent deployments on one account.
// CreateOrUpdate is an idempotent upsert, so re-running reconciles an existing
// deployment rather than duplicating it.
@batchSize(1)
resource modelDeployments 'Microsoft.CognitiveServices/accounts/deployments@2025-06-01' = [
  for d in deployments: {
    parent: foundryAccount
    name: d.name
    properties: {
      model: d.model
    }
    sku: d.sku
  }
]

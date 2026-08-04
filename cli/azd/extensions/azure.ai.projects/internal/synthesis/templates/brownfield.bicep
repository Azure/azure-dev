// Resource-group-scoped template for an EXISTING Foundry (AIServices) account.
// The account and project are REFERENCED, never created. It reconciles model
// deployments declared in azure.yaml and, when includeAcr is true, creates a
// container registry wired to the project (AcrPull + ContainerRegistry
// connection) for a hosted container agent. Used by the brownfield path.

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

@description('Shape of a list of Foundry project connections.')
type connectionsType = connectionType[]

@description('Shape of one Foundry project connection (a host: azure.ai.connection service).')
type connectionType = {
  name: string
  category: string
  target: string
  authType: string
  metadata: object?
}

// Parameters

@description('Name of the existing Foundry (AIServices) account.')
@minLength(2)
@maxLength(64)
param accountName string

@description('Name of the existing Foundry project that receives the ACR connection. Required when includeAcr is true.')
param projectName string = ''

@description('Model deployments to create or update on the existing account.')
param deployments deploymentsType = []

@description('Azure region for the container registry. Defaults to the resource group location.')
param location string = resourceGroup().location

@description('Tags applied to created resources.')
param tags object = {}

@description('Create an Azure Container Registry and wire it to the existing project. Set true for a hosted container agent.')
param includeAcr bool = false

@description('Container registry name. 5-50 alphanumeric chars. Required when includeAcr is true.')
param acrName string = ''

@description('Foundry project connections to create on the existing project (host: azure.ai.connection services).')
param connections connectionsType = []

@description('Credentials keyed by Foundry project connection name.')
@secure()
param connectionCredentials object = {}

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

// Existing project reference (preview API): exposes the project's system-assigned
// managed identity principal id, used as the AcrPull grantee and the connection
// credential identity. Pinned to 2025-04-01-preview to match acr.bicep; the GA
// API fails to resolve the projects/connections ContainerRegistry sub-resource.
resource foundryAccountPreview 'Microsoft.CognitiveServices/accounts@2025-04-01-preview' existing = {
  name: accountName

  resource project 'projects' existing = {
    name: projectName
  }
}

// Container registry for the hosted container agent. Premium SKU mirrors the
// greenfield acr.bicep.
resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' = if (includeAcr) {
  name: acrName
  location: location
  tags: tags
  sku: {
    name: 'Premium'
  }
  identity: {
    type: 'SystemAssigned'
  }
  properties: {
    adminUserEnabled: false
    publicNetworkAccess: 'Enabled'
    zoneRedundancy: 'Disabled'
  }
}

// Built-in AcrPull role. See: https://learn.microsoft.com/azure/role-based-access-control/built-in-roles
var acrPullRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '7f951dda-4ed3-4680-a7ca-43fe172d538d'
)

// The nested module makes the runtime project principal a deployment
// parameter. The assignment name can then include that principal.
module foundryAcrPull 'modules/acr-pull-role-assignment.bicep' = if (includeAcr) {
  name: 'foundry-acr-pull'
  params: {
    registryName: registry.name
    principalId: foundryAccountPreview::project.identity.principalId
    roleDefinitionId: acrPullRoleId
  }
}

// Project-scoped ContainerRegistry connection so Foundry can resolve the registry
// by name when running the hosted agent.
resource acrConnection 'Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview' = if (includeAcr) {
  name: '${accountName}/${projectName}/${acrName}-conn'
  properties: {
    category: 'ContainerRegistry'
    target: registry!.properties.loginServer
    authType: 'ManagedIdentity'
    credentials: {
      clientId: foundryAccountPreview::project.identity.principalId
      resourceId: registry!.id
    }
    isSharedToAll: true
    metadata: {
      ResourceId: registry!.id
    }
  }
  dependsOn: [
    foundryAcrPull
  ]
}

// Project connections (RemoteTool/MCP, CognitiveSearch, ...) declared as
// host: azure.ai.connection services, created on the existing project at
// provision time. Optional properties (credentials / metadata) are emitted only
// when supplied so None / identity-token connections don't send empty objects.
resource projectConnections 'Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview' = [
  for c in connections: {
    parent: foundryAccountPreview::project
    name: c.name
    properties: union(
      {
        category: c.category
        target: c.target
        authType: c.authType
      },
      contains(connectionCredentials, c.name)
        ? { credentials: connectionCredentials[c.name] }
        : {},
      c.?metadata != null ? { metadata: c.?metadata } : {}
    )
  }
]

// Outputs

output AZURE_CONTAINER_REGISTRY_ENDPOINT string = includeAcr ? registry!.properties.loginServer : ''
output AZURE_CONTAINER_REGISTRY_RESOURCE_ID string = includeAcr ? registry!.id : ''
output AZURE_AI_PROJECT_ACR_CONNECTION_NAME string = includeAcr ? '${acrName}-conn' : ''
output AZURE_AI_PROJECT_CONNECTION_NAMES string = join(map(connections, c => c.name), ',')

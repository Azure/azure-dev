// Azure Container Registry for hosted agents that use docker:.
// Wires the registry as a connection on the Foundry project so the
// project's managed identity can pull images.

@description('Azure region.')
param location string

@description('Tags applied to all resources.')
param tags object = {}

@description('Registry name (5-50 alphanumeric chars).')
param name string

@description('Foundry account ARM id; receives AcrPull role.')
param foundryAccountId string

@description('Foundry project name; receives the ACR connection.')
param foundryProjectName string

resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' = {
  name: name
  location: location
  tags: tags
  sku: { name: 'Premium' }
  properties: {
    adminUserEnabled: false
    publicNetworkAccess: 'Enabled'
    zoneRedundancy: 'Disabled'
  }
  identity: { type: 'SystemAssigned' }
}

// Grant the Foundry account's managed identity AcrPull on this registry.
var acrPullRoleId = '/subscriptions/${subscription().subscriptionId}/providers/Microsoft.Authorization/roleDefinitions/7f951dda-4ed3-4680-a7ca-43fe172d538d'

resource foundryAcrPull 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(registry.id, foundryAccountId, acrPullRoleId)
  scope: registry
  properties: {
    principalId: reference(foundryAccountId, '2025-06-01', 'Full').identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: acrPullRoleId
  }
}

// Project-scoped connection so Foundry can resolve the registry by name.
resource projectAcrConnection 'Microsoft.CognitiveServices/accounts/projects/connections@2025-06-01' = {
  name: '${split(foundryAccountId, '/')[8]}/${foundryProjectName}/${name}-conn'
  properties: {
    category: 'ContainerRegistry'
    target: registry.properties.loginServer
    authType: 'ManagedIdentity'
    isSharedToAll: true
    useWorkspaceManagedIdentity: false
    metadata: {
      ApiType: 'Azure'
      ResourceId: registry.id
    }
  }
  dependsOn: [ foundryAcrPull ]
}

output loginServer string = registry.properties.loginServer
output resourceId string = registry.id
output connectionName string = '${name}-conn'

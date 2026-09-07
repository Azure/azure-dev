targetScope = 'resourceGroup'

@allowed([
  'create'
  'reuse-connect'
])
param mode string
param location string
param tags object = {}
param registryName string
param projectPrincipalId string

var acrPullRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '7f951dda-4ed3-4680-a7ca-43fe172d538d'
)

resource newRegistry 'Microsoft.ContainerRegistry/registries@2023-07-01' = if (mode == 'create') {
  name: registryName
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

resource existingRegistry 'Microsoft.ContainerRegistry/registries@2023-07-01' existing = if (mode == 'reuse-connect') {
  name: registryName
}

resource newRegistryAccess 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (mode == 'create') {
  scope: newRegistry
  name: guid(newRegistry.id, projectPrincipalId, acrPullRoleId)
  properties: {
    principalId: projectPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: acrPullRoleId
  }
}

resource existingRegistryAccess 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (mode == 'reuse-connect') {
  scope: existingRegistry
  name: guid(existingRegistry.id, projectPrincipalId, acrPullRoleId)
  properties: {
    principalId: projectPrincipalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: acrPullRoleId
  }
}

output endpoint string = mode == 'create' ? newRegistry!.properties.loginServer : existingRegistry!.properties.loginServer
output resourceId string = mode == 'create' ? newRegistry!.id : existingRegistry!.id

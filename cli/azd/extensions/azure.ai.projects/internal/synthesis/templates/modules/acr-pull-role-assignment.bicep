targetScope = 'resourceGroup'

@description('Name of the Azure Container Registry.')
param registryName string

@description('Principal receiving AcrPull on the registry.')
param principalId string

@description('AcrPull role definition resource ID.')
param roleDefinitionId string

resource registry 'Microsoft.ContainerRegistry/registries@2023-07-01' existing = {
  name: registryName
}

resource acrPull 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(registry.id, principalId, roleDefinitionId)
  scope: registry
  properties: {
    principalId: principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: roleDefinitionId
  }
}

targetScope = 'resourceGroup'

@description('Name of the Azure Container Registry.')
param registryName string

@description('Principal receiving pull access on the registry.')
param principalId string

@description('Pull role definition resource ID (AcrPull or Container Registry Repository Reader).')
param roleDefinitionId string
param assignmentName string = ''
param ownershipDescription string = ''

resource registry 'Microsoft.ContainerRegistry/registries@2025-11-01' existing = {
  name: registryName
}

resource acrPull 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: empty(assignmentName) ? guid(registry.id, principalId, roleDefinitionId) : assignmentName
  scope: registry
  properties: {
    principalId: principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: roleDefinitionId
    description: ownershipDescription
  }
}

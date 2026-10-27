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
param pullAssigned bool = false
param accessOperationId string = ''

var acrPullRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  '7f951dda-4ed3-4680-a7ca-43fe172d538d'
)
// Container Registry Repository Reader — required when the registry uses
// AbacRepositoryPermissions (AcrPull is not honored in that mode).
var repositoryReaderRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'b93aa761-3e63-49ed-ac28-beffa264f7ac'
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

resource existingRegistry 'Microsoft.ContainerRegistry/registries@2025-11-01' existing = if (mode == 'reuse-connect') {
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

// Nested so roleDefinitionId can be chosen from the registry's roleAssignmentMode
// (Bicep forbids runtime existing-resource properties in resource conditions/names).
module existingRegistryAccess 'acr-pull-role-assignment.bicep' = if (mode == 'reuse-connect' && !pullAssigned) {
  name: empty(accessOperationId) ? 'existing-acr-access-${uniqueString(projectPrincipalId, registryName)}' : 'azd-acr-access-${accessOperationId}'
  params: {
    registryName: registryName
    principalId: projectPrincipalId
    assignmentName: accessOperationId
    ownershipDescription: empty(accessOperationId) ? '' : 'azd-foundry:${accessOperationId}'
    roleDefinitionId: existingRegistry!.properties.roleAssignmentMode == 'AbacRepositoryPermissions'
      ? repositoryReaderRoleId
      : acrPullRoleId
  }
}

output endpoint string = mode == 'create' ? newRegistry!.properties.loginServer : existingRegistry!.properties.loginServer
output resourceId string = mode == 'create' ? newRegistry!.id : existingRegistry!.id

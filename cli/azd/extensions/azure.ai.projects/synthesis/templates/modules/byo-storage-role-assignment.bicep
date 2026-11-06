targetScope = 'resourceGroup'

param resourceName string
param principalId string
param roleDefinitionId string

resource target 'Microsoft.Storage/storageAccounts@2023-05-01' existing = {
  name: resourceName
}

resource assignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(target.id, principalId, roleDefinitionId)
  scope: target
  properties: {
    principalId: principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: roleDefinitionId
  }
}

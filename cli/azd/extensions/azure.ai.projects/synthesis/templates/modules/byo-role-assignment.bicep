targetScope = 'resourceGroup'

param resourceName string
param principalId string
param roleDefinitionId string
param existingRoleAssignmentKeys string[] = []

resource target 'Microsoft.ContainerService/managedClusters@2025-01-01' existing = {
  name: resourceName
}

var assignmentKey = toLower('${target.id}|${principalId}|${last(split(roleDefinitionId, '/'))}')

resource assignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = if (!contains(existingRoleAssignmentKeys, assignmentKey)) {
  name: guid(target.id, principalId, roleDefinitionId)
  scope: target
  properties: {
    principalId: principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: roleDefinitionId
  }
}

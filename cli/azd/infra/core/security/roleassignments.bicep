param managedIdentityPrincipalID string
param managedIdentityID string

// Assign the Contributor role to the managed identity at the resource group level
resource roleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(managedIdentityID, 'Contributor')
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'b24988ac-6180-42a0-ab88-20f7382dd24c') // Contributor role ID
    principalType: 'ServicePrincipal'
    principalId: managedIdentityPrincipalID
  }
}

// Assign the Contributor role to the managed identity at the resource group level
resource roleAssignment2 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  name: guid(managedIdentityID, 'CognitiveServicesUser')
  properties: {
    roleDefinitionId: subscriptionResourceId('Microsoft.Authorization/roleDefinitions', '5e0bd9bd-7b93-4f28-af87-19fc36ad61bd') // Cognitive Services User role ID
    principalType: 'ServicePrincipal'
    principalId: managedIdentityPrincipalID
  }
}

@description('The name of the AI Services account')
param aiServicesName string

@description('The name of the AI Services project')
param aiServicesProjectName string

@description('The name of the AI Search')
param aiSearchName string

resource search 'Microsoft.Search/searchServices@2025-02-01-preview' existing = {
  name: aiSearchName
}

resource aiServices 'Microsoft.CognitiveServices/accounts@2025-04-01-preview' existing = {
  name: aiServicesName

  resource project 'projects' existing = {
    name: aiServicesProjectName

    resource AzureAISearch 'connections' = {
      name: 'AzureAISearch-connection'
      properties: {
        category: 'CognitiveSearch'
        target: search.properties.endpoint
        authType: 'AAD'
        isSharedToAll: true
        metadata: {
          ApiType: 'Azure'
          ResourceId: search.id
          location: search.location
        }
      }
    }
  }
}

resource projectSearchIndexDataContributorAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: search
  name: guid(subscription().id, resourceGroup().id, aiServices::project.id, '8ebe5a00-799e-43f5-93ac-243d3dce84a7')
  properties: {
    principalId: aiServices::project.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', '8ebe5a00-799e-43f5-93ac-243d3dce84a7')
  }
}

resource projectSearchServiceContributorRoleAssignment 'Microsoft.Authorization/roleAssignments@2022-04-01' = {
  scope: search
  name: guid(subscription().id, resourceGroup().id, aiServices::project.id, '7ca78c08-252a-4471-8644-bb5ff32d4ba0')
  properties: {
    principalId: aiServices::project.identity.principalId
    principalType: 'ServicePrincipal'
    roleDefinitionId: resourceId('Microsoft.Authorization/roleDefinitions', '7ca78c08-252a-4471-8644-bb5ff32d4ba0')
  }
}

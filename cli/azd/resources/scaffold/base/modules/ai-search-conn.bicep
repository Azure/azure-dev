@description('The name of the main AI Services')
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

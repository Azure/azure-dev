// Resolve the credential inside ARM and never update an existing connection.
targetScope = 'resourceGroup'

param accountName string
param projectName string
param applicationInsightsResourceId string
param environmentName string

var componentParts = split(applicationInsightsResourceId, '/')

resource account 'Microsoft.CognitiveServices/accounts@2025-04-01-preview' existing = {
  name: accountName
  resource project 'projects' existing = {
    name: projectName
  }
}

resource component 'Microsoft.Insights/components@2020-02-02' existing = {
  name: componentParts[8]
  scope: resourceGroup(componentParts[2], componentParts[4])
}

@onlyIfNotExists()
resource connection 'Microsoft.CognitiveServices/accounts/projects/connections@2025-04-01-preview' = {
  parent: account::project
  name: 'azd-appinsights-${take(replace(guid(toLower(projectName)), '-', ''), 13)}'
  properties: {
    category: 'AppInsights'
    authType: 'ApiKey'
    target: component.id
    credentials: {
      key: component.properties.ConnectionString
    }
    metadata: {
      ApiType: 'Azure'
      ResourceId: component.id
      'azd-created-by': 'azure.ai.projects'
      'azd-environment': environmentName
      'azd-monitoring-project': account::project.id
    }
  }
}

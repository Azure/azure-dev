targetScope = 'resourceGroup'

@allowed([
  'none'
  'create'
  'reuse-connect'
  'already-connected'
])
param mode string
param location string
param tags object = {}
param projectResourceId string
param applicationInsightsResourceId string = ''
param createConnection bool = true
param existingConnectionName string = ''
param environmentName string
param resourceTokenSalt string = ''

var createResources = mode == 'create'
var token = uniqueString(projectResourceId, resourceGroup().id, resourceTokenSalt)
var projectParts = split(projectResourceId, '/')
var componentParts = split(applicationInsightsResourceId, '/')
var connectionName = 'azd-appinsights-${take(replace(guid(toLower(projectParts[10])), '-', ''), 13)}'
var monitoringTags = union(tags, {
  'azd-created-by': 'azure.ai.projects'
  'azd-environment': environmentName
  'azd-monitoring-project': projectResourceId
})

resource workspace 'Microsoft.OperationalInsights/workspaces@2023-09-01' = if (createResources) {
  name: 'log-${token}'
  location: location
  tags: monitoringTags
  properties: {
    sku: {
      name: 'PerGB2018'
    }
    retentionInDays: 30
  }
}

resource component 'Microsoft.Insights/components@2020-02-02' = if (createResources) {
  name: 'appi-${token}'
  location: location
  tags: monitoringTags
  kind: 'web'
  properties: {
    Application_Type: 'web'
    WorkspaceResourceId: workspace!.id
  }
}

resource existingComponent 'Microsoft.Insights/components@2020-02-02' existing = if (mode != 'none' && !createResources) {
  name: componentParts[8]
  scope: resourceGroup(componentParts[2], componentParts[4])
}

var effectiveComponentId = mode == 'none' ? '' : (createResources ? component!.id : existingComponent!.id)

module connection 'monitoring-connection.bicep' = if (createConnection && (mode == 'create' || mode == 'reuse-connect')) {
  name: 'foundry-monitoring-connection'
  scope: resourceGroup(projectParts[2], projectParts[4])
  params: {
    accountName: projectParts[8]
    projectName: projectParts[10]
    applicationInsightsResourceId: effectiveComponentId
    environmentName: environmentName
  }
}

output applicationInsightsResourceId string = effectiveComponentId
output applicationInsightsAppId string = mode == 'none' ? '' : (createResources ? component!.properties.AppId : existingComponent!.properties.AppId)
output logAnalyticsWorkspaceResourceId string = mode == 'none' ? '' : (createResources ? component!.properties.WorkspaceResourceId : existingComponent!.properties.WorkspaceResourceId)
output projectConnectionName string = mode == 'none' ? '' : (createConnection && mode != 'already-connected' ? connectionName : existingConnectionName)

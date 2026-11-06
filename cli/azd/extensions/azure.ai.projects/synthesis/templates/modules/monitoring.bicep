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

@description('When true, new monitoring resources accept ingestion and queries only through an Azure Monitor private link.')
param privateNetwork bool = false
param vnetId string = ''
param vnetLocation string = ''
param peSubnetId string = ''
param dnsZonesResourceGroup string = ''
param dnsZonesSubscription string = ''

var createResources = mode == 'create'
var publicNetworkAccess = privateNetwork ? 'Disabled' : 'Enabled'
var token = uniqueString(projectResourceId, resourceGroup().id, resourceTokenSalt)
var projectParts = split(projectResourceId, '/')
var componentParts = split(applicationInsightsResourceId, '/')
var connectionName = 'azd-appinsights-${take(replace(guid(toLower(projectParts[10])), '-', ''), 13)}'
var monitoringTags = union(tags, {
  'azd-created-by': 'azure.ai.projects'
  'azd-environment': environmentName
  // A bounded hash: the raw project ID can exceed the 256-character tag value limit.
  'azd-monitoring-project': guid(toLower(projectResourceId))
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
    publicNetworkAccessForIngestion: publicNetworkAccess
    publicNetworkAccessForQuery: 'Enabled'
  }
}

module privateLink 'monitor-private-link.bicep' = if (createResources && privateNetwork) {
  name: 'foundry-monitoring-private-link'
  params: {
    location: vnetLocation
    suffix: token
    applicationInsightsResourceId: component!.id
    logAnalyticsWorkspaceResourceId: workspace!.id
    vnetId: vnetId
    peSubnetId: peSubnetId
    dnsZonesResourceGroup: dnsZonesResourceGroup
    dnsZonesSubscription: dnsZonesSubscription
  }
}

module resync 'monitoring-resync.bicep' = if (createResources && privateNetwork) {
  name: 'foundry-monitoring-resync'
  params: {
    name: 'appi-${token}'
    location: location
    tags: monitoringTags
    workspaceResourceId: workspace!.id
    publicNetworkAccessForIngestion: publicNetworkAccess
    publicNetworkAccessForQuery: 'Enabled'
    privateLinkScopeResourceId: privateLink!.outputs.azureMonitorPrivateLinkScopeResourceId
  }
}

resource existingComponent 'Microsoft.Insights/components@2020-02-02' existing = if (mode != 'none' && !createResources) {
  name: componentParts[8]
  scope: resourceGroup(componentParts[2], componentParts[4])
}

// Routing through the resync output makes the connection wait for the second component update.
var effectiveComponentId = mode == 'none' ? '' : (createResources ? (privateNetwork ? resync!.outputs.id : component!.id) : existingComponent!.id)

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

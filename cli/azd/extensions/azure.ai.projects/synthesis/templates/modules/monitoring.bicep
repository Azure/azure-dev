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

@description('When true, new monitoring resources accept ingestion only through an Azure Monitor private link. Queries stay public.')
param privateNetwork bool = false
param vnetId string = ''
param vnetLocation string = ''
param peSubnetId string = ''
param dnsZonesResourceGroup string = ''
param dnsZonesSubscription string = ''

@description('When true, the project uses managed egress and needs an outbound rule so the managed network can reach the private link scope.')
param managedEgressRule bool = false

// Azure AI Enterprise Network Connection Approver: lets the account approve the managed private endpoint.
var networkConnectionApproverRoleId = 'b556d68e-0be0-4f35-a333-ad7ee1ce17ea'

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
    publicNetworkAccessForIngestion: publicNetworkAccess
    publicNetworkAccessForQuery: 'Enabled'
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

module privateLink 'monitor-private-link.bicep' = if ((createResources || mode == 'reuse-connect') && privateNetwork) {
  name: 'foundry-monitoring-private-link'
  params: {
    location: vnetLocation
    suffix: token
    applicationInsightsResourceId: createResources ? component!.id : existingComponent!.id
    logAnalyticsWorkspaceResourceId: createResources ? workspace!.id : existingComponent!.properties.WorkspaceResourceId
    vnetId: vnetId
    peSubnetId: peSubnetId
    dnsZonesResourceGroup: dnsZonesResourceGroup
    dnsZonesSubscription: dnsZonesSubscription
  }
}

resource foundryAccount 'Microsoft.CognitiveServices/accounts@2025-06-01' existing = if (managedEgressRule) {
  name: projectParts[8]
}

module networkApproverAssignment 'network-approver-role-assignment.bicep' = if (managedEgressRule) {
  name: 'foundry-monitoring-network-approver'
  params: {
    principalId: foundryAccount!.identity.principalId
    roleDefinitionId: networkConnectionApproverRoleId
  }
}

#disable-next-line BCP081
resource managedNetworkRule 'Microsoft.CognitiveServices/accounts/managedNetworks/outboundRules@2025-10-01-preview' = if (managedEgressRule) {
  name: '${projectParts[8]}/default/ampls-monitor-rule'
  properties: {
    type: 'PrivateEndpoint'
    destination: {
      serviceResourceId: privateLink!.outputs.azureMonitorPrivateLinkScopeResourceId
      subresourceTarget: 'azuremonitor'
    }
    category: 'UserDefined'
  }
  dependsOn: [networkApproverAssignment]
}

// Hosted agents call back into the account from the managed network; without a
// private endpoint rule to the account itself they fail with "Public access is disabled".
#disable-next-line BCP081
resource accountSelfRule 'Microsoft.CognitiveServices/accounts/managedNetworks/outboundRules@2025-10-01-preview' = if (managedEgressRule) {
  name: '${projectParts[8]}/default/foundry-account-pe'
  properties: {
    type: 'PrivateEndpoint'
    destination: {
      serviceResourceId: foundryAccount.id
      subresourceTarget: 'account'
    }
    category: 'UserDefined'
  }
  dependsOn: [managedNetworkRule]
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

output privateLinkScopeResourceId string = (mode == 'create' || mode == 'reuse-connect') && privateNetwork ? privateLink!.outputs.azureMonitorPrivateLinkScopeResourceId : ''
output applicationInsightsResourceId string = effectiveComponentId
output applicationInsightsAppId string = mode == 'none' ? '' : (createResources ? component!.properties.AppId : existingComponent!.properties.AppId)
output logAnalyticsWorkspaceResourceId string = mode == 'none' ? '' : (createResources ? component!.properties.WorkspaceResourceId : existingComponent!.properties.WorkspaceResourceId)
output projectConnectionName string = mode == 'none' ? '' : (createConnection && mode != 'already-connected' ? connectionName : existingConnectionName)

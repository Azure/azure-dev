// Private Azure Monitor path for a network-secured Foundry project: an Azure
// Monitor Private Link Scope (AMPLS) holding the App Insights component and its
// Log Analytics workspace, an 'azuremonitor' private endpoint in the PE subnet,
// and the four Azure Monitor private DNS zones.
//
// Matches the Foundry samples (15/17/19): ingestion is PrivateOnly, query access
// is Open, and the component's own public ingestion is disabled while its public
// query stays enabled. Like private-endpoint-dns.bicep, zones
// are created and linked to the VNet by default; when dnsZonesResourceGroup is
// set they are referenced from that resource group (in dnsZonesSubscription,
// defaulting to this subscription) instead of being created.
//
// A VNet can connect to only one AMPLS. The caller checks that the VNet has no
// 'azuremonitor' private endpoint before deploying this module.

targetScope = 'resourceGroup'

@description('Azure region of the customer VNet; the private endpoint must be in the same region.')
param location string

@description('Suffix for unique resource and link names.')
param suffix string

@description('ARM resource id of the Application Insights component to scope.')
param applicationInsightsResourceId string

@description('ARM resource id of the Log Analytics workspace to scope.')
param logAnalyticsWorkspaceResourceId string

@description('ARM resource id of the customer VNet.')
param vnetId string

@description('ARM resource id of the private-endpoint subnet.')
param peSubnetId string

@description('Resource group holding existing private DNS zones. Empty creates and links new zones.')
param dnsZonesResourceGroup string = ''

@description('Subscription holding existing private DNS zones. Empty defaults to this subscription.')
param dnsZonesSubscription string = ''

var monitorDnsZoneNames = [
  'privatelink.monitor.azure.com'
  'privatelink.oms.opinsights.azure.com'
  'privatelink.ods.opinsights.azure.com'
  'privatelink.agentsvc.azure-automation.net'
]

var useExistingZones = !empty(dnsZonesResourceGroup)
var existingZonesSubscription = empty(dnsZonesSubscription) ? subscription().subscriptionId : dnsZonesSubscription
var scopeName = 'ampls-${suffix}'

resource ampls 'Microsoft.Insights/privateLinkScopes@2021-07-01-preview' = {
  name: scopeName
  location: 'global'
  properties: {
    accessModeSettings: {
      ingestionAccessMode: 'PrivateOnly'
      queryAccessMode: 'Open'
    }
  }
}

resource scopedApplicationInsights 'Microsoft.Insights/privateLinkScopes/scopedResources@2021-07-01-preview' = {
  parent: ampls
  name: 'appinsights-scoped'
  properties: {
    linkedResourceId: applicationInsightsResourceId
  }
}

resource scopedWorkspace 'Microsoft.Insights/privateLinkScopes/scopedResources@2021-07-01-preview' = {
  parent: ampls
  name: 'workspace-scoped'
  properties: {
    linkedResourceId: logAnalyticsWorkspaceResourceId
  }
  dependsOn: [
    scopedApplicationInsights
  ]
}

// ---- Private DNS zones: create-and-link, or reference existing ----

resource zones 'Microsoft.Network/privateDnsZones@2020-06-01' = [for zone in monitorDnsZoneNames: if (!useExistingZones) {
  name: zone
  location: 'global'
}]

resource zoneLinks 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2024-06-01' = [for (zone, i) in monitorDnsZoneNames: if (!useExistingZones) {
  parent: zones[i]
  name: '${replace(zone, '.', '-')}-${suffix}-link'
  location: 'global'
  properties: {
    virtualNetwork: {
      id: vnetId
    }
    registrationEnabled: false
  }
}]

var zoneIds = [for zone in monitorDnsZoneNames: useExistingZones
  ? extensionResourceId('/subscriptions/${existingZonesSubscription}/resourceGroups/${dnsZonesResourceGroup}', 'Microsoft.Network/privateDnsZones', zone)
  : resourceId('Microsoft.Network/privateDnsZones', zone)]

// ---- Private endpoint to the AMPLS, group 'azuremonitor' ----

resource privateEndpoint 'Microsoft.Network/privateEndpoints@2024-05-01' = {
  name: '${scopeName}-private-endpoint'
  location: location
  properties: {
    subnet: {
      id: peSubnetId
    }
    privateLinkServiceConnections: [
      {
        name: '${scopeName}-private-link-service-connection'
        properties: {
          privateLinkServiceId: ampls.id
          groupIds: [
            'azuremonitor'
          ]
        }
      }
    ]
  }
  dependsOn: [
    scopedApplicationInsights
    scopedWorkspace
  ]
}

resource dnsZoneGroup 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2024-05-01' = {
  parent: privateEndpoint
  name: '${scopeName}-dns-group'
  properties: {
    privateDnsZoneConfigs: [for (zone, i) in monitorDnsZoneNames: {
      name: replace(zone, '.', '-')
      properties: {
        privateDnsZoneId: zoneIds[i]
      }
    }]
  }
  dependsOn: [
    zoneLinks
  ]
}

output azureMonitorPrivateLinkScopeResourceId string = ampls.id

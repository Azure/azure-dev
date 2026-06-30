// Account private endpoint + the three AI private DNS zones for a
// network-secured Foundry account. Dependent stores stay platform-managed, so
// only the account itself gets a private endpoint here (no Search / Storage /
// Cosmos endpoints).
//
// DNS zones are created and linked to the VNet by default. When
// dnsZonesResourceGroup is set, the zones are referenced from that resource
// group (in dnsZonesSubscription, defaulting to this subscription) instead of
// being created.

targetScope = 'resourceGroup'

@description('Name of the Foundry (AIServices) account to bind the private endpoint to.')
param aiAccountName string

@description('ARM resource id of the customer VNet.')
param vnetId string

@description('ARM resource id of the private-endpoint subnet.')
param peSubnetId string

@description('Suffix for unique resource/link names.')
param suffix string

@description('Resource group holding existing private DNS zones. Empty creates and links new zones.')
param dnsZonesResourceGroup string = ''

@description('Subscription holding existing private DNS zones. Empty defaults to this subscription.')
param dnsZonesSubscription string = ''

var aiServicesDnsZoneName = 'privatelink.services.ai.azure.com'
var openAiDnsZoneName = 'privatelink.openai.azure.com'
var cognitiveServicesDnsZoneName = 'privatelink.cognitiveservices.azure.com'

var useExistingZones = !empty(dnsZonesResourceGroup)
var existingZonesSubscription = empty(dnsZonesSubscription) ? subscription().subscriptionId : dnsZonesSubscription

resource aiAccount 'Microsoft.CognitiveServices/accounts@2025-06-01' existing = {
  name: aiAccountName
  scope: resourceGroup()
}

// Account private endpoint in the PE subnet, targeting the 'account' group.
resource aiAccountPrivateEndpoint 'Microsoft.Network/privateEndpoints@2024-05-01' = {
  name: '${aiAccountName}-private-endpoint'
  location: resourceGroup().location
  properties: {
    subnet: {
      id: peSubnetId
    }
    privateLinkServiceConnections: [
      {
        name: '${aiAccountName}-private-link-service-connection'
        properties: {
          privateLinkServiceId: aiAccount.id
          groupIds: [
            'account'
          ]
        }
      }
    ]
  }
}

// ---- Private DNS zones: create-and-link, or reference existing ----

resource aiServicesZone 'Microsoft.Network/privateDnsZones@2020-06-01' = if (!useExistingZones) {
  name: aiServicesDnsZoneName
  location: 'global'
}
resource existingAiServicesZone 'Microsoft.Network/privateDnsZones@2020-06-01' existing = if (useExistingZones) {
  name: aiServicesDnsZoneName
  scope: resourceGroup(existingZonesSubscription, dnsZonesResourceGroup)
}
var aiServicesZoneId = useExistingZones ? existingAiServicesZone.id : aiServicesZone.id

resource openAiZone 'Microsoft.Network/privateDnsZones@2020-06-01' = if (!useExistingZones) {
  name: openAiDnsZoneName
  location: 'global'
}
resource existingOpenAiZone 'Microsoft.Network/privateDnsZones@2020-06-01' existing = if (useExistingZones) {
  name: openAiDnsZoneName
  scope: resourceGroup(existingZonesSubscription, dnsZonesResourceGroup)
}
var openAiZoneId = useExistingZones ? existingOpenAiZone.id : openAiZone.id

resource cognitiveServicesZone 'Microsoft.Network/privateDnsZones@2020-06-01' = if (!useExistingZones) {
  name: cognitiveServicesDnsZoneName
  location: 'global'
}
resource existingCognitiveServicesZone 'Microsoft.Network/privateDnsZones@2020-06-01' existing = if (useExistingZones) {
  name: cognitiveServicesDnsZoneName
  scope: resourceGroup(existingZonesSubscription, dnsZonesResourceGroup)
}
var cognitiveServicesZoneId = useExistingZones ? existingCognitiveServicesZone.id : cognitiveServicesZone.id

// ---- VNet links (only when we create the zones) ----

resource aiServicesLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2024-06-01' = if (!useExistingZones) {
  parent: aiServicesZone
  name: 'aiServices-${suffix}-link'
  location: 'global'
  properties: {
    virtualNetwork: {
      id: vnetId
    }
    registrationEnabled: false
  }
}
resource openAiLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2024-06-01' = if (!useExistingZones) {
  parent: openAiZone
  name: 'aiServicesOpenAI-${suffix}-link'
  location: 'global'
  properties: {
    virtualNetwork: {
      id: vnetId
    }
    registrationEnabled: false
  }
}
resource cognitiveServicesLink 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2024-06-01' = if (!useExistingZones) {
  parent: cognitiveServicesZone
  name: 'aiServicesCognitiveServices-${suffix}-link'
  location: 'global'
  properties: {
    virtualNetwork: {
      id: vnetId
    }
    registrationEnabled: false
  }
}

// ---- DNS zone group binds the three zones to the account endpoint ----

resource aiAccountDnsGroup 'Microsoft.Network/privateEndpoints/privateDnsZoneGroups@2024-05-01' = {
  parent: aiAccountPrivateEndpoint
  name: '${aiAccountName}-dns-group'
  properties: {
    privateDnsZoneConfigs: [
      {
        name: '${aiAccountName}-dns-aiserv-config'
        properties: {
          privateDnsZoneId: aiServicesZoneId
        }
      }
      {
        name: '${aiAccountName}-dns-openai-config'
        properties: {
          privateDnsZoneId: openAiZoneId
        }
      }
      {
        name: '${aiAccountName}-dns-cogserv-config'
        properties: {
          privateDnsZoneId: cognitiveServicesZoneId
        }
      }
    ]
  }
  dependsOn: [
    aiServicesLink
    openAiLink
    cognitiveServicesLink
  ]
}

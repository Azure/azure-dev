param dnsZoneName string
param principalId string
param isPrivate bool
param vnetId string = ''

resource dns 'Microsoft.Network/dnsZones@2018-05-01' existing = if (!isPrivate) {
  name: dnsZoneName
}

resource privateDns 'Microsoft.Network/privateDnsZones@2020-06-01' existing = if (isPrivate) {
  name: dnsZoneName
}

var DNSZoneContributor = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'befefa01-2a29-4197-83a8-272ff33ce314')
resource dnsContributor 'Microsoft.Authorization/roleAssignments@2020-04-01-preview' = if (!isPrivate) {
  scope: dns
  name: guid(dns.id, principalId, DNSZoneContributor)
  properties: {
    roleDefinitionId: DNSZoneContributor
    principalType: 'ServicePrincipal'
    principalId: principalId
  }
}

var PrivateDNSZoneContributor = subscriptionResourceId('Microsoft.Authorization/roleDefinitions', 'b12aa53e-6015-4669-85d0-8515ebb3ae7f')
resource privateDnsContributor 'Microsoft.Authorization/roleAssignments@2020-04-01-preview' = if (isPrivate) {
  scope: privateDns
  name: guid(privateDns.id, principalId, PrivateDNSZoneContributor)
  properties: {
    roleDefinitionId: PrivateDNSZoneContributor
    principalType: 'ServicePrincipal'
    principalId: principalId
  }
}

resource dns_vnet_link 'Microsoft.Network/privateDnsZones/virtualNetworkLinks@2020-06-01' = if (isPrivate && !empty(vnetId)) {
  parent: privateDns
  name: 'privatedns'
  tags: {}
  location: 'global'
  properties: {
    virtualNetwork: {
      id: vnetId
    }
    registrationEnabled: false
  }
}

//This module facilitates RBAC assigned to specific DNS Zones.
//The DNS Zone Id is extracted and the scope is set correctly.

@description('The full Azure resource ID of the DNS zone to use for the AKS cluster')
param dnsZoneId string

@description('The id of a virtual network to be linked to a PRIVATE DNS Zone')
param vnetId string

@description('The AAD identity to create the RBAC against')
param principalId string

var dnsZoneRg = !empty(dnsZoneId) ? split(dnsZoneId, '/')[4] : ''
var dnsZoneName = !empty(dnsZoneId) ? split(dnsZoneId, '/')[8] : ''
var isDnsZonePrivate = !empty(dnsZoneId) ? split(dnsZoneId, '/')[7] == 'privateDnsZones' : false

module dnsZone './dnsZone.bicep' = if (!empty(dnsZoneId)) {
  name: 'dns-${dnsZoneName}'
  scope: resourceGroup(dnsZoneRg)
  params: {
    dnsZoneName: dnsZoneName
    isPrivate: isDnsZonePrivate
    vnetId : vnetId
    principalId: principalId
  }
}

targetScope = 'resourceGroup'

param location string
param tags object
param clusterResourceId string
param storageAccountName string
param hostingIdentityName string
param workloadIdentityName string
param federatedIdentityRoleDefinitionId string
param hostingIdentityResourceId string = ''
param workloadIdentityResourceId string = ''
param storageAccountResourceId string = ''
param suppliedHostingPrincipalId string = ''
param suppliedWorkloadPrincipalId string = ''

var clusterParts = split(clusterResourceId, '/')
var workloadParts = split(empty(workloadIdentityResourceId)
  ? resourceId('Microsoft.ManagedIdentity/userAssignedIdentities', workloadIdentityName)
  : workloadIdentityResourceId, '/')
var storageParts = split(empty(storageAccountResourceId)
  ? resourceId('Microsoft.Storage/storageAccounts', storageAccountName)
  : storageAccountResourceId, '/')
var hostingPrincipalId = empty(hostingIdentityResourceId) ? hosting!.properties.principalId : suppliedHostingPrincipalId
var workloadPrincipalId = empty(workloadIdentityResourceId) ? workload!.properties.principalId : suppliedWorkloadPrincipalId
var storageZrsUnsupportedRegions = [
  'australiacentral'
  'australiacentral2'
  'australiasoutheast'
  'brazilsoutheast'
  'canadaeast'
  'francesouth'
  'germanynorth'
  'koreasouth'
  'northcentralus'
  'norwaywest'
  'southafricawest'
  'southindia'
  'swedensouth'
  'switzerlandwest'
  'taiwannorth'
  'taiwannorthwest'
  'uaecentral'
  'ukwest'
  'westcentralus'
  'westindia'
  'westus'
]
var readerRoleId = 'acdd72a7-3385-48ef-bd42-f606fba81ae7'
var kubernetesExtensionContributorRoleId = '85cb6faf-e071-4c9b-8136-154b5a04f717'
var blobContributorRoleId = subscriptionResourceId(
  storageParts[2], 'Microsoft.Authorization/roleDefinitions', 'ba92f5b4-2d11-453d-a403-e96b0029c9fe'
)

resource hosting 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = if (empty(hostingIdentityResourceId)) {
  name: hostingIdentityName
  location: location
  tags: tags
}

resource workload 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = if (empty(workloadIdentityResourceId)) {
  name: workloadIdentityName
  location: location
  tags: tags
}

resource storage 'Microsoft.Storage/storageAccounts@2023-05-01' = if (empty(storageAccountResourceId)) {
  name: storageAccountName
  location: location
  tags: tags
  kind: 'StorageV2'
  sku: { name: contains(storageZrsUnsupportedRegions, toLower(location)) ? 'Standard_LRS' : 'Standard_ZRS' }
  properties: {
    accessTier: 'Hot'
    isHnsEnabled: false
    publicNetworkAccess: 'Enabled'
    networkAcls: { defaultAction: 'Allow' }
    minimumTlsVersion: 'TLS1_2'
    supportsHttpsTrafficOnly: true
    allowBlobPublicAccess: false
    allowSharedKeyAccess: false
  }
}

module clusterGrants 'byo-role-assignment.bicep' = [
  for role in [
    readerRoleId
    kubernetesExtensionContributorRoleId
  ]: if (empty(hostingIdentityResourceId)) {
    name: 'byo-cluster-${uniqueString(resourceGroup().id, hostingIdentityName)}-${role}'
    scope: resourceGroup(clusterParts[2], clusterParts[4])
    params: {
      resourceName: last(clusterParts)
      principalId: hostingPrincipalId
      roleDefinitionId: subscriptionResourceId(clusterParts[2], 'Microsoft.Authorization/roleDefinitions', role)
    }
  }
]

module federationGrant 'byo-identity-role-assignment.bicep' = if (empty(hostingIdentityResourceId) || empty(workloadIdentityResourceId)) {
  name: empty(workloadIdentityResourceId)
    ? 'byo-federation-access'
    : 'byo-federation-access-${uniqueString(resourceGroup().id, hostingIdentityName)}'
  scope: resourceGroup(workloadParts[2], workloadParts[4])
  params: {
    resourceName: last(workloadParts)
    principalId: hostingPrincipalId
    roleDefinitionId: federatedIdentityRoleDefinitionId
  }
  dependsOn: [workload]
}

module storageGrant 'byo-storage-role-assignment.bicep' = if (empty(workloadIdentityResourceId) || empty(storageAccountResourceId)) {
  name: empty(storageAccountResourceId)
    ? 'byo-storage-access'
    : 'byo-storage-access-${uniqueString(resourceGroup().id, workloadIdentityName)}'
  scope: resourceGroup(storageParts[2], storageParts[4])
  params: {
    resourceName: last(storageParts)
    principalId: workloadPrincipalId
    roleDefinitionId: blobContributorRoleId
  }
  dependsOn: [storage]
}

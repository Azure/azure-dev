targetScope = 'resourceGroup'

param location string
param tags object
param clusterResourceId string
param plan object

var clusterParts = split(clusterResourceId, '/')
var blobContributorRoleId = subscriptionResourceId(
  'Microsoft.Authorization/roleDefinitions',
  'ba92f5b4-2d11-453d-a403-e96b0029c9fe'
)

resource hosting 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: plan.hostingIdentityName
  location: location
  tags: tags
}

resource workload 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: plan.workloadIdentityName
  location: location
  tags: tags
}

resource storage 'Microsoft.Storage/storageAccounts@2023-05-01' = {
  name: plan.storageName
  location: location
  tags: tags
  kind: 'StorageV2'
  sku: { name: plan.storageSku }
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
    'acdd72a7-3385-48ef-bd42-f606fba81ae7'
    '85cb6faf-e071-4c9b-8136-154b5a04f717'
  ]: {
    name: 'byo-cluster-${role}'
    scope: resourceGroup(clusterParts[2], clusterParts[4])
    params: {
      resourceName: last(clusterParts)
      principalId: hosting.properties.principalId
      roleDefinitionId: subscriptionResourceId(clusterParts[2], 'Microsoft.Authorization/roleDefinitions', role)
      deploymentId: plan.deploymentId
    }
  }
]

module federationGrant 'byo-identity-role-assignment.bicep' = {
  name: 'byo-federation-access'
  params: {
    resourceName: workload.name
    principalId: hosting.properties.principalId
    roleDefinitionId: plan.federatedIdentityRoleDefinitionId
    deploymentId: plan.deploymentId
  }
}

module storageGrant 'byo-storage-role-assignment.bicep' = {
  name: 'byo-storage-access'
  params: {
    resourceName: storage.name
    principalId: workload.properties.principalId
    roleDefinitionId: blobContributorRoleId
    deploymentId: plan.deploymentId
  }
}

output ownership object = {
  hostingIdentityResourceId: hosting.id
  workloadIdentityResourceId: workload.id
  storageAccountResourceId: storage.id
  ownedStorage: true
  ownedRegistry: true
  ownedHostingIdentity: true
  ownedWorkloadIdentity: true
  hostingPrincipalId: hosting.properties.principalId
  workloadPrincipalId: workload.properties.principalId
  roleAssignments: [
    clusterGrants[0].outputs.grant
    clusterGrants[1].outputs.grant
    federationGrant.outputs.grant
    storageGrant.outputs.grant
  ]
}

targetScope = 'resourceGroup'

param location string
param tags object
param plan object

var ownershipTags = union(tags, { 'azd-byoaks-owner': plan.owner })
var clusterParts = split(plan.clusterResourceId, '/')
var storageParts = split(plan.storageAccountResourceId, '/')
var registryParts = split(plan.registryResourceId, '/')
var registryRole = plan.registryABAC ? 'b93aa761-3e63-49ed-ac28-beffa264f7ac' : '7f951dda-4ed3-4680-a7ca-43fe172d538d'

resource hosting 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: plan.hostingIdentityName
  location: location
  tags: ownershipTags
}

resource workload 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: plan.workloadIdentityName
  location: location
  tags: ownershipTags
}

resource storage 'Microsoft.Storage/storageAccounts@2023-05-01' = if (!plan.storageSupplied) {
  name: plan.storageName
  location: location
  tags: ownershipTags
  kind: 'StorageV2'
  sku: { name: plan.storageSku }
  properties: {
    accessTier: 'Hot'
    isHnsEnabled: false
    publicNetworkAccess: 'Enabled'
    networkAcls: {
      defaultAction: 'Allow'
    }
    minimumTlsVersion: 'TLS1_2'
    supportsHttpsTrafficOnly: true
    allowBlobPublicAccess: false
    allowSharedKeyAccess: false
  }
}

resource registry 'Microsoft.ContainerRegistry/registries@2025-03-01-preview' = if (!plan.registrySupplied) {
  name: plan.registryName
  location: location
  tags: ownershipTags
  sku: { name: 'Premium' }
  properties: {
    roleAssignmentMode: 'LegacyRegistryPermissions'
    adminUserEnabled: false
    publicNetworkAccess: 'Enabled'
  }
}

module clusterGrants 'byo-role-assignment.bicep' = [for role in [
  'acdd72a7-3385-48ef-bd42-f606fba81ae7'
  '85cb6faf-e071-4c9b-8136-154b5a04f717'
]: {
  name: 'byo-cluster-${role}'
  scope: resourceGroup(clusterParts[2], clusterParts[4])
  params: {
    resourceName: last(clusterParts)
    principalId: hosting.properties.principalId
    roleDefinitionId: subscriptionResourceId(clusterParts[2], 'Microsoft.Authorization/roleDefinitions', role)
    owner: plan.owner
  }
}]

module federationGrant 'byo-identity-role-assignment.bicep' = {
  name: 'byo-federation-access'
  params: {
    resourceName: workload.name
    principalId: hosting.properties.principalId
    roleDefinitionId: plan.federatedIdentityRoleDefinitionId
    owner: plan.owner
  }
}

module storageGrant 'byo-storage-role-assignment.bicep' = {
  name: 'byo-storage-access'
  scope: resourceGroup(storageParts[2], storageParts[4])
  params: {
    resourceName: last(storageParts)
    principalId: workload.properties.principalId
    roleDefinitionId: subscriptionResourceId(storageParts[2], 'Microsoft.Authorization/roleDefinitions', 'ba92f5b4-2d11-453d-a403-e96b0029c9fe')
    owner: plan.owner
  }
  dependsOn: [storage]
}

module kubeletGrant 'acr-pull-role-assignment.bicep' = if (empty(plan.kubeletRoleAssignmentId)) {
  name: 'byo-kubelet-registry-access'
  scope: resourceGroup(registryParts[2], registryParts[4])
  params: {
    registryName: last(registryParts)
    principalId: plan.kubeletObjectId
    roleDefinitionId: subscriptionResourceId(registryParts[2], 'Microsoft.Authorization/roleDefinitions', registryRole)
    owner: plan.owner
  }
  dependsOn: [registry]
}

output ownership object = {
  owner: plan.owner
  hostingIdentityResourceId: hosting.id
  workloadIdentityResourceId: workload.id
  storageAccountResourceId: plan.storageAccountResourceId
  registryResourceId: plan.registryResourceId
  ownedStorage: !plan.storageSupplied
  ownedRegistry: !plan.registrySupplied
  hostingPrincipalId: hosting.properties.principalId
  workloadPrincipalId: workload.properties.principalId
  kubeletPrincipalId: plan.kubeletObjectId
  roleAssignments: concat(
    [clusterGrants[0].outputs.grant, clusterGrants[1].outputs.grant],
    [federationGrant.outputs.grant],
    [storageGrant.outputs.grant],
    empty(plan.kubeletRoleAssignmentId) ? [kubeletGrant!.outputs.grant] : []
  )
}

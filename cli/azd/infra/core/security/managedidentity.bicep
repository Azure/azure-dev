param location string = resourceGroup().location
param tags object = {}
param identityName string

// Create the user-assigned managed identity
resource managedIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2018-11-30' = {
  name: identityName
  tags: tags
  location: location
}

output userAssignedIdentity object = managedIdentity
output clientId string = managedIdentity.properties.clientId
output userAssignedIdentityID string = managedIdentity.id
output principalId string = managedIdentity.properties.principalId

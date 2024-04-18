metadata description = 'Creates a user assigned managed identity.'
param identityName string
param location string = resourceGroup().location
param tags object = {}

resource apiIdentity 'Microsoft.ManagedIdentity/userAssignedIdentities@2023-01-31' = {
  name: identityName
  location: location
  tags: tags
}

output identityName string = identityName
output principalId string = apiIdentity.properties.principalId
